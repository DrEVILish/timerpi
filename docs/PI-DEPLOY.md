# PI-DEPLOY — TimerPi on Raspberry Pi 4/5

Flash → boot → verify checklist for the 1080p50 show-timer appliance.
Target: 64-bit Raspberry Pi OS bookworm (full install, not Lite-minimal —
systemd, avahi and framebuffer KMS come from the OS image), HDMI display
at 1920×1080@50, single binary serving HTTP+WS on port 80.

```
boot ─▶ firmware (cmdline pins the HDMI mode)
     ─▶ timerpi-splash.service  : logo on /dev/fb0, waits for /run/timerpi/ready
     ─▶ timerpi.service         : app in /opt/timerpi, data in /var/lib/timerpi
                                  writes /run/timerpi/ready → splash fades
```

## 1. Build (cross-compile on the build machine)

Go 1.25 + the `aarch64-linux-gnu-gcc` toolchain. The sqlite driver needs
CGO for the arm64 build (the splash is pure Go and builds without it):

```sh
make build-arm64            # → bin/timerpi-arm64   (CGO cross build)
make build-splash-arm64     # → bin/splash-draw-arm64
```

(The Makefile's prior `install-pi` placeholder now runs
`scripts/install-pi.sh`; the splash targets were added in the same pass.)

## 2. Stage & install on the Pi

Copy over the things `scripts/install-pi.sh` expects to find in `--src`
(defaults to the repo the script lives in):

```sh
ssh root@<pi> mkdir -p /opt/timerpi
scp bin/timerpi-arm64 root@<pi>:/opt/timerpi/bin/timerpi-arm64
scp bin/splash-draw-arm64 root@<pi>:/opt/timerpi/bin/splash-draw-arm64
scp timerpi.service timerpi-splash.service root@<pi>:/opt/timerpi/
scp public/img/timerpi-512.png root@<pi>:/opt/timerpi/public/img/
scp -r scripts root@<pi>:/opt/timerpi/
# Pages and browser files (templates/, public/) are embedded in the binary
# (STATUS C9). Only the ftl-themes tree is read from disk:
ssh root@<pi> mkdir -p /opt/timerpi/third_party
scp -r third_party/ftl-themes root@<pi>:/opt/timerpi/third_party/

ssh root@<pi> 'cd /opt/timerpi && ./scripts/install-pi.sh --hostname stage-left'
```

The installer is idempotent (safe to re-run):

1. creates `/opt/timerpi` (app home) and `/var/lib/timerpi` (SQLite DB +
   `config.json`, which the app creates on first boot);
2. installs both binaries, the two systemd units and the splash PNG;
3. writes `/etc/tmpfiles.d/timerpi.conf` so `/run/timerpi` exists from
   boot (the splash polls for the ready file in it);
4. pins the HDMI mode in the kernel command line — appends
   `video=HDMI-A-1:1920x1080@50e` to `/boot/firmware/cmdline.txt`
   (fallback `/boot/cmdline.txt`). A *differing* `video=HDMI-A-1:…` token
   is replaced; when our token is already there nothing changes. The
   first modification backs the file up once, as
   `cmdline.txt.timerpi.bak` (never overwritten by later runs);
5. `--hostname NAME` sets the OS hostname (`hostnamectl`, or plain
   `/etc/hostname` + live `hostname`), updates the `/etc/hosts`
   `127.0.1.1` line and (re)enables `avahi-daemon` when present — avahi
   then publishes `<name>.local`; the app publishes
   `_timerpi._tcp` itself via zeroconf, so the two mechanisms mesh;
6. enables both units (`timerpi-splash` runs at sysinit, `timerpi` at
   multi-user), reloads systemd and starts the main service;
7. prints the next steps. Run with `--dry-run` to see every action
   without touching the machine.

## 3. Boot → verify checklist

| # | check | expected |
|---|---|---|
| 1 | boot completes, HDMI shows | TimerPi logo (splash), mesas-to-black when the app is ready |
| 2 | `systemctl status timerpi-splash` | active (exited); `journalctl -u timerpi-splash` shows the fade |
| 3 | `systemctl status timerpi` | active (running), restarting itself only on failure (`Restart=always`) |
| 4 | `curl -s http://localhost/health` | `{"ok":true,...}` — serving on :80 |
| 5 | `test -e /run/timerpi/ready` | exists while the app serves (splash handshake) |
| 6 | from **another** LAN host: `avahi-browse -rt _timerpi._tcp` | an entry with `TXT` `host=<hostname>`, `role=…`, `ver=…`, `epoch=…` and `port=80` |
| 7 | another host: `curl http://<pi-hostname>.local/health` | works (avahi + the app's mDNS) |
| 8 | `cat /sys/class/drm/card?-HDMI-A-1/modes \| grep '\*' \|\| cat /sys/class/drm/*/modes` after boot | `1920x1080` @ 50 Hz active (`drm_info` shows the active mode when installed) |
| 9 | reboot the Pi | logo again; app takes over (no stuck console text) |
|10 | `systemctl stop timerpi && journalctl -u timerpi -f` → start | auto-restart, no orphaned sockets, WS clients reconnect |

The mDNS spot-check can also look for the name-conflict watcher: two Pis
with the same hostname produce a `NAME CONFLICT` line in
`journalctl -u timerpi` — rename one (`--hostname`).

## 4. Reverse proxy / custom domains

The origin guard (`routes.OriginGuard`, open mode by default) answers to
ANY `Host` header, so a reverse proxy or custom domain works untouched.
Keep it that way unless the network is untrusted; to harden, list the
proxy domain in that machine's `/var/lib/timerpi/config.json`:

```json
{ "http_port": 80, "allowed_hosts": ["timer.example.com"] }
```

proxy requirements: pass `Host` verbatim, support WS upgrades on `/ws`
(any host/origin — `routes.SameOriginRequest` is applied by the
upgrader), and `proxy_read_timeout` at least ~120 s for the WS
keepalive cadence.

## 5. Troubleshooting

| symptom | where to look | fix |
|---|---|---|
| splash never appears | `journalctl -u timerpi-splash`; `ls -l /dev/fb0` | framebuffer missing → add `dtoverlay=vc4-fkms-v3d` (or enable KMS via `raspi-config`); splash exits 0 gracefully otherwise, boot continues |
| splash stays forever | `test -e /run/timerpi/ready` | app didn't become healthy → `journalctl -u timerpi`; splash auto-times-out after 45 s and releases the fb |
| build/install writes fail with `arm` mismatch | `file bin/timerpi-arm64` | binary wasn't cross-compiled for arm64 → `make build-arm64` on the build machine and restage |
| `avahi-browse` shows nothing | firewall (UDP 5353); `systemctl status avahi-daemon` | allow mDNS; `--hostname` path enables avahi automatically when installed |
| port 80 in use / bind error | `journalctl -u timerpi` | stop the conflicting service, or set `http_port` in `/var/lib/timerpi/config.json` and restart |
| wrong HDMI mode after reboot | `grep video= /boot/firmware/cmdline.txt` | must contain `video=HDMI-A-1:1920x1080@50e`; restore-backup `cmdline.txt.timerpi.bak` if a firmware update rewrote the file |
| DB corrupt after power cut | `ls /var/lib/timerpi` | SQLite recovery happens on open; `rm timerpi.db` to reset state (config survives) |
| active mode not 50 Hz ( Mesa) | `cat /sys/class/drm/card0-HDMI-A-1/status` | check the cable/HDMI port (HDMI-1 vs micro-HDMI on the Pi 4); the kernel mode must be `1920x1080@50` |

## 6. Boot handshake contract (closed)

* **ready-file producer**: `main.go` writes `/run/timerpi/ready` (0644,
  empty) only AFTER the TCP listener is live (a failed bind never signals
  ready), and removes it on graceful shutdown (SIGTERM path included).
  Failure to write is non-fatal — the splash falls back to its own 45 s
  timeout. `/run` is ephemeral tmpfs; nothing here is configured for
  persistence.
* `scripts/install-pi.sh` and the units are aligned with `config.LoadConfig`
  (env-override contract, no CLI flags). If a future main gains a flag,
  keep the unit's plain `ExecStart` (config lives in
  `/var/lib/timerpi/config.json`).
