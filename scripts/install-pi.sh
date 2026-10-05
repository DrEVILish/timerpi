#!/usr/bin/env bash
# TimerPi — Pi installer (root, idempotent).
#
# Deploys built binaries, systemd units and the splash asset onto a
# Raspberry Pi 4/5 (64-bit Raspberry Pi OS bookworm), prepares the data
# directory, pins the HDMI mode to 1080p50 in the kernel command line and
# (optionally) renames the host, which mDNS/avahi then publish.
#
# Usage (on the Pi, from the checkout, or anywhere with --src):
#   sudo ./scripts/install-pi.sh
#   sudo ./scripts/install-pi.sh --src /path/to/stage --hostname stage-left
#
# Flags:
#   --src DIR        source tree with bin/, timerpi*.service, public/img/
#                    (default: the repo dir this script lives in)
#   --boot-dir DIR   firmware dir containing cmdline.txt
#                    (default: try /boot/firmware, then /boot)
#   --hostname NAME  set the OS hostname (and /etc/hosts); avahi (if
#                    present) republishes <name>.local automatically
#   --dry-run        print every action, touch nothing
#
# Env overrides (for container/testing, implied by --dry-run too):
#   PREFIX=/tmp/...  root of the deployment tree instead of /
#
# Build the Pi binaries first:
#   make build-arm64                                   # timerpi (CGO)
#   CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
#     go build -o bin/splash-draw-arm64 ./scripts/splash
#
# See docs/PI-DEPLOY.md for the flash→boot→verify checklist.
#
# Exit 0 and no changes on a second run: the kernel-cmdline token, dir
# setup and file installs are all re-checks; the cmdline backup is only
# written on the FIRST real modification.

set -euo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

SRC=""
BOOT_DIR_ARG=""
HOSTNAME_ARG=""
DRY_RUN=0
PREFIX="${PREFIX:-/}"

log() { printf '%s\n' "$*"; }
run() { # run DESC cmd...
  local desc="$1"; shift
  if (( DRY_RUN )); then log "DRY-RUN  ${desc}"; return 0; fi
  "$@"
}

if [[ $EUID -ne 0 && "${PREFIX:-/}" == "/" ]]; then
  log "ERROR: run as root (or set PREFIX to a staging tree for tests)" >&2
  exit 1
fi

die() { log "ERROR: $*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --src) SRC="${2:?missing arg}"; shift 2 ;;
    --boot-dir) BOOT_DIR_ARG="${2:?missing arg}"; shift 2 ;;
    --hostname) HOSTNAME_ARG="${2:?missing arg}"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown argument: $1 (try --help)" ;;
  esac
done

[[ -z "$SRC" ]] && SRC="$SELF_DIR/.."
SRC="$(cd "$SRC" && pwd)"

OPT_DIR="$PREFIX/opt/timerpi"
VAR_DIR="$PREFIX/var/lib/timerpi"
UNIT_DIR="$PREFIX/etc/systemd/system"
TMPFILES_DIR="$PREFIX/etc/tmpfiles.d"

# Where binaries live in the source tree (cross-compiled or native).
if [[ -f "$SRC/bin/timerpi-arm64" ]]; then
  TIMERPI_BIN="$SRC/bin/timerpi-arm64"
elif [[ -f "$SRC/bin/timerpi" ]]; then
  TIMERPI_BIN="$SRC/bin/timerpi"
else
  die "no Pi binary in $SRC/bin — run 'make build-arm64' (and the splash build) first"
fi
if [[ -f "$SRC/bin/splash-draw-arm64" ]]; then
  SPLASH_BIN="$SRC/bin/splash-draw-arm64"
elif [[ -f "$SRC/bin/splash-draw" ]]; then
  SPLASH_BIN="$SRC/bin/splash-draw"
else
  die "no splash binary in $SRC/bin — 'CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/splash-draw-arm64 ./scripts/splash'"
fi

[[ -f "$SRC/timerpi.service" ]] || die "missing $SRC/timerpi.service"
[[ -f "$SRC/timerpi-splash.service" ]] || die "missing $SRC/timerpi-splash.service"
[[ -f "$SRC/public/img/timerpi-512.png" ]] || die "missing $SRC/public/img/timerpi-512.png (splash logo asset)"

# If `file` can verify, the appliance binary must be aarch64 (or contain
# aarch64 in an object list). Warn loudly, don't stop: the Pi may run a
# native build staged under bin/timerpi.
if command -v file >/dev/null 2>&1; then
  if ! file -b "$TIMERPI_BIN" | grep -qi "aarch64\|arm64\|arm aarch64"; then
    if [[ "$PREFIX" == "/" ]]; then
      log "WARNING: $TIMERPI_BIN does not look like an aarch64 build ($(file -b "$TIMERPI_BIN" | cut -c1-80))"
      log "         the Pi target runs arm64 — check 'make build-arm64'"
    fi
  fi
fi

log "TimerPi install:  src=$SRC  prefix=$PREFIX"

# ---- 1. Directories ----------------------------------------------------
run "create $OPT_DIR (app home)"  mkdir -p "$OPT_DIR/bin" "$OPT_DIR/public/img"
run "create $VAR_DIR (data home)" mkdir -p "$VAR_DIR"
chmod 0755 "$OPT_DIR" 2>/dev/null || true
chmod 0755 "$VAR_DIR" 2>/dev/null || true

# ---- 2. Binaries, units, splash asset ----------------------------------
run "install timerpi binary"      install -m 0755 "$TIMERPI_BIN" "$OPT_DIR/bin/timerpi"
run "install splash-draw binary"  install -m 0755 "$SPLASH_BIN"  "$OPT_DIR/bin/splash-draw"
run "install timerpi-512.png"     install -m 0644 "$SRC/public/img/timerpi-512.png" "$OPT_DIR/public/img/timerpi-512.png"

run "ensure $UNIT_DIR"           mkdir -p "$UNIT_DIR"
run "install timerpi.service"           install -m 0644 "$SRC/timerpi.service" "$UNIT_DIR/timerpi.service"
run "install timerpi-splash.service"    install -m 0644 "$SRC/timerpi-splash.service" "$UNIT_DIR/timerpi-splash.service"

# /run/timerpi exists from boot (splash polls it before the app runs).
run "ensure $TMPFILES_DIR"        mkdir -p "$TMPFILES_DIR"
run "install tmpfiles.d/timerpi.conf" bash -c "printf 'D /run/timerpi 0755 root root -\n' > '$TMPFILES_DIR/timerpi.conf'"

# ---- 3. Kernel command line: pin the HDMI mode to 1080p50 --------------
DESIRED_VIDEO="video=HDMI-A-1:1920x1080@50e"
if [[ -n "$BOOT_DIR_ARG" ]]; then
  BOOT_DIR="$BOOT_DIR_ARG"
else
  if [[ -d "$PREFIX/boot/firmware" ]]; then
    BOOT_DIR="$PREFIX/boot/firmware"
  else
    BOOT_DIR="$PREFIX/boot"
  fi
fi
CMDLINE="$BOOT_DIR/cmdline.txt"
if [[ ! -f "$CMDLINE" ]]; then
  log "WARNING: no $CMDLINE — skipping the 1080p50 HDMI pin (install Raspberry Pi OS in full mode first)"
else
  cmdline_content="$(cat "$CMDLINE")"
  # Matches the whole video=HDMI-A-1:… family, both spaced and stray forms.
  if [[ "$cmdline_content" =~ (^|[[:space:]])video=HDMI-A-1:[^[:space:]]*($|[[:space:]]) ]]; then
    token="$(printf '%s\n' "$cmdline_content" | grep -oE '(^|[[:space:]])video=HDMI-A-1:[^[:space:]]*' | head -n1)"
    token="$(echo "$token" | tr -d '[:space:]')"
    if [[ "$token" == "$DESIRED_VIDEO" ]]; then
      log "cmdline HDMI mode already pinned to $DESIRED_VIDEO"
    else
      log "cmdline: replacing '$token' with $DESIRED_VIDEO"
      [[ -f "$CMDLINE.timerpi.bak" ]] || run "backup $CMDLINE" cp "$CMDLINE" "$CMDLINE.timerpi.bak"
      run "rewrite $CMDLINE (1080p50)" sed -i "s/$token/$DESIRED_VIDEO/" "$CMDLINE"
    fi
  else
    log "cmdline: appending HDMI mode $DESIRED_VIDEO"
    [[ -f "$CMDLINE.timerpi.bak" ]] || run "backup $CMDLINE" cp "$CMDLINE" "$CMDLINE.timerpi.bak"
    run "append video mode to $CMDLINE" bash -c "printf '%s ' '$DESIRED_VIDEO' >> '$CMDLINE'"
  fi
fi

# ---- 4. Optional hostname (mDNS name) ----------------------------------
if [[ -n "$HOSTNAME_ARG" ]]; then
  if ! [[ "$HOSTNAME_ARG" =~ ^[a-zA-Z0-9][a-zA-Z0-9-]{0,62}$ ]]; then
    die "--hostname: '$HOSTNAME_ARG' is not a valid DNS label (letters/digits/hyphen)"
  fi
  if [[ "$PREFIX" == "/" ]]; then
    if command -v hostnamectl >/dev/null 2>&1; then
      run "hostnamectl set-hostname $HOSTNAME_ARG" hostnamectl set-hostname "$HOSTNAME_ARG"
    else
      run "write /etc/hostname"            bash -c "echo '$HOSTNAME_ARG' > /etc/hostname"
      run "set live hostname"              hostname "$HOSTNAME_ARG" 2>/dev/null || true
    fi
    # Keep the Debian /etc/hosts convention line in sync so sudo keeps working.
    if grep -qE '^127\.0\.1\.1[[:space:]]' /etc/hosts 2>/dev/null; then
      run "update /etc/hosts 127.0.1.1"    sed -i -E "s#^(127\.0\.1\.1[[:space:]]+).*#\\1$HOSTNAME_ARG#" /etc/hosts
    else
      run "add /etc/hosts 127.0.1.1 line"  bash -c "printf '127.0.1.1\t%s\n' '$HOSTNAME_ARG' >> /etc/hosts"
    fi
    # avahi-daemon publishes <hostname>.local on the LAN natively — just
    # (re)enable it; the app's zeroconf announce carries the service type.
    if command -v systemctl >/dev/null 2>&1; then
      if systemctl list-unit-files 2>/dev/null | grep -q '^avahi-daemon\.service'; then
        run "enable avahi-daemon (mDNS .local publish)" systemctl enable avahi-daemon 2>/dev/null || true
        run "restart avahi-daemon"                      systemctl restart avahi-daemon 2>/dev/null || true
      fi
    fi
  else
    log "DRY-RUN  would set hostname to '$HOSTNAME_ARG' (skipped: testing prefix)"
  fi
fi

# ---- 5. systemd enablement --------------------------------------------
if command -v systemctl >/dev/null 2>&1 && [[ "$PREFIX" == "/" && -d /run/systemd/system ]]; then
  run "systemctl daemon-reload" systemctl daemon-reload || true
  run "enable timerpi-splash (boot splash)" systemctl enable timerpi-splash 2>/dev/null || true
  run "enable timerpi"                      systemctl enable timerpi 2>/dev/null || true
  run "restart timerpi"                     systemctl restart timerpi
elif (( ! DRY_RUN )); then
  log "NOTE: systemd not active here — units installed but not enabled"
fi

# ---- 6. Next steps ------------------------------------------------------
log
log "TimerPi is installed. Next steps:"
log "  1. systemctl status timerpi            # expect active; journalctl -u timerpi for logs"
log "  2. curl -s http://localhost/health     # {\"ok\":true,...} means it serves on :80"
log "  3. edit $VAR_DIR/config.json (title, auth_password, allowed_hosts) then restart timerpi"
log "  4. from another host: avahi-browse -rt _timerpi._tcp"
log "  5. splash: /opt/timerpi/bin/splash-draw -clear wipes the fbdev manually"
log "Verify checklist: docs/PI-DEPLOY.md"
