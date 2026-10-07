# TimerPi — build targets.
#
# Native build is for the dev container; build-arm64 cross-compiles for the
# Pi (64-bit Raspberry Pi OS Lite, Trixie); build-amd64 is the cloud server
# (Debian Trixie, x86-64). go-sqlite3 needs CGO on all of them.
#
# VERSION names the build (buildinfo.Version). A release is signed for the
# boot-time updater (VENUE-CLOUD §14):
#   make release VERSION=3.0.0 RELEASE_KEY=~/timerpi-release.key

BINARY := bin/timerpi
CROSS_CC := aarch64-linux-gnu-gcc
VERSION ?= 3.0.0-dev
LDFLAGS := -X timerpi/buildinfo.Version=$(VERSION)

.PHONY: build build-arm64 build-amd64 release run test fmt vet clean install-pi

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

# Cloud server build (x86-64). Build it on Debian Trixie (or a Trixie
# container) so it links against the host's glibc.
build-amd64:
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/timerpi-amd64 .

# Signed release for both architectures: binaries + manifests in bin/.
# Publish them on the cloud under <data>/releases/<arch>/ (docs/OPS.md).
release: build-arm64 build-amd64
	@test -n "$(RELEASE_KEY)" || { echo "release: set RELEASE_KEY=<private key file>"; exit 1; }
	@case "$(VERSION)" in *dev*) echo "release: VERSION must be a release number, not $(VERSION)"; exit 1;; esac
	go run ./tools/release sign -key $(RELEASE_KEY) -bin bin/timerpi-arm64 -version $(VERSION) -arch arm64
	go run ./tools/release sign -key $(RELEASE_KEY) -bin bin/timerpi-amd64 -version $(VERSION) -arch amd64

# splash-draw: framebuffer splash binary (advisory; true target is arm64).
build-splash:
	go build -o bin/splash-draw ./scripts/splash

# Cross-compile for Pi 4/5 (arm64). Needs the aarch64 cross toolchain on
# this machine (the Pi itself needs no toolchain).
build-arm64:
	CGO_ENABLED=1 CC=$(CROSS_CC) GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/timerpi-arm64 .

# Splash for the Pi: pure Go (no CGO) framebuffer drawing.
build-splash-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/splash-draw-arm64 ./scripts/splash

# Dev run: separate data dir so the dev checkout never touches /var/lib.
run:
	TIMERPI_DATA_DIR=./data TIMERPI_HTTP_PORT=8080 TIMERPI_ROLE=cloud go run . # many events, like the cloud

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

browser-test: ## end-to-end browser checks (tools/browser-tests; needs npm install there once)
	cd tools/browser-tests && node run.mjs

update: ## full safe-update rehearsal: vet+test+build (both arches), stage, gate, restart, health, rollback
	bash scripts/update.sh

backup: ## hot SQLite backup (service keeps serving) into /var/lib/timerpi/backups
	bash scripts/backup.sh

restore: ## usage: make restore B=/var/lib/timerpi/backups/<ts>.db.gz [-- extra args..]
	bash scripts/restore.sh $(B)

healthcheck: ## curl /health and record /var/lib/timerpi/health.last
	bash scripts/healthcheck.sh

.PHONY: update backup restore healthcheck

