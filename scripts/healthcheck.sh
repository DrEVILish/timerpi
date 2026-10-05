#!/usr/bin/env bash
# TimerPi — appliance health pulse (MY FILE: scripts/, ops-agent).
#
# Success criteria (duplicated from the unit doc / docs/OPS.md §5, the two
# MUST stay in sync):
#   * /health answers HTTP 200  (curl -f gates redirects & >=400 codes)
#   * body contains "ok":true
# A non-200 (e.g. the origin guard's 421 under strict allowed_hosts) is a
# FAIL by definition.
#
# Behaviour: prints one status line (JOURNAL-FRIENDLY — the systemd timer
# makes this line the whole signal), records state to
# /var/lib/timerpi/health.last ("<iso-utc> state=OK|FAIL <detail>"), and
# exits 0/1. Failure hook: pid-flag /var/lib/timerpi/health.failed
# (created on FAIL, removed on OK) so any watcher —
# `test -e /var/lib/timerpi/health.failed` — can react without journal
# scraping. See reviews/NOTES-ops.md for the OnFailure= pairing note.
#
# Port: $CAPACITIMER_HTTP_PORT > config.json http_port > 80 (mirrors the
# unit's pinned env; sandbox/dev data dirs get :
# PORT from their config.json).
set -u

DATA_DIR="${TIMERPI_DATA_DIR:-/var/lib/timerpi}"

port() {
  local p="${CAPACITIMER_HTTP_PORT:-}"
  local conf="$DATA_DIR/config.json"
  if [ -z "$p" ] && [ -f "$conf" ]; then
    if command -v python3 >/dev/null 2>&1; then
      p="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("http_port",80))' "$conf" 2>/dev/null)"
    fi
    # no python3 and no env → fall through to the appliance default 80
  fi
  echo "${p:-80}"
}

URL="http://localhost:$(port)/health"
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LAST="$DATA_DIR/health.last"
FLAG="$DATA_DIR/health.failed"
mkdir -p "$DATA_DIR" 2>/dev/null

BODY="$(curl -fsS --max-time 5 "$URL" 2>/dev/null)"
detail="down"
state="FAIL"
if [ -n "$BODY" ] && printf '%s' "$BODY" | grep -q '"ok":true'; then
  state="OK"
  detail="$BODY"
  rm -f "$FLAG"
else
  [ -f "$FLAG" ] || { : > "$FLAG"; chmod 0644 "$FLAG"; }
fi

printf '%s state=%s detail=%s\n' "$NOW" "$state" "$(printf '%s' "$detail" | cut -c1-200)" > "$LAST"

if [ "$state" = "OK" ]; then
  echo "[health $NOW] OK $URL — $(printf '%s' "$detail" | cut -c1-160)"
  exit 0
fi
# NOTE (journal hook): running under systemd, this non-zero exit already
# produces "journalctl -u timerpi-healthcheck.service" lines; an
# OnFailure= unit pair is documented in reviews/NOTES-ops.md, not shipped.
echo "[health $NOW] FAIL $URL — no 200/ok:true (port $(port)); see docs/OPS.md §5" >&2
exit 1
