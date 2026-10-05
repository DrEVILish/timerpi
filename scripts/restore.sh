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
mkdir -p "$WORK"

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

# 3. Stop → rollback copy → swap.
stop_srv || true
say "rollback copy: $DB → $DB.pre-restore.$(date -u +%Y%m%dT%H%M%SZ)"
if [ -f "$DB" ]; then
  cp -f "$DB" "$DB.pre-restore.$(date -u +%Y%m%dT%H%M%SZ)" || die "cannot copy the current db aside — aborting, nothing changed"
fi

if [ "$WITH_CONFIG" -eq 1 ]; then
  NEWCONF="${BACKUP%.db.gz}.config.json"
  if [ -f "$NEWCONF" ]; then
    cp -f "$CONF" "$CONF.pre-restore" 2>/dev/null
    cp -f "$NEWCONF" "$CONF"
    say "config restored from sidecar: $NEWCONF (previous saved as $CONF.pre-restore)"
  else
    say "--with-config requested but no sidecar next to the backup — current config kept"
  fi
else
  say "config.json kept as-is (use --with-config to restore the sidecar too)"
fi

# WAL/SHM of the *current* db belong to the old instance — the fresh file
# lands without them, so anything not checkpointed into $DB at backup time
# would be LOST. That is exactly the documented backup caveat; warn loudly.
for ext in -wal -shm; do
  [ -f "$DB$ext" ] && { say "removing stale $DB$ext (belongs to the old db, not in the backup)"; rm -f "$DB$ext"; }
done
cp -f "$RAW" "$DB.new" && mv -f "$DB.new" "$DB" || die "swap failed — rollback copy is ready at $DB.pre-restore.*"
chmod 0640 "$DB"

# 4. Start → health → roll back on failure.
if start_srv; then
  if watch_health; then
    say "restore complete — db now serves $DB (from $BACKUP)"
    rm -rf "$WORK"
    exit 0
  fi
fi

say "FAILING — rolling back to the pre-restore copy"
PRE="$(ls -1t "$DATA_DIR"/timerpi.db.pre-restore.* 2>/dev/null | head -1)"
if [ -n "$PRE" ]; then
  cp -f "$PRE" "$DB.roll.new" && mv -f "$DB.roll.new" "$DB" && chmod 0640 "$DB"
  say "rolled back: $PRE"
fi
start_srv
if watch_health; then say "rollback verified — service healthy again (database state = pre-restore)"; fi
rm -rf "$WORK"
exit 1
