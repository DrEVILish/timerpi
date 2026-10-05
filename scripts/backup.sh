#!/usr/bin/env bash
# TimerPi — hot SQLite backup (MY FILE: scripts/, ops-agent).
#
# Runs while timerpi.service KEEPS Serving (no stop, no restart). Strategy:
#   1. sqlite3 CLI present  → `sqlite3 .backup` into a temp file (halves the
#      db against a live WAL in a separate consistent copy) — preferred.
#   2. python3 stdlib       → sqlite3.Connection.backup() — same guarantee,
#      zero extra packages (python3 is preinstalled on bookworm).
#   3. Neither             → plain cp of db + wal + shm, clearly marked
#      best-effort in the filename-side *.note (WAL caveat, docs/OPS.md §5).
# Every artifact is integrity-gated: the SQLite header must read
# "SQLite format 3\0" (15 useful bytes: 'SQLite format ' + '3').
#
# Output: /var/lib/timerpi/backups/<ts>.db.gz (+ optional <ts>.config.json
# sidecar — config ALWAYS included when present). Keeps N=14 newest .db.gz,
# dir 0750, files 0640 (read/backup-group only; the exec bit would be noise
# on a data file, hence 0640 and not the dir's 0750).
#
# Usage: backup.sh [--data-dir DIR] [--keep N] [--quiet]
set -uo pipefail

DATA_DIR="${TIMERPI_DATA_DIR:-/var/lib/timerpi}"
KEEP=14
QUIET=0

say() { [ "$QUIET" -eq 1 ] || echo "[backup] $*"; }
die() { echo "[backup] FATAL: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    --keep)     KEEP="$2"; shift 2 ;;
    --quiet)    QUIET=1; shift ;;
    *) die "unknown arg: $1 (usage: backup.sh [--data-dir DIR] [--keep N] [--quiet])" ;;
  esac
done

DB="$DATA_DIR/timerpi.db"
BK="$DATA_DIR/backups"
CONF="$DATA_DIR/config.json"
TS="$(date -u +%Y%m%dT%H%M%SZ)"

[ -f "$DB" ] || die "no database at $DB (wrong --data-dir? service never ran?)"
mkdir -p "$BK" || die "cannot create $BK"
chmod 0750 "$BK"

snap_db() {  # snap_db SRC DST — one consistent copy of the live db, if we can
  local src="$1" dst="$2"
  rm -f "$dst"
  if command -v sqlite3 >/dev/null 2>&1; then
    say "snapshot: sqlite3 .backup"
    sqlite3 "$src" ".backup '$dst'" >/dev/null 2>&1 || return 1
  elif command -v python3 >/dev/null 2>&1; then
    say "snapshot: python3 sqlite3 backup() (sqlite3 CLI absent)"
    python3 - "$src" "$dst" <<'PY' || return 1
import sqlite3, sys
src, dst = sys.argv[1], sys.argv[2]
out = sqlite3.connect(dst)
out.execute("PRAGMA journal_mode=WAL")   # match source mode before copy
sqlite3.connect(src).backup(out)
out.close()
PY
  else
    # Last resort: copy db+wal+shm sequentially. NOT crash-consistent in
    # general — safe only if the WAL has been quiet (a show is running?).
    say "snapshot: plain cp (BEST-EFFORT — no sqlite3 CLI, no python3)"
    cp -f "$src" "$dst" || return 1
    for ext in -wal -shm; do
      [ -f "$src$ext" ] && cp -f "$src$ext" "$dst$ext"
    done
    echo "best-effort copy; install sqlite3 or python3 for a real snapshot" > "$dst.note"
    return 0
  fi
  [ -n "$(head -c 15 "$dst" 2>/dev/null | grep -c 'SQLite format 3')" ] \
    || { rm -f "$dst"; return 1; }
  return 0
}

TMP="$BK/.tmp-$TS.db"
rm -f "$TMP" "$TMP-wal" "$TMP-shm"
say "data dir: $DATA_DIR  (db $(du -h "$DB" | cut -f1) — hot, service untouched)"
if ! snap_db "$DB" "$TMP"; then
  rm -f "$TMP" "$TMP-wal" "$TMP-shm"
  die "snapshot failed (snapshot tool errored) — service left running, nothing written"
fi

OUT="$BK/$TS.db.gz"
gzip -c "$TMP" > "$OUT.tmp" && [ -s "$OUT.tmp" ] \
  || { rm -f "$OUT.tmp" "$TMP" "$TMP-wal" "$TMP-shm"; die "gzip produced nothing — aborting before rotation"; }
chmod 0640 "$OUT.tmp"
mv -f "$OUT.tmp" "$OUT"    # atomic-enough: same dir, same fs
rm -f "$TMP" "$TMP-wal" "$TMP-shm"
say "wrote: $OUT ($(du -h "$OUT" | cut -f1))"

# Config is tiny and ALWAYS travels with the backup when present.
if [ -f "$CONF" ]; then
  cp -f "$CONF" "$BK/$TS.config.json"
  chmod 0640 "$BK/$TS.config.json"
  say "wrote: $BK/$TS.config.json (sidecar)"
fi

# Rotate: keep $KEEP newest .db.gz, drop the deleted entries' sidecars with them.
say "rotation: keeping $KEEP newest"
n=0
for f in $(ls -1t "$BK"/*.db.gz 2>/dev/null); do
  n=$((n + 1))
  [ "$n" -le "$KEEP" ] && continue
  stem="${f%.db.gz}"
  rm -f "$f" "$stem.config.json" "$stem.note"
  say "rotated: $f (+sidecars)"
done

say "done — $(ls -1 "$BK"/*.db.gz 2>/dev/null | wc -l) backup(s) on disk"
exit 0
