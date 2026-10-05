# HW-DRILLS — bench runbooks for the hardware-blocked ladder items

One-shot procedures for C1 (two-Pi LAN mesh), C3 (DRM renderer) and the
C2 P2P re-run. Each drill lists prerequisites, exact commands, expected
output and timing budgets — run top to bottom, paste the result log
(§5) back when done so the ladder rows can be closed.

Code facts these drills rely on (all verified in-tree 2026-10-05):

- Device mesh tunables (`mesh/device.go`): poll 5 s, browse window 3 s,
  claim grace 10 s, takeover after 8 s silence, peers-fresh window 30 s,
  harvest timeout 3 s.
- Seniority: `claimedEpoch` (ms of FIRST claim, persisted in SQLite
  `mesh_state`) never re-stamps — smaller epoch wins, across restarts
  and takeovers. Ties break by hostname, then port.
- Observability: `GET /api/network` → `{role, epoch, peers[]}`,
  `POST /api/network/role {"force":"auto|primary|member"}`,
  `POST /api/network/hostname {"name":…}`, `GET /frag/network` live panel.
- Browser mesh handle: `window.__tpmesh` (`peerId`, `masterId`,
  `isMaster()`, `openPeerIds()`, per-peer `connections.get(id).dc/pc`
  states). Master = earliest `joinedAt`, peerId tiebreak (`mesh.js`).

## 0. Bench prerequisites (all drills)

- [ ] Two Raspberry Pi 4/5, 64-bit Pi OS bookworm, each flashed per
      `docs/PI-DEPLOY.md` §§1–2 (distinct `--hostname`, e.g.
      `stage-left` / `stage-right`), on the SAME LAN as the bench machine.
- [ ] HDMI panel on at least one Pi (C3 needs it; C1/C2 don't).
- [ ] Bench machine with `avahi-browse` (or Bonjour), `curl`, and a
      Chromium/Firefox with devtools (C2).
- [ ] Both Pis reachable: `curl http://<name>.local/health` → `{"ok":true,…}`.

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
12. B's dashboard serves the show (open `http://stage-right.local/`).

### 1e. Reunion: seniority returns authority to A, no split-brain

13. `ssh root@stage-left 'systemctl start timerpi'`; wait 20 s.
14. A → `"role":"primary"`, epoch == E_A (unchanged — claims never
    re-stamp). B → `"role":"display"` (split-brain heal: B saw A's
    senior claim and yielded).
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

## 4. What "done" means per ladder row

| row | close when |
|---|---|
| C1 | §1a–1g all PASS; result log pasted; no dual-primary outside overrides |
| C3 | §2 steps 4+6 PASS at 1920x1080@50; framebuffer alternating confirmed |
| C2 | §3 steps 2+4+5 PASS with `connected`/`open` channels on a real machine |

## 5. Result log template (paste back into the session)

```
DRILL: C1|C3|C2-P2P   DATE:           BENCH HOST:
PI(s): <hostname> <model> <os> / panel: <model|none>
1a primary=A epoch=E_A: PASS|FAIL (output:)
1b member join + avahi both: PASS|FAIL
1c fast restart no flap: PASS|FAIL
1d takeover harvest line: PASS|FAIL (blind? y/n)
1e reunion single primary: PASS|FAIL (epochs A= B= )
1f name conflict + clear: PASS|FAIL
C3 TestHW RUN (not skip): PASS|FAIL; mode 1080p50: y/n; live tick: y/n
C2 channels connected+open: y/n; dark replication peer-follows: y/n; merge toast: y/n
NOTES:
```
