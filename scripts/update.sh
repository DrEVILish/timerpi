#!/usr/bin/env bash
# TimerPi — safe updater (MY FILE: scripts/, ops-agent).
#
# Order of operations — NOTHING touches the running service until the
# whole pipeline is green:
#   1. go vet + go test (abort on any failure; the old binary keeps serving)
#   2. make build   (+ make build-arm64 when the cross toolchain is here,
#                     so the Pi-ready artifact never lags the x86 one)
#   3. stage bin/timerpi.new
#   4. "already latest" fast-path via the stamp file (idempotent re-runs)
#   5. install the build-stamp systemd drop-in (see systemd-internal note)
#   6. restart gate: DEFAULT = restart; the service may veto via marker
#      file /var/lib/timerpi/.update-restart-or-manual containing "manual"
#      — the door stays open for a mid-show "stage only, I'll restart at
#      the break" workflow; --no-restart skips the restart explicitly.
#   7. atomic mv onto bin/timerpi (previous binary kept as bin/timerpi.bak)
#   8. systemctl restart + /health watched for 30 s
#   9. on failure: roll bin/timerpi.bak back, restart, re-verify, exit 1
#
# Both worlds: run as-is in the dev container (systemd present if the unit
# is installed there; otherwise it merely SAYS what it would do) and on
# the Pi (the real target). Prints every step — this script is the one a
# nervous operator should be able to read end to end.
#
# Usage: update.sh [--no-restart] [--skip-tests] [--data-dir DIR]
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$REPO/bin/timerpi"
DATA_DIR="${TIMERPI_DATA_DIR:-/var/lib/timerpi}"
DO_RESTART=1
SKIP_TESTS=0

say() { echo "[update] $*"; }
die() { echo "[update] FATAL: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --no-restart) DO_RESTART=0; shift ;;
    --skip-tests) SKIP_TESTS=1; shift ;;
    --data-dir)   DATA_DIR="$2"; shift 2 ;;
    *) die "unknown arg: $1" ;;
  esac
done
cd "$REPO" || die "cannot cd into repo $REPO"

TS="$(date -u +%Y%m%dT%H%M%SZ)"
BUILD_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
STAMP_FILE="/var/lib/timerpi/update.stamp"          # make-safe redirect below
MARKER="/var/lib/timerpi/.update-restart-or-manual"
DROP_IN_DIR="/etc/systemd/system/timerpi.service.d"   # unit drop-ins: <unit>.d == timerpi.service.d for timerpi.service
DROP_IN="$DROP_IN_DIR/29-timerpi-build-stamp.conf"

# /var/lib is root-only in the unit's world; fall back next to the repo in
# dev environments without the appliance layout.
if [ ! -d "/var/lib/timerpi" ] || [ ! -w "/var/lib/timerpi" ]; then
  STAMP_FILE="$REPO/data/update.stamp"
  MARKER="$REPO/data/.update-restart-or-manual"
  say "note: /var/lib/timerpi not writable here — stamp/marker redirect to $REPO/data/"
fi

want_restart() {
  [ "$DO_RESTART" -eq 0 ] && return 1
  if [ -f "$MARKER" ] && grep -qiE 'manual|skip' "$MARKER" 2>/dev/null; then
    return 1
  fi
  return 0
}

# Step 5: build-stamp drop-in (documented in the template itself and OPS.md).
# main.go embeds NO version constant — the stamp lives here + in the journal
# ExecStartPre (see systemd/timerpi-build-stamp.drop.in for the contract).
# Runs in EVERY path (also already-latest) so a missing/stale drop-in
# self-heals on any update run.
install_drop_in() {
  local dstamp="$1" dts="$2"
  if ! { systemctl is-system-running >/dev/null 2>&1 || [ -d /run/systemd/system ]; }; then
    say "note (drop-in): no running systemd here (dev); skipped"
    return 0
  fi
  if ! mkdir -p "$DROP_IN_DIR" 2>/dev/null; then
    say "note (drop-in): cannot create $DROP_IN_DIR — skipped"
    return 0
  fi
  if awk -v stamp="$dstamp" -v ts="$dts" \
      '{gsub(/__VSTAMP__/, stamp); gsub(/__BUILD_TS__/, ts); print}' \
      "$REPO/systemd/timerpi-build-stamp.drop.in" > "$DROP_IN" \
     && chmod 0644 "$DROP_IN" \
     && systemctl daemon-reload 2>/dev/null; then
    say "step 5/8 — build-stamp drop-in installed: $DROP_IN (vstamp=$dstamp)"
  else
    say "note: drop-in install failed (harmless — /health just has no stamp yet)"
  fi
}

verify_health() {
  local port p conf="$DATA_DIR/config.json"
  p="${TIMERPI_HTTP_PORT:-${CAPACITIMER_HTTP_PORT:-}}"
  if [ -z "$p" ] && [ -f "$conf" ]; then
    p="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("http_port",80))' "$conf" 2>/dev/null || echo 80)"
  fi
  port="${p:-80}"
  for _ in $(seq 1 30); do
    if curl -fsS --max-time 3 "http://localhost:$port/health" 2>/dev/null | grep -q '"ok":true'; then
      say "health: /health ok=true (port $port)"
      return 0
    fi
    sleep 1
  done
  say "health: FAIL — no 200/ok:true from port $port within 30 s"
  return 1
}

say "step 1/8 — go vet"
go vet ./... || die "go vet failed — keeping the running build untouched"
if [ "$SKIP_TESTS" -ne 1 ]; then
  say "step 1/8 — go test ./..."
  go test ./... || die "tests failed — keeping the running build untouched"
else
  say "step 1/8 — go test SKIPPED (--skip-tests)"
fi

say "step 2/8 — make build (x86, staged next to the live binary)"
# Staging builds to a SEPARATE target (CLI var overrides the Makefile's
# BINARY :=) so the live bin/timerpi is never touched by the compiler, and
# step 3's "already latest" cmp compares against the actually-running
# binary — not against make's own fresh output.
NEW="$BIN.new"
rm -f "$NEW"
make build BINARY="$NEW" || die "make build failed"
[ -s "$NEW" ] || die "staged binary is empty — build produced nothing"
say "     staged: $NEW ($(du -h "$NEW" | cut -f1))"

# vstamp = short sha256 of the exact staged binary + build timestamp.
VSTAMP="$(sha256sum "$NEW" | cut -c1-16)"

if command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; then
  say "step 2/8 — make build-arm64 (cross toolchain present → Pi artifact refreshed too)"
  make build-arm64 || die "make build-arm64 failed"
else
  say "step 2/8 — make build-arm64 SKIPPED (no aarch64-linux-gnu-gcc; stage from the build host)"
fi

# Idempotency: identical staged binary → already latest.
if [ -f "$BIN" ] && cmp -s "$BIN" "$NEW"; then
  say "step 3/8 — already latest: staged binary is byte-identical to the running one"
  install_drop_in "$VSTAMP" "$BUILD_TS"
  mkdir -p "$(dirname "$STAMP_FILE")"
  printf 'vstamp=%s build=%s updated=%s\n' "$VSTAMP" "$BUILD_TS" "$TS" > "$STAMP_FILE" 2>/dev/null || \
    say "note: could not write $STAMP_FILE (skipping stamp update)"
  rm -f "$NEW"
  if [ "$DO_RESTART" -eq 1 ]; then
    if want_restart; then
      say "step 4/8 — restart (unchanged binary, marker permits)"
      systemctl restart timerpi && verify_health || true
    else
      say "step 4/8 — restart vetoed by marker: $MARKER (contains manual/skip)"
    fi
  else
    say "step 4/8 — --no-restart: nothing restarted (nothing changed anyway)"
  fi
  say "done — already-latest exit; stamp: $(cat "$STAMP_FILE" 2>/dev/null || echo n/a)"
  exit 0
fi

install_drop_in "$VSTAMP" "$BUILD_TS"

mkdir -p "$(dirname "$STAMP_FILE")"
printf 'vstamp=%s build=%s updated=%s\n' "$VSTAMP" "$BUILD_TS" "$TS" > "$STAMP_FILE" 2>/dev/null \
  || say "note: could not write stamp file $STAMP_FILE"
say "stamp: vstamp=$VSTAMP build=$BUILD_TS (file $STAMP_FILE, journal print via drop-in)"

# Restart gate (default: restart; marker file may veto; --no-restart skips).
if want_restart; then
  say "step 6/8 — atomic swap: bin/timerpi.bak ← old; bin/timerpi ← staged (cp + mv)"
  cp -f "$BIN" "$BIN.bak" 2>/dev/null || say "note: no previous binary to back up"
  mv -f "$NEW" "$BIN" || die "swap failed — old binary still in place, nothing lost"
  say "step 7/8 — systemctl restart timerpi and watch /health (30 s)"
  if systemctl restart timerpi && verify_health; then
    say "step 8/8 — done: new binary live, stamp $VSTAMP recorded"
  else
    say "FAILING — rolling back to bin/timerpi.bak"
    [ -f "$BIN.bak" ] || die "no backup binary exists — cannot roll back automatically"
    cp -f "$BIN.bak" "$BIN"
    systemctl restart timerpi || true
    if verify_health; then
      say "rolled back and healthy again — run 'journalctl -u timerpi -n 50' for the failure"
    else
      say "rollback restarted the service but /health still fails — check journalctl -u timerpi"
    fi
    exit 1
  fi
elif [ "$DO_RESTART" -eq 0 ] && [ -f "$NEW" ]; then
  say "step 6/8 — --no-restart: staged binary kept at $NEW"
  say "          apply later with:  cp -f $NEW $BIN && systemctl restart timerpi"
else
  say "step 6/8 — restart vetoed by marker: $MARKER contains manual/skip"
  say "          staged binary kept at $NEW; clear the marker and re-run to apply"
fi
exit 0
