# HW-DRILLS — bench runbooks for the hardware-blocked ladder items

One-shot procedures for C1 (two-Pi LAN mesh), C3 (DRM renderer), the
C2 P2P re-run, H5 (audience load) and H6 (portrait walk-in). Each drill lists prerequisites, exact commands, expected
output and timing budgets — run top to bottom, paste the result log
(§7) back when done so the ladder rows can be closed.

Current device-mesh tunables (latest bench evidence is in §7):

- Device mesh tunables (`mesh/device.go`): poll 5 s, browse window 3 s,
  claim grace 10 s, takeover after 8 s silence, peers-fresh window 30 s,
  harvest timeout 3 s.
- Seniority: `claimedEpoch` (ms of FIRST claim, persisted in SQLite
  `mesh_state`) never re-stamps — smaller epoch wins, across restarts
  and takeovers. Ties break by hostname, then port. A restarting box follows an existing healthy primary regardless of its stored epoch; seniority resolves competing active primaries.
- Observability: `GET /api/network` → `{role, epoch, peers[]}`,
  `POST /api/network/role {"force":"auto|primary|member"}`,
  `POST /api/network/hostname {"name":…}`, `GET /frag/network` live panel.
- Browser mesh handle: `window.__tpmesh` (`peerId`, `masterId`,
  `isMaster()`, `openPeerIds()`, per-peer `connections.get(id).dc/pc`
  states). Master = earliest `joinedAt`, peerId tiebreak (`mesh.js`).

## 0. Bench prerequisites (all drills)

- [ ] Two Raspberry Pi 4/5, 64-bit Pi OS Lite Trixie, each flashed per
      `docs/PI-DEPLOY.md` §§1–2 (distinct `--hostname`, e.g.
      `stage-left` / `stage-right`), on the SAME LAN as the bench machine.
- [ ] HDMI panel on at least one Pi (C3 needs it; C1/C2 don't).
- [ ] Bench machine with `avahi-browse` (or Bonjour), `curl`, and a
      Chromium/Firefox with devtools (C2).
- [ ] Both Pis reachable: `curl http://<name>.local/health` → `{"ok":true,…}`.

### Shared Pis (CuTePi and TimerPi)

Before changing shared hardware, agree all of the following with the CuTePi
playback owner:

- Two named Pis and an explicit disruptive test slot. A reachable host is
  not an allocated spare. Read-only inventory does not authorize conversion.
- One network configuration writer (TimerPi); CuTePi retains playback,
  deployment and DRM ownership. Do not start both mesh controllers.
- A shared radio/addressing profile. CuTePi's current unbridged mesh and
  TimerPi's bridged mesh differ; resolve that decision before applying either
  installer. PRODUCT's existing radio decisions remain in force until changed
  by the owner.
- A wired management path that remains reachable, a saved network/service
  configuration, and a tested rollback procedure before changing radios,
  bridges, routes or network managers.
- Separate transport proof from application sync: first establish mesh
  reachability, then run signed discovery, event-copy and takeover checks.
  A BATMAN-adv link alone does not prove event synchronization or recovery.

Keep the shared device inventory with the CuTePi allocation record, rather
than duplicating host addresses and playback service state here. Never run
the TimerPi installer, install tools, restart playback, open DRM devices or
reconfigure a shared Pi outside its agreed slot.

## 1. Drill C1 — two-Pi LAN mesh (closes ladder C1)

Budget ~10 min. Timings below include the poll cadence — do not
shorten the waits, late joins are the #1 false failure.

### 1a. First claim (Pi A boots alone)

1. `ssh root@stage-left 'systemctl start timerpi'` (or reboot the Pi).
2. Wait 15 s (10 s grace + one 5 s poll).
3. `curl -s http://stage-left.local/api/network` → `"role":"primary"`.
   Record `epoch` as E_A.

### 1b. Member join (Pi B boots second)

4. `ssh root@stage-right 'systemctl start timerpi'`; wait 15 s.
5. `curl -s http://stage-right.local/api/network` → `"role":"display"`,
   `peers[]` contains `stage-left` with role `primary`.
6. On either Pi: `avahi-browse -rt _timerpi._tcp` → TWO entries, TXT
   `host=`, `role=`, `ver=`, `epoch=`, `port=80` on each.

### 1c. Fast restart does NOT flap (downtime < 8 s)

7. `ssh root@stage-left 'systemctl restart timerpi'`; wait 15 s.
8. A is still `primary` with epoch == E_A; B stayed `display` throughout
   (spot-check B twice, 10 s apart). No takeover journal line on B:
   `journalctl -u timerpi --since '2 min ago' | grep -i takeover`
   → empty.

### 1d. Takeover on long outage (downtime > 8 s)

9. Seed state on A: create a cue via the dashboard
   (`http://stage-left.local/c/<code>`) so the harvest has content.
10. `ssh root@stage-left 'systemctl stop timerpi'`; start a timer.
11. Within ~20 s (8 s silence + polls), B promotes:
    `curl -s http://stage-right.local/api/network` → `"role":"primary"`,
    and `journalctl -u timerpi` on B shows
    `takeover: harvested show <id> from stage-left (<n> bytes)`
    (or `promoting blind` if A died mid-harvest — still a PASS for
    authority, note it in the log).
12. B's dashboard serves the show (open `http://stage-right.local/`). Add a uniquely named cue on B while A is stopped and verify it is saved.

### 1e. Reunion: the returning box catches up, no split-brain

13. `ssh root@stage-left 'systemctl start timerpi'`; wait 20 s.
14. A → `"role":"display"`, epoch == E_A (unchanged). B stays
    `"role":"primary"`. Within the mirror interval A must contain the cue
    added on B; it must not erase that edit by reclaiming authority.
    After confirming catch-up, stop B and verify A can take over with the
    edit intact; restart B and verify one primary remains.
15. PASS requires exactly ONE primary across both `/api/network`
    outputs. If both ever claim primary outside a force override,
    that is a bug — capture both outputs + journals.

### 1f. Name conflict watcher

16. `ssh root@stage-right` → set hostname to `stage-left`
    (`POST /api/network/hostname {"name":"stage-left"}` or re-run
    `install-pi.sh --hostname stage-left`); wait 15 s.
17. `journalctl -u timerpi` on either shows `NAME CONFLICT`.
18. Rename back to `stage-right`; conflict clears within ~15 s.

### 1g. Cleanup

19. `POST /api/network/role {"force":"auto"}` on BOTH Pis (never leave
    an override: two forced primaries split-brain by design, see
    `mesh/device.go` (`decideLocked`)).

## 2. Drill C3 — DRM renderer hardware run (closes ladder C3)

Budget ~10 min. Needs the panel attached to the Pi under test and a cross-compiled ARM64 test binary on that Pi. The binary, rather than a build-container test, must run where `/dev/dri` lives. No Go installation on the Pi is required.

1. `grep video= /boot/firmware/cmdline.txt` → contains
   `video=HDMI-A-1:1920x1080@50e` (PI-DEPLOY §2 pins this).
2. `cat /sys/class/drm/card0-HDMI-A-1/status` → `connected`.
3. Free the card — the app is DRM master while running:
   `systemctl stop timerpi` (splash already exited; nothing else may
   hold `/dev/dri/card0` — a stray `drm_info` run breaks this the
   same way).
4. Cross-build on the build machine:
   `CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc GOOS=linux GOARCH=arm64 go test -c -o bin/drm-hw-arm64.test ./drm`. Stage it in the agreed test directory.
   On the released display, select its connected card and run
   `TIMERPI_HW_TEST=1 DRM_CARD=/dev/dri/cardN ./drm-hw-arm64.test -test.run "^TestHWDirectKMS$" -test.v -test.timeout 45s`.
   It must present 200 changed frames at 1920×1080@50 Hz. An opted-in open, mode or presentation failure is a failure, not a skip.
5. Record the test's actual mode, changed-frame count and elapsed time. Do not poll DRM debug state during measurement. Before returning display ownership, confirm the test exited and no test process retains a primary-card handle.
6. `systemctl start timerpi` → top-right wall clock ticks once per
   second; start any countdown and watch the seconds digit advance.
7. Expected log line on wrong-mode fallback (informational, not a
   failure): runclock logs the mode mismatch; output is clipped as-is
   (scaling is future work — `drm/README.md` risk #2).

| symptom | fix |
|---|---|
| `permission denied` opening the card | something else is master — stop timerpi first, kill stray drm_info |
| TestHW SKIPs | `/dev/dri` absent (FKMS overlay?) → `dtoverlay=vc4-fkms-v3d` / raspi-config KMS, reboot |
| mode not 50 Hz | cable/port (Pi 4 micro-HDMI), then cmdline token, then `cmdline.txt.timerpi.bak` archaeology |

## 3. Drill C2-P2P — two-tab mesh replication, real machine (closes C2)

Budget ~10 min. Do this on a REAL LAN machine — the sandbox browser
gathers ZERO ICE candidates (SDP exchanges fine, channels stay `new`),
so this drill cannot pass there by construction.

1. Open the dashboard for one show in TWO tabs (or two browsers) on
   the bench machine: `http://<pi>.local/c/<CODE>` × 2.
2. In each tab's devtools console:
   `__tpmesh.peerId`, `__tpmesh.masterId`, `__tpmesh.isMaster()`,
   `__tpmesh.openPeerIds()`.
   PASS: both tabs agree on ONE `masterId` (the earliest-joined tab),
   and each lists the other in `openPeerIds()` (data channel `open`;
   `__tpmesh.connections.get(<id>).pc.connectionState` → `connected`).
3. The unproven hop — server-independent replication: `systemctl stop
   timerpi` on the Pi. Both tabs show the degraded voice
   (`Server down · show keeps running from here`); the master tab
   stays fully operable.
4. On the master tab: add a cue, delete a cue, fire WRAP-UP, UNDO once.
   PASS: the master tab repaints instantly (offline mirror renderer)
   AND the peer tab follows each step over the mesh data channel with
   the server dark (this is the hop the sandbox could never exercise).
5. `systemctl start timerpi` → master auto-pushes; server adopts the
   merged ledger; master toasts `Merged N remote change(s)`;
   peer adopts the merged snapshot. PASS: no manual sync kick, both
   tabs converge on identical cue lists.

## 4. Drill H5 — audience load on a Pi (PRODUCT A10)

Budget ~30 min. Two stages: 4a measures the Pi itself, 4b the venue Wi-Fi.
Run 4a first; if it fails there is no point filling a room with phones.

### 4a. The harness on the Pi (CPU and memory, no Wi-Fi)

The harness (`ws/load_test.go`) starts its own server and its own fake
phones in one process, so it needs nothing installed on the Pi.

1. On the build machine:
   `CGO_ENABLED=1 CC=aarch64-linux-gnu-gcc GOOS=linux GOARCH=arm64 go test -c -o bin/ws-load.test ./ws`
2. `scp bin/ws-load.test root@<pi>:/tmp/`
3. On the Pi, with `timerpi.service` running as it would at a show:
   `ulimit -n 8192; cd /tmp && TP_LOAD=1 TP_LOAD_N=200 ./ws-load.test -test.run TestLoad -test.v`
   then again with `TP_LOAD_N=1000`.
4. PASS at each size: both tests pass (every join answered; every phone gets
   the coalesced frames; p95 latency < 100 ms). Note the p95 and the join
   storm time. A screen on the same Pi must keep ticking smoothly throughout.

### 4b. Real phones on the venue Wi-Fi

The external `tools/load` probe measures the real server from another host. It does not replace phone/AP airtime qualification. This stage uses real phones: as many as you can gather, 20 at least.

1. Put a poll on air in one room; every phone scans the room's QR.
2. Everyone votes within about 5 s, then changes their vote a few times.
3. PASS: every phone shows the results within 2 s of Show results; no phone
   shows "Reconnecting…" for more than a moment; the audience screen's bars
   match the moderator's tally; the moderator's footer ping stays under
   100 ms.
4. Note the access point model and how many phones it carried. The access
   point is usually the limit, not the Pi.

## 5. Drill H6 — portrait walk-in on a rotated TV (PRODUCT S7, S8, L5)

Budget ~15 min. Needs a TV hung portrait, once fed by a kiosk Pi and once
by its own smart-TV browser (or any PC browser on a portrait monitor).

1. Open `/d/` on the screen. It shows the READY card, readable although the
   panel is on its side.
2. On the Event Technician's Screens page capture it as **Walk-in**, layout
   Event (or Room), Mounted 90° (270° if the TV hangs the other way).
   PASS: within 2 s the screen shows the portrait version, upright, filling
   the panel with no scroll bars and no letterboxing.
3. Read it from 5 m: clock, current and next sessions and the schedule are
   legible; nothing is clipped at the panel edges.
4. Start a cue in a room. PASS: "All rooms now" follows within 5 s without a
   page reload or flicker.
5. Change Mounted to 0° and back. PASS: the screen follows live each time.
6. Power-cycle the TV (and the Pi). PASS: it comes back on the same layout
   and rotation with no one touching it.

## 6. What "done" means per ladder row

| row | close when |
|---|---|
| C1 | §1a–1g all PASS; result log pasted; no dual-primary outside overrides |
| C3 | §2 steps 4+6 PASS at 1920x1080@50; framebuffer alternating confirmed |
| C2 | §3 steps 2+4+5 PASS with `connected`/`open` channels on a real machine |
| H5 | §4a PASS at 200 and 1,000; §4b PASS with the phone count and access point noted |
| H6 | §5 steps 2–6 PASS on a kiosk Pi and on one other browser |

## 7. Current qualification evidence — 2026-11-15

Controlled shared-Pi session: 15:16:39–16:15:02 UTC. Inventory, original
workloads and verified restoration are maintained in the [shared allocation
record](https://github.com/DrEVILish/CuTePi/blob/main/docs/SHARED_PI_ALLOCATION.md).
TimerPi used disposable data, port 18080, headless services and cross-built
ARM64 binaries. No reboot or permanent installer/network conversion occurred.


Tested artifacts (local `bin/`, not a signed production release):

- `timerpi-arm64`: SHA-256 `371084a430b5aa49786759fddada3dd09d6e648b4789f1715c8bd307d9fc2408`
- `drm-hw-arm64.test`: SHA-256 `3c38226a3dc041220ce835df083debc901ed75dd93ffa0a39b4af9896d6e11e0`

### Functional and recovery checks

| Check | Result and scope |
|---|---|
| Application on both physical Pis | PASS: health, two-room creation, cues, start/pause/resume and stage messages |
| Pairing and replication | PASS: actual signed discovery, official pairing and two-room copy over Ethernet |
| Forced BATMAN path | PASS: temporary `timerpi`, GB/channel 13/20 MHz; Ethernet mDNS and peer API traffic filtered while SSH remained available. Both directions: 20/20 bat0 pings, zero loss; actual TCP, signed discovery, event edits and blackout copy passed |
| Takeover | PASS: first Ethernet takeover 10.66 s; final mesh-only takeover 13.04 s, with event retained |
| Reunion | Original run FAILED: restarting old primary erased a replacement edit. Corrected build PASS: returning box remained member and retained the edit; its later takeover retained it and accepted writes in 12.03 s |
| Member writes | PASS: physical member rejected a local mutation with HTTP 409 and primary target; the authoritative copy remained unchanged |
| Browser offline/reconnect | PASS: two independent Chromium processes connected real RTC/data channels to a Pi-hosted event. Both lost all HTTP/WS access; offline cue/GO/message reached the visible display, countdown changed 10:00→9:57, and reconnect automatically committed changes. Both clients were on CT108; this was browser-side partition, not two physical browser hosts or a Pi power failure |
| Native KMS | Initial EFAULT/no-mode failures exposed ABI bugs and an incorrect skip. Corrected real card run PASS: 1920×1080@50 Hz, 200 changed frames in 9.339 s. No framebuffer/production native-app boot acceptance is implied |
| Shared hardware restoration | PASS: network baselines restored, test filters/timers/processes/files removed. CuTePi independently restored original .73 playback at actual 1080p60 with original show/binary/unit, and exact .55 follower service/env/cwd. Reserved slot released |

Radio setup now retries legacy fixed-frequency IBSS when HT20 fails on these
built-in drivers. Both hard interfaces remained at MTU 1500; successful 1532-MTU,
5 GHz/USB, busy-venue RF and bridged-Ethernet deployment are not proven here.

### Native Pi load harness

`ws-load-arm64.test`, running on each Pi (local synthetic clients and server):

| Pi | Clients | Delivery p95 | Join storm | Result |
|---|---:|---:|---:|---|
| .73 | 200 | 6 ms | 2.16 s | PASS |
| .55 | 200 | 7 ms | 2.17 s | PASS |
| .73 | 1,000 | 18 ms | 10.633 s | PASS |
| .55 | 1,000 | 20 ms | 10.754 s | PASS |

### External deployed-server probe

`tools/load` creates/removes only its disposable fixture. Every passing run
joined all clients, accepted/counts every vote, delivered the marker to all
clients and had no disconnects. Operator commands were verified during voting.

| Path / server | Clients | Join | Fanout p95 | Operator p95 | Result |
|---|---:|---:|---:|---:|---|
| Wired / .73 | 200 | 106 ms | 83.547 ms | 5.778 ms | PASS |
| Wired / .55 | 200 | 110 ms | 81.931 ms | 3.512 ms | PASS |
| Wired / .73 | 1,000 | 514 ms | 238.727 ms | 7.702 ms | PASS |
| Wired / .55 | 1,000 | 533 ms | 244.907 ms | 151.169 ms | PASS |
| IBSS via .73 SSH relay / .55 | 200 | 404 ms | 298.050 ms | 61.061 ms | PASS |
| IBSS via .73 SSH relay / .55 | 1,000 | 1,617 ms | 329.293 ms | 1,378.198 ms | **NOT QUALIFIED**: operator gate is 250 ms |

The first 1,000-client relay attempt hit its dedicated sshd session's 1,024-FD
limit and is not a valid server-capacity measurement. Only that test session's
limit was raised to 8,192 for the tabled rerun; the SSH service configuration
was unchanged and the session was closed. The valid rerun delivered all
1,000 votes/updates but failed the operator latency gate. Do not describe the
1,000-client wireless path as qualified. This is a burst, not a sustained soak;
there were no 200/1,000 physical phones or venue AP in this session.

### 500-client / 500 ms follow-up

A separate owner-requested network-only session began at 16:52:14 UTC.
The same tested ARM64 application ran with fresh disposable, unpaired event
fixtures on port 18080; displays stayed off and .73 CuTePi playback kept
running. .55 follower was paused by CuTePi at 16:55:00.022 UTC. The path was
bench host → dedicated .73 SSH relay → bat0 → .55, temporary
`timerpi`/GB/channel 13, with Ethernet peer API/mDNS filtered. After initial
radio convergence, both directions passed 5/5 bat0 pings. The dedicated relay
session had 8,192 FDs; no daemon setting changed. Go/browser validation also
ran on the bench host during this session; this is not an isolated client-host
performance benchmark or a direct comparison with the earlier paired fixtures.

The experimental operator gate was **500 ms**, explicitly recorded by
`-clients 500 -operator-p95 500ms`. All other gates stayed unchanged, including
fanout p95 ≤1,000 ms. The default operator gate remains 250 ms.

| Path / run | Join | Vote p95 | Fanout p95 | Operator p95 | Result |
|---|---:|---:|---:|---:|---|
| Wireless 1 | 1,060 ms | 844.850 ms | 391.111 ms | 1,298.597 ms | FAIL operator |
| Wireless 2 | 1,040 ms | 2,390.954 ms | 3,285.603 ms | 2,673.078 ms | FAIL operator and fanout |
| Wireless 3 | 862 ms | 763.171 ms | 305.728 ms | 1,123.006 ms | FAIL operator |
| Same-session wired control / .55 | 262 ms | 586.798 ms | 682.017 ms | 569.997 ms | FAIL operator |

Every run joined all 500 clients, accepted and counted all 500 votes,
delivered all 500 updates, had zero disconnects/operator errors, and cleaned
its disposable room. Wireless runs had 6/5/7 operator samples; the wired run
had 8. These are short bursts, not soak tests. **500 clients at 500 ms are
not qualified by this experiment.** The wired control also failed, so these
measurements do not isolate radio congestion as the cause. Earlier 250 ms
results are unchanged. Network/controller rollback was verified and handed
back at 16:57:09 UTC. CuTePi verified the original follower restored at
16:58:59.842 UTC; TimerPi removed only its temporary files/units and released
the slot at 16:59:29 UTC. .73 playback stayed running throughout.

### Reproduce and remaining gates

- External load: `go run ./tools/load -base http://<test-pi>:18080 -clients 200`
  then `-clients 1000`. For an already-paired disposable event, add `-event`
  and `-password-file` as documented in `tools/load/README.md`.
- Real RTC check from `tools/browser-tests/`:
  `CHROMIUM_PATH=/usr/bin/chromium node hardware-offline.mjs http://<test-pi>:18080 result.json private-state.json`.
  For an already-paired box, the private state is `{ "password":"<test-password>", "73":{"event":"<code>"} }`, keyed by the last hostname component (`55` for the other bench Pi). The script signs in and creates/deletes only its own room. Without that argument it creates/deletes a disposable event. Keep this credential file private. Run browsers on the bench machine, never install a browser on a shared playback Pi.
- Repeat the full H1 fast-restart/name-conflict cases, permanent bridged
  installer/boot/update/rollback acceptance, two-host client partition,
  venue-phone/AP airtime, sustained load, 5 GHz/USB/busy-channel and physical
  portrait-TV/power-cycle checks before claiming those gates complete.
- Member mirroring remains asynchronous (30 s interval). These checks do not
  establish synchronous zero-loss failover or partition-safe consensus.
