# OPS — TimerPi appliance runbook

Operating, upgrading, and backing up the appliance safely and boringly.
Reads like a checklist on purpose: every command below is copy-paste safe
against a **live show** — the ones that touch the service say so.
Equivalent targets exist in the Makefile (`make update backup restore
healthcheck`).

Environments this runbook covers:

| | dev container | Pi appliance (the real target) |
|---|---|---|
| data dir | `/var/lib/timerpi` when unit installed, else `./data` via `TIMERPI_DATA_DIR` | `/var/lib/timerpi` (root-owned) |
| service | `systemctl` unit `timerpi` (same unit file as the Pi) | `timerpi.service` + `timerpi-splash.service` |
| builds | x86 `make build`; `build-arm64` only if the cross toolchain is present | runs the arm64 binary, cross-built elsewhere |
| timers (`systemd/` dir) | NOT enabled — scripts run by hand | see §6 |

---

## 1. Daily operations

```sh
make healthcheck         # curl /health, record state, exit non-zero on fail
make backup              # hot SQLite backup; service keeps serving
make update              # vet+test+build (both arches) → stage → restart → health → rollback-on-fail
```

Order matters for boringness: **health → backup → update**. Backup before
update means every update carries a restore point that predates it.

### What each does in one breath

* `healthcheck` — GET `http://localhost:<port>/health`; success = HTTP 200
  **and** `"ok":true`. Writes `<ts> state=OK|FAIL detail=…` to
  `/var/lib/timerpi/health.last`; on FAIL also creates (on OK removes)
  `/var/lib/timerpi/health.failed`.
* `backup` — snapshot of the live db via `sqlite3 .backup` (or python3
  stdlib fallback; a plain-cp best-effort path exists and announces
  itself). Gz written to `/var/lib/timerpi/backups/<ts>.db.gz` with the
  `config.json` sidecar `<ts>.config.json`; dir 0750 / files 0640;
  keeps 14 newest. **Never stops the service.**
* `update` — the only script that restarts the service (default). See §2.

## 2. Update (`scripts/update.sh`)

```sh
make update                       # full: vet + tests + both builds + restart + health + rollback
scripts/update.sh --no-restart    # stage only; apply manually at the break
scripts/update.sh --skip-tests    # NEVER recommend this for the Pi
```

Steps in exact order (script prints all of them):

1. `go vet ./...` then `go test ./...` — abort → nothing changed.
2. `make build`, then `make build-arm64` when `aarch64-linux-gnu-gcc` is
   installed (keeps the Pi artifact in lockstep).
3. Stage `bin/timerpi.new`; compute `vstamp` = first 16 hex of its
   sha256.
4. **Already latest?** If the staged binary is byte-identical to
   `bin/timerpi`, refresh the stamp, exit 0 (idempotent re-runs are free).
5. Stamp bookkeeping: `/var/lib/timerpi/update.stamp`
   (`vstamp=… build=… updated=…`, EPOCH-style) and the systemd drop-in
   `29-timerpi-build-stamp.conf` under
   `/etc/systemd/system/timerpi.service.d/` (see §3).
6. **Restart gate.** Default: restart. Veto paths:
   * `--no-restart` — stage only; `bin/timerpi.new` kept; apply with
     `cp -f bin/timerpi.new bin/timerpi && systemctl restart timerpi`.
   * Marker veto: if `/var/lib/timerpi/.update-restart-or-manual` exists
     and contains `manual` (or `skip`), the restart is refused and the
     staged binary stays parked — the mid-show "update now, restart at
     the break" flow. Clear the marker (or delete the file) and re-run.
7. Atomic swap: old binary → `bin/timerpi.bak`; staged → `bin/timerpi`
   (plain `mv` within the same fs is the atomic step).
8. `systemctl restart timerpi`, then poll `/health` for **30 s**
   (`"ok":true` required).
9. On failure: restore `bin/timerpi.bak`, restart again, re-verify,
   exit 1. The old build is never left behind a broken new one.

WS clients reconnect on their own after the restart (~2 s out from
`RestartSec=2`); a dashboard in mid-show shows a brief spinner at worst.

## 3. How versions surface

* `curl -s http://localhost/health` → `{"ok":true,"version":"2.0","uptime":…,"sessions":{"connected":N},…}`
  (live clients; rises/falls with WS connections, that's normal).
* `cat /var/lib/timerpi/update.stamp` → `vstamp=… build=… updated=…` —
  the exact sha256-short of the serving binary.
* `journalctl -u timerpi | head -3` →
  `timerpi: build stamp vstamp=… build=…`, printed by the build-stamp
  drop-in's `ExecStartPre` (no daemon restart needed to *read* it; a
  restart to *refresh* it).
* `systemctl show -p Environment timerpi` → the same stamp as
  `TIMERPI_BUILD_STAMP`.

`/health` reports the product version (`"2.0"`, a constant in
`routes/setup.go`). The per-build identity (which exact binary) lives
deploy-side in `update.stamp`, as above.

## 4. Backup detail

* Preferred path needs `sqlite3`; the Pi image ideally ships it, else
  `apt install sqlite3` (runbook: one-time). python3 fallback is
  automatic and equally safe; the plain-cp path prints a big warning and
  tags its output `*.note` — treat it as unverified until a real tool is
  installed.
* Every snapshot is gated on the SQLite header (`SQLite format 3`).
* `restore.sh` re-runs `PRAGMA integrity_check` on any candidate backup
  before stopping the service — a failed integrity check aborts WITHOUT
  touching anything.
* File layout per snapshot: `<ts>.db.gz` + `<ts>.config.json` (config is
  always included; restore of db does **not** overwrite config by
  default — pass `--with-config` for both).

## 5. Restore (`scripts/restore.sh`)

```sh
# dry/sandbox (never touches the service):
cp -r /var/lib/timerpi /tmp/timerpi-sandbox
scripts/restore.sh /var/lib/timerpi/backups/<ts>.db.gz \
    --data-dir /tmp/timerpi-sandbox --no-service --with-config

# real (will stop/start the service):
scripts/restore.sh /var/lib/timerpi/backups/<ts>.db.gz --force
```

The script **refuses** the live data dir without `--force`. Real-mode
sequence: integrity gate → stop service → rollback copy
(`timerpi.db.pre-restore.<ts>`) → swap → start → 30 s `/health` watch →
roll back and re-verify on failure. Deterministic, same steps every time.

## 6. systemd timer pilots (`systemd/` dir in the repo — NOT repo root)

Three pairs, installed and enabled **only on the Pi**:

```sh
cp systemd/timerpi-backup.* systemd/timerpi-healthcheck.* /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now timerpi-backup.timer        # 03:15 + 20 min jitter, Persistent=true
systemctl enable --now timerpi-healthcheck.timer   # every 5 min
```

* `timerpi-healthcheck.service` exit code is the signal; failure lines
  land in `journalctl -u timerpi-healthcheck.service` plus the
  `health.failed` flag file. **Failure hook (piloted):** a future pairing
  `OnFailure=timerpi-health-fail@%n.service` could notify an operator
  (mail/webhook) — deliberately not shipped yet.
* The build-stamp drop-in above is a third, non-timer piece of the same
  dir: `/etc/systemd/system/timerpi.service.d/29-timerpi-build-stamp.conf`,
  regenerated idempotently by every real update run.

## 7. Troubleshooting

| symptom | where to look | fix |
|---|---|---|
| **port taken** (`-`, bind error at boot or after update) | `journalctl -u timerpi`; `ss -ltnp | grep ':80'` | stop the neighbor (`apache2`, `nginx`, dev leftovers) or pin a port: `http_port` in `/var/lib/timerpi/config.json` then restart; `TIMERPI_HTTP_PORT` env in the unit intentionally WINS only for the unit, config edit is the appliance way |
| **WS not connecting** (display loads, then dies) | browser console → WS frame; `journalctl -u timerpi` | server in strict mode: `allowed_hosts` in `/var/lib/timerpi/config.json` does not list your host → add it or ship open mode; reverse proxy must pass `Host` verbatim and allow `/ws` upgrades |
| **mDNS absent** (`<name>.local` unreachable) | `avahi-browse -rt _timerpi._tcp`; `systemctl status avahi-daemon` | firewall UDP 5353; avahi only gets enabled by the installer's `--hostname` path — re-run `scripts/install-pi.sh --hostname …` or `hostnamectl set-hostname` |
| **splash timing out** (logo sticks 45 s, then boot continues) | `test -e /run/timerpi/ready`; `journalctl -u timerpi` | app not becoming healthy — see next row; splash auto-releases regardless, boot is not blocked |
| **update ends in FAIL + rollback** | `journalctl -u timerpi -n 50`; `bin/timerpi` vs `bin/timerpi.bak` | check `go test` output from that run; the pre-update binary is already live again; if the stamp claims new-but-rolled-back, delete `/var/lib/timerpi/update.stamp` to resync |
| **restore left a `.pre-restore.*`** | `ls /var/lib/timerpi` | that's the automatic rollback copy; keep the newest two, hand-delete the rest after sanity-checking the service |
| bin/timerpi 0-byte / wrong arch, service looks healthy | `file bin/timerpi`; note uptime | NEVER `cp` into `bin/timerpi` while it serves (a truncating cp followed by a restart → exec failure); replace via temp + `mv` — update.sh does exactly this; recovery: re-run `make update` |
| health flags at 03:1x–03:3x daily | backup window | expected: backup is hot, but disk IO spikes; a single FAIL inside the window is non-alarming unless persistent |

## Test and dev servers

Run extra TimerPi instances (dev loops, `make browser-test`) with
`TIMERPI_MESH=off`: they then never announce themselves over mDNS, so the
venue's boxes can't react to them. The browser test runner sets it.

## Passwords and sign-in

Access is per event, plus one box password for the box's own settings:

- **Supervisor password** (set when the event is created) signs the
  SuperOperator in at `/e/<event code>`. Change it on the SuperOperator
  dashboard; every other supervisor session is signed out.
- **Room password** (optional, set by the SuperOperator per room) protects
  one room's moderator view.
- **Box password** guards box settings (`/settings`: hostname, network,
  OSC, default theme). It is set the first time someone opens `/box` (or is
  sent there from `/settings`), and changed or signed out on the same page.
  Changing it signs every other box session out. Event passwords never
  unlock box settings.
- **Lost box password:** stop the service and clear it with
  `sqlite3 /var/lib/timerpi/timerpi.db "DELETE FROM settings WHERE key='box.pw_hash';"`.
  The next visitor to `/box` then sets a new one, so do this on a closed network.
- Screens (`/d/`), audience phones (`/a/`) and `/health` never sign in. A
  screen set up through capture carries a **screen key** in its address;
  only keyed screens (or a browser a moderator opened) get stage messages,
  notes and the Presenter item. A screen opened by hand with just
  `/d/<room>` shows the public timer. To open a screen by hand with full
  content, use **Screen link** on the Screens page.
- **After updating from a build without screen keys:** screens set up
  earlier have no key. Re-capture them (open `/d/` on them) or open their
  **Screen link**; until then they show the public timer.
- **Lost supervisor password:** stop the service and clear it with
  `sqlite3 /var/lib/timerpi/timerpi.db "UPDATE events SET super_hash='' WHERE code='<CODE>';"`.
  The event then admits anyone holding its code until a new password is set.

## 8. Cloud server (VENUE-CLOUD §10)

Unprivileged LXC container, Debian Trixie, x86-64.

1. Build on Trixie (or in a Trixie container): `make build-amd64 VERSION=3.0.0`.
2. On the container: `adduser --system --group timerpi`, copy `bin/timerpi-amd64`
   to `/opt/timerpi/bin/timerpi`, the ftl-themes tree to `/opt/timerpi/third_party/ftl-themes`,
   `deploy/cloud/timerpi.service` to `/etc/systemd/system/`, then
   `systemctl enable --now timerpi`. It listens on port 8080 with
   `TIMERPI_ROLE=cloud` (no mesh, mDNS, HDMI or OSC).
3. Install Caddy, put `deploy/cloud/Caddyfile` in `/etc/caddy/Caddyfile` with the
   public name, `systemctl reload caddy`. Caddy gets the certificate and passes `/ws`.
4. Set `allowed_hosts` in `/var/lib/timerpi/config.json` to the public name; restart.
5. `curl -s https://<name>/health` → `"role":"cloud"`.
6. Before the first show, run the load harness against it (`TP_LOAD=1 go test ./ws -run Load`).

## 9. Releases and boot-time updates (VENUE-CLOUD §14)

- **Release key, once:** `go run ./tools/release keygen -key ~/timerpi-release.key`.
  Keep the private key off the repo and the boxes. Commit `update/release.pub`:
  builds without it never update.
- **Make a release:** `make release VERSION=3.0.1 RELEASE_KEY=~/timerpi-release.key`
  → `bin/timerpi-arm64`, `bin/timerpi-amd64` and their `.manifest.json`.
- **Publish to boxes:** on the cloud, copy `bin/timerpi-arm64` and
  `bin/timerpi-arm64.manifest.json` to `/var/lib/timerpi/releases/arm64/timerpi`
  and `…/timerpi.manifest.json`. Set `cloud_url` (e.g. `https://timer.example.com`)
  in each box's `config.json` (or `TIMERPI_CLOUD_URL`).
- **What a box does:** for 5 minutes after the machine boots it asks the cloud and
  every TimerPi on the mesh for a newer signed build, installs the newest, keeps
  the old one as `bin/timerpi.prev` and restarts. Then it doesn't look again until
  the next reboot. It never swaps while a timer runs.
- **Rollback:** a new build that fails to start twice is replaced by `.prev`
  on the third start (journal: "restored the previous one"). A build that has
  served for a minute is kept.
- A box installed with a signed build (manifest beside the binary) serves it to
  other boxes, so a venue without internet updates from one box.
- **Missed the boot window?** Open the box's `/settings`: the SOFTWARE panel
  offers **UPDATE to x.y.z** when a newer build is reachable. It installs and
  restarts at once (not while a timer runs).

## 10. Venue boxes and the cloud (VENUE-CLOUD §3–§8a)

- **Install a box** with the cloud's address: `install-pi.sh --cloud https://timer.example.com`
  (sets `cloud_url`). Without it the box works offline only (no audience).
- **The box's screen:** the kiosk opens `http://localhost/d/box`: the box's name and a
  6-digit code until it is paired, then its screen.
- **Pair a box:** on the event page (cloud, or `timerpi.local` at the venue), **Pair a box**:
  type the code, pick the room, display type, layout and mounting. The first box at a venue
  copies the event from the cloud and becomes the main box; the others follow it.
- **Event end:** set **Event ends** in Event settings. Four hours later (never while a timer
  runs) screens and boxes are released; the main box sends the final copy to the cloud.
- **Link status:** the cloud's event page says when the event is running at the venue. With
  the link down, phones see "Audience paused" and remote changes pause; the venue carries on.
- **Servers with many events** (the cloud, dev, tests) run with `TIMERPI_ROLE=cloud`; a box
  belongs to one event at a time and refuses to create a second.
- **Lost track of a box:** its pairing is the `box.pairing` setting in its database; clearing
  it (`DELETE FROM settings WHERE key='box.pairing'`, then restart) makes it show a code again.
