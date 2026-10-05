#!/bin/sh
# Regenerate the embedded font atlases for drm/ (drm/assets/*.png + metrics.json).
#
# This directory is a self-contained Go module with its own go.mod, so the
# font dependencies (golang.org/x/image) never touch TimerPi's go.mod. The
# runtime reads only the committed PNG/JSON output.
#
# Usage: ./run.sh [ShareTechMono-Regular.ttf]
set -e
cd "$(dirname "$0")"

if [ "$#" -ge 1 ]; then
	cp -f "$1" ShareTechMono-Regular.ttf
fi
if [ ! -f ShareTechMono-Regular.ttf ]; then
	echo "copying the reference font from /opt/capacitimer (read-only checkout)"
	cp -f /opt/capacitimer/web-server/fonts/Share_Tech_Mono/ShareTechMono-Regular.ttf .
fi

# Offline build: everything x/image needs is already in the module cache of
# the dev container. Regenerate go.sum only inside this nested module.
export GOFLAGS=-mod=mod
# Pin versions compatible with the Go 1.25 toolchain (newest x/image needs 1.26).
if ! grep -q golang.org/x/image go.sum 2>/dev/null; then
  go get golang.org/x/image@v0.18.0 >/dev/null 2>&1 || true
fi
[ -f go.sum ] || go mod tidy >/dev/null

go run . 
