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

## Passwords and sign-in

There is no appliance password. Access is per event:

- **Supervisor password** (set when the event is created) signs the
  SuperOperator in at `/e/<event code>`. Change it on the SuperOperator
  dashboard; every other supervisor session is signed out.
- **Room password** (optional, set by the SuperOperator per room) protects
  one room's moderator view.
- **Box settings** (`/settings`: hostname, network, OSC, default theme)
  need a SuperOperator session of any protected event on the box. They stay
  open while no event has a supervisor password.
- Screens (`/d/`), audience phones (`/a/`) and `/health` never sign in.
- **Lost supervisor password:** stop the service and clear it with
  `sqlite3 /var/lib/timerpi/timerpi.db "UPDATE events SET super_hash='' WHERE code='<CODE>';"`.
  The event then admits anyone holding its code until a new password is set.
