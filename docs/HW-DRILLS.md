# HW-DRILLS — bench runbooks for the hardware-blocked ladder items

One-shot procedures for C1 (two-Pi LAN mesh), C3 (DRM renderer), the
C2 P2P re-run, H5 (audience load) and H6 (portrait walk-in). Each drill lists prerequisites, exact commands, expected
output and timing budgets — run top to bottom, paste the result log
(§7) back when done so the ladder rows can be closed.

Code facts these drills rely on (all verified in-tree 2026-10-05):

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
    `mesh/device.go` decideLocked + `docs/MESH.md` trust note).

## 2. Drill C3 — DRM renderer hardware run (closes ladder C3)

Budget ~10 min. Needs the panel attached to the Pi under test and a Go
toolchain + checkout ON that Pi (`go test` must run where `/dev/dri`
lives — cross-compiled binaries cannot run the HW test).

1. `grep video= /boot/firmware/cmdline.txt` → contains
   `video=HDMI-A-1:1920x1080@50e` (PI-DEPLOY §2 pins this).
2. `cat /sys/class/drm/card0-HDMI-A-1/status` → `connected`.
3. Free the card — the app is DRM master while running:
   `systemctl stop timerpi` (splash already exited; nothing else may
   hold `/dev/dri/card0` — a stray `drm_info` run breaks this the
   same way).
4. `cd /opt/timerpi && TIMERPI_HW_TEST=1 go test ./drm/ -run TestHW -v`
   → drives ~200 animated frames through the real card (and fb0);
   skips cleanly are a FAIL here — on this hardware it must RUN.
5. `drm_info` (if installed): mode `1920x1080` @ 50 Hz, our two dumb
   framebuffers alternating `FB_ID`s per presented frame.
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

There is no synthetic client that loads a box from another machine (STATUS
H5), so this stage uses real phones: as many as you can gather, 20 at least.

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

## 7. Result log template (paste back into the session)

```
DRILL: C1|C3|C2-P2P|H5|H6   DATE:           BENCH HOST:
PI(s): <hostname> <model> <os> / panel: <model|none>
1a primary=A epoch=E_A: PASS|FAIL (output:)
1b member join + avahi both: PASS|FAIL
1c fast restart no flap: PASS|FAIL
1d takeover harvest line: PASS|FAIL (blind? y/n)
1e reunion single primary: PASS|FAIL (epochs A= B= )
1f name conflict + clear: PASS|FAIL
C3 TestHW RUN (not skip): PASS|FAIL; mode 1080p50: y/n; live tick: y/n
C2 channels connected+open: y/n; dark replication peer-follows: y/n; merge toast: y/n
H5 4a N=200 p95=  ms storm=  s: PASS|FAIL; N=1000 p95=  ms storm=  s: PASS|FAIL
H5 4b phones=   AP=        results<2s: y/n; reconnects: y/n
H6 portrait upright+fills: y/n; live follow: y/n; survives power cycle: y/n
NOTES:
```
