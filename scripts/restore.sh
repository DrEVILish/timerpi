#!/usr/bin/env bash
# TimerPi — deterministic restore (MY FILE: scripts/, ops-agent).
#
# Sequence (always the same): verify the backup → stop the service →
# put the current db aside (rollback copy) → swap the restored one in →
# start the service → watch /health for up to 30 s → on failure roll the
# pre-restore copy back and restart again.
#
# Safety: refuses to touch the LIVE data dir ($TIMERPI_DATA_DIR default
# /var/lib/timerpi) without --force. Sandbox/dry restores (e.g. a copy in
# /tmp) never need --force; with --no-service they don't touch systemd or
# /health at all — that is how this script is verified in CI/dev.
#
# Usage: restore.sh <backup.db.gz|backup.db> [--force] [--no-service]
#                    [--data-dir DIR] [--with-config]
set -uo pipefail

HEALTH_TIMEOUT=30
BACKUP=""
FORCE=0
NO_SERVICE=0
WITH_CONFIG=0
DATA_DIR="${TIMERPI_DATA_DIR:-/var/lib/timerpi}"

say() { echo "[restore] $*"; }
die() { echo "[restore] FATAL: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --force)       FORCE=1; shift ;;
    --no-service)  NO_SERVICE=1; shift ;;
    --data-dir)    DATA_DIR="$2"; shift 2 ;;
    --with-config) WITH_CONFIG=1; shift ;;
    -*) die "unknown flag: $1" ;;
    *)
      [ -z "$BACKUP" ] || die "multiple backup paths given"
      BACKUP="$1"; shift ;;
  esac
done
[ -n "$BACKUP" ] || die "usage: restore.sh <backup.db.gz|backup.db> [--force] [--no-service] [--data-dir DIR] [--with-config]"
[ -f "$BACKUP" ] || die "backup not found: $BACKUP"

DB="$DATA_DIR/timerpi.db"
CONF="$DATA_DIR/config.json"
LIVE=0
case "$DATA_DIR" in
  /var/lib/timerpi) LIVE=1 ;;
esac
if [ "$LIVE" -eq 1 ] && [ "$FORCE" -ne 1 ]; then
  die "refusing to restore over the LIVE data dir without --force
[restore] live target: $DB
[restore] dry-run against a sandbox instead:  cp -r /var/lib/timerpi /tmp/timerpi-sandbox
[restore]   ./restore.sh /var/lib/timerpi/backups/<ts>.db.gz --data-dir /tmp/timerpi-sandbox --no-service"
fi

WORK="$DATA_DIR/.restore-work-$$"
mkdir -p "$WORK" || die "cannot create restore workspace"
trap 'rm -rf "$WORK"' EXIT

# 1. Unpack (gz or plain) and gate on the SQLite header, then a real
#    integrity_check when a checker is available.
RAW="$WORK/timerpi.db"
case "$BACKUP" in
  *.gz) gzip -dc "$BACKUP" > "$RAW" || die "gunzip failed on $BACKUP" ;;
  *)    cp -f "$BACKUP" "$RAW" ;;
esac
[ -n "$(head -c 15 "$RAW" | grep -c 'SQLite format 3')" ] || die "$BACKUP is NOT a SQLite database (bad header) — aborting"
CHECK="skipped (no checker available)"
if command -v sqlite3 >/dev/null 2>&1; then
  CHECK="$(sqlite3 "$RAW" 'PRAGMA integrity_check;' 2>/dev/null)"
elif command -v python3 >/dev/null 2>&1; then
  CHECK="$(python3 -c 'import sqlite3,sys;print(sqlite3.connect(sys.argv[1]).execute("PRAGMA integrity_check").fetchone()[0])' "$RAW" 2>/dev/null)"
fi
[ "$CHECK" = "ok" ] || die "integrity_check returned '$CHECK' — refusing to install a suspect backup"
say "verified: $BACKUP → integrity_check=ok"

# 2. Service gate (sandbox mode / explicit opt-out skips systemd entirely).
stop_srv() {
  [ "$NO_SERVICE" -eq 1 ] && { say "no-service mode: skipping service stop"; return 0; }
  say "stopping timerpi.service"
  systemctl stop timerpi || die "systemctl stop timerpi failed"
}
start_srv() {
  [ "$NO_SERVICE" -eq 1 ] && return 0
  say "starting timerpi.service"
  systemctl start timerpi || { systemctl status timerpi --no-pager 2>/dev/null | tail -5; return 1; }
}
port() {  # /health port: env > config.json > 80
  local p="${TIMERPI_HTTP_PORT:-${CAPACITIMER_HTTP_PORT:-}}"
  if [ -z "$p" ] && command -v python3 >/dev/null 2>&1 && [ -f "$CONF" ]; then
    p="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("http_port",80))' "$CONF" 2>/dev/null || echo 80)"
  fi
  echo "${p:-80}"
}
health_ok() {
  curl -fsS --max-time 3 "http://localhost:$(port)/health" 2>/dev/null | grep -q '"ok":true'
}
watch_health() {
  [ "$NO_SERVICE" -eq 1 ] && { say "no-service mode: skipping /health gate"; return 0; }
  for _ in $(seq 1 "$HEALTH_TIMEOUT"); do
    if health_ok; then say "healthy: /health ok=true"; return 0; fi
    sleep 1
  done
  say "health gate: no /health ok=true within ${HEALTH_TIMEOUT}s"
  return 1
}

# 3. Stage the replacement before taking the service down.
stage_file() {
  local source="$1" target="$2" reference="$3" mode="$4"
  cp -f "$source" "$target" &&
    chown --reference="$reference" "$target" &&
    chmod "$mode" "$target"
}
REFERENCE="$DB"
[ -f "$REFERENCE" ] || REFERENCE="$DATA_DIR"
stage_file "$RAW" "$WORK/db.new" "$REFERENCE" 0640 || die "cannot stage replacement database"

NEWCONF="${BACKUP%.gz}"
NEWCONF="${NEWCONF%.db}.config.json"
if [ "$WITH_CONFIG" -eq 1 ] && [ -f "$NEWCONF" ]; then
  REFERENCE="$CONF"
  [ -f "$REFERENCE" ] || REFERENCE="$WORK/db.new"
  stage_file "$NEWCONF" "$WORK/config.new" "$REFERENCE" 0600 || die "cannot stage replacement config"
elif [ "$WITH_CONFIG" -eq 1 ]; then
  say "--with-config requested but no sidecar next to the backup — current config kept"
fi

PRE="$DB.pre-restore.$(date -u +%Y%m%dT%H%M%SZ)-$$"
STOPPED=0
SWAP_STARTED=0
CONFIG_CHANGED=0
START_ATTEMPTED=0
recover() {
  local result=$?
  trap - EXIT
  if [ "$STOPPED" -eq 1 ]; then
    say "restore failed — recovering the previous service"
    if [ "$START_ATTEMPTED" -eq 1 ]; then
      stop_srv
    fi
    if [ "$SWAP_STARTED" -eq 1 ]; then
      rm -f "$DB-wal" "$DB-shm" || die "cannot clear replacement WAL/SHM"
      if [ -f "$PRE" ]; then
        cp -p "$PRE" "$WORK/db.roll" && mv -f "$WORK/db.roll" "$DB" \
          || die "database rollback failed — original copy: $PRE"
        if [ -f "$PRE-wal" ]; then
          cp -p "$PRE-wal" "$DB-wal" || die "cannot restore original WAL"
        fi
      else
        rm -f "$DB" || die "cannot remove replacement database"
      fi
    fi
    if [ "$CONFIG_CHANGED" -eq 1 ]; then
      if [ -f "$WORK/config.original" ]; then
        mv -f "$WORK/config.original" "$CONF" || die "cannot restore original config"
      else
        rm -f "$CONF" || die "cannot remove replacement config"
      fi
    fi
    if start_srv && watch_health; then
      say "rollback verified — previous service healthy again"
    else
      say "previous service health could not be verified"
    fi
  fi
  rm -rf "$WORK"
  exit "$result"
}
trap recover EXIT

stop_srv
STOPPED=1
if [ -f "$DB" ]; then
  cp -p "$DB" "$PRE" || die "cannot save the current database"
  if [ -f "$DB-wal" ]; then
    cp -p "$DB-wal" "$PRE-wal" || die "cannot save the current WAL"
  fi
fi
if [ -f "$WORK/config.new" ] && [ -f "$CONF" ]; then
  cp -p "$CONF" "$WORK/config.original" && cp -p "$CONF" "$CONF.pre-restore" \
    || die "cannot save the current config"
fi

SWAP_STARTED=1
rm -f "$DB-wal" "$DB-shm" || die "cannot remove the previous WAL/SHM"
mv -f "$WORK/db.new" "$DB" || die "cannot install replacement database"
if [ -f "$WORK/config.new" ]; then
  mv -f "$WORK/config.new" "$CONF" || die "cannot install replacement config"
  CONFIG_CHANGED=1
fi

START_ATTEMPTED=1
if start_srv && watch_health; then
  STOPPED=0
  say "restore complete — db now serves $DB (from $BACKUP)"
  exit 0
fi
die "replacement service did not become healthy"
