# TimerPi — build targets.
#
# Native build is for the dev container; build-arm64 cross-compiles for the
# Pi (64-bit Raspberry Pi OS bookworm). go-sqlite3 needs CGO on both.

BINARY := bin/timerpi
CROSS_CC := aarch64-linux-gnu-gcc

.PHONY: build build-arm64 run test fmt vet clean install-pi

build:
	go build -o $(BINARY) .

# splash-draw: framebuffer splash binary (advisory; true target is arm64).
build-splash:
	go build -o bin/splash-draw ./scripts/splash

# Cross-compile for Pi 4/5 (arm64). Needs the aarch64 cross toolchain on
# this machine (the Pi itself needs no toolchain).
build-arm64:
	CGO_ENABLED=1 CC=$(CROSS_CC) GOOS=linux GOARCH=arm64 go build -o bin/timerpi-arm64 .

# Splash for the Pi: pure Go (no CGO) framebuffer drawing.
build-splash-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/splash-draw-arm64 ./scripts/splash

# Dev run: separate data dir so the dev checkout never touches /var/lib.
run:
	TIMERPI_DATA_DIR=./data TIMERPI_HTTP_PORT=8080 go run .

test:
	go test ./...

fmt:
	gofmt -w -s .

vet:
	go vet ./...

clean:
	rm -rf bin

# install-pi: run the full installer on the Pi (see scripts/install-pi.sh
# and docs/PI-DEPLOY.md). Typical flow from the build machine:
#
#   1. make build-arm64                      # timerpi (CGO cross build)
#   2. CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
#        go build -o bin/splash-draw-arm64 ./scripts/splash
#   3. scp -r bin/*.bin? scripts/install-pi.sh timerpi*.service public/img/timerpi-512.png \
#        root@<pi>:/opt/timerpi/
#   4. ssh root@<pi> 'cd /opt/timerpi && ./scripts/install-pi.sh --hostname <name>'
install-pi:
	bash scripts/install-pi.sh

# ─── Append-only ops block (ops-agent; anything below this line is new) ────
#
# BUILD_TIMESTAMP: exported so scripts/update.sh records it in the EPOCH
# stamp file (/var/lib/timerpi/update.stamp) and the systemd drop-in
# (systemd/timerpi-build-stamp.drop.in). main.go embeds NO version
# constant today — once a linkable var exists in package main, extend
# build/build-arm64 with `-ldflags "-X main.BuildTimestamp=$(BUILD_TIMESTAMP)"`
# (consolidation hook in reviews/NOTES-ops.md). Override per-invocation:
#   make build BUILD_TIMESTAMP='future-rev-N'
BUILD_TIMESTAMP ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
export BUILD_TIMESTAMP

update: ## full safe-update rehearsal: vet+test+build (both arches), stage, gate, restart, health, rollback
	bash scripts/update.sh

backup: ## hot SQLite backup (service keeps serving) into /var/lib/timerpi/backups
	bash scripts/backup.sh

restore: ## usage: make restore B=/var/lib/timerpi/backups/<ts>.db.gz [-- extra args..]
	bash scripts/restore.sh $(B)

healthcheck: ## curl /health and record /var/lib/timerpi/health.last
	bash scripts/healthcheck.sh

.PHONY: update backup restore healthcheck

