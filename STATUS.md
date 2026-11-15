# TimerPi — Status & Roadmap

> **The distance between [docs/PRODUCT.md](docs/PRODUCT.md) (target) and the
> code (today).** Every row cites a PRODUCT requirement ID. Update this file in
> the same commit that changes a status. This file lists what is **open**;
> finished work is one line in §5 and its detail is in `git log`.
>
> Key: ✅ meets spec · 🟡 works with caveats, or unproven on hardware ·
> ⚠️ partial or buggy · ❌ missing
>
> State 2026-11-12: `go build`, `go vet`, `go test ./...` and the 55 browser
> tests are green; `.github/workflows/ci.yml` runs all four on every push.

---

## 1. Reference event, surface by surface (PRODUCT §5)

| # | Surface | Status | Gap |
|---|---|---|---|
| 1 | Event walk-in | ✅ | `event` / `event-portrait` templates: "All rooms now" (live now/next per room), event schedule (one column per room), event map, clock. Fed by `/api/shows/:room/walkin` (5 s). The old `/zone/` page is retired (C10) |
| 2/3 | Room walk-in | ✅ | `room` / `room-portrait` templates; rotation per screen |
| 4/5 | Room audience display | ✅ | `main` template: one large Audience-item tile (poll/quiz bars, Q&A wall + spotlight, word cloud, ideas) + join QR. Text scaling for big screens comes in the UI pass |
| 6/7 | Room presenter display | ✅ | `dsm` template; its Audience-item tile follows the Presenter target |
| 8/9 | Room moderator | 🟡 | ✅ Event code → pick room → optional room password. Room-isolated (tested). Dashboard is complete for timing/audience. A dedicated tablet layout for the Run tab is open (**N11**) |
| 10 | SuperOperator | ✅ | ✅ `/e/<code>/admin`: live room cards (state, now/next, time left, screens) with GO/Pause/Blackout, blackout all, rooms admin (add, inline rename, reorder, delete, passwords), event settings (name, theme, map, password, delete). Moderates every room; event-wide screens view |
| 11 | Audience devices | ✅ | Rebuilt phone page: vote/change vote, results with your answer and the quiz verdict, ask/ideas with "waiting for review", upvotes, spotlight, word cloud. WS lane with REST fallback |

## 2. Requirements matrix

| Req | Status | Note |
|---|---|---|
| E1–E3 events, join flow, rooms admin | ✅ | Tested in `routes/events_test.go` |
| E4 event export/import | ✅ | Event Technician page "Event file": Export event (`GET /api/events/:c/export`, no hashes or screen keys) and Import an event file (`POST /api/events/import`, a new event with the importer's password). Tests `TestEventFileRoundTrip`, browser `event-file-leave` |
| T1–T5 timing | ✅ | Mature |
| T6 import | ✅ | |
| T7 blackout room / event | ✅ | Room, and event-wide from the SuperOperator dashboard |
| S1 capture | ✅ | Capture asks name, display type (Audience/Walk-in/Presenter), layout (templates for that type), mounting (rotation) and theme; only into rooms you moderate |
| S2 layout editor | ✅ | Editor shows the screen's real canvas (rows × 12, landscape or portrait) in its theme |
| S3 templates per display type | ✅ | 13 templates in a catalog grouped by type, incl. portrait walk-ins (`boards.Templates`) |
| S4 remote screen management | ✅ | Per room and event-wide screens view (N3) |
| S5 offline resilience | 🟡 | Code + tests. Two-machine drill pending (H4) |
| S6 no chrome on screens | ✅ | B5 fixed (editor-only chrome), U22 (no header on boards) |
| S7 rotation | ✅ | Per screen 0/90/180/270, CSS rotation, server-rendered + pushed live (`screen-look` frame) |
| S8 live event walk-in | ✅ | |
| S9 per-screen theme + layout, inline rename | ✅ | Screens page: type, layout, theme, mounting apply instantly; double-click/tap renames |
| A1 kinds | ✅ | Poll, quiz, Q&A, word cloud, ideas (survey removed) |
| A2 room-scoped | ✅ | |
| A3 hidden until pushed | ✅ | Absent from the payload |
| A4 two push targets | ✅ | Show to Audience / Show to Presenter (`ShowTo`) |
| A5 results bars, voting closes | ✅ | |
| A6 hide with animation | ✅ | |
| A7 Q&A wall / spotlight / dismiss / answered | ✅ | Plus auto-approve per item |
| A8 per-item moderation | ✅ | Approve / dismiss per entry; word approval covers identical words |
| A9 QR join, one vote per device | ✅ | |
| A10 1,000 per room / 200 on a Pi | 🟡 | Dev container: 1,000 phones, p95 12 ms, 1,000/1,000 join storm (`TP_LOAD=1 go test ./ws -run TestLoad`); poll broadcasts coalesced (≤4/s per room) so vote bursts do not fan out per vote. Not yet on a Pi or real Wi-Fi (H5) |
| L1 ftl-themes, default `blue-future` | ✅ | ftl-themes is a submodule pinned to an upstream commit |
| L2–L3 animation everywhere, operator-set | ✅ | N9 |
| L4 reduced motion on phones only | ✅ | |
| L5 viewport sizing | ✅ | Layouts are canvases that fill the screen; tile text scales with the tile (container query units), so 720p/4K/portrait all work |
| L6 device-class UX (tablet moderator) | 🟡 | N11 |
| H1–H3 appliance, offline LAN, mesh | 🟡 | Built; hardware drills pending (H1–H4, D1) |
| H4 CuTePi OSC | ✅ | |
| H5 any browser as screen | ✅ | |
| M2 day-ready model | 🟡 | `cues.day` and `events.days` exist (always 1). Queries are not day-scoped yet (N10) |
| htmx 4 vendored | ✅ | N8 |

## 3. Open work

Bugs from the 2026-10-06 code review are tracked in [docs/BUGLOG.md](docs/BUGLOG.md) (one open: RS37, the JS half).

| ID | Open | Spec | Notes |
|---|---|---|---|
| **N10** | Day-scope the queries. `cues.day` (default 1) and `events.days` exist; nothing reads them | M2 | Waits for the multi-day UI (out of scope for v2) |
| **N11** | A dedicated tablet layout for the Run tab | L6 | 1024×768 shows the band, transport, On air column and 4+ rows (U49, U50, U53); decide what more a tablet needs |
| **N13** | Pairing code on the DRM splash | H7 | Waits for the kiosk decision (N18). The code already shows on `/d/box` |
| **N17** | Protocol-version check on the cloud uplink | H6 | Boxes already ignore mesh peers on another major |
| **N18** | Kiosk mode in the box installer | H3 | Owner decision pending. The installer and mesh are built, not tried on hardware |
| **N19** | Warn when two DHCP servers are on the venue network | H3 | |
| **N20** | Run the load harness on the real cloud container | H6 | `deploy/cloud/` and OPS §8 are ready |
| **U26, U27** | Import: match columns by header | T6 | Deferred by the owner (2026-10-06) |
| **U51** | "Done" rows are position-based: a cue dragged above the running one is struck through | T1 | The snapshot keeps no "has run" record (U50's known limit) |
| **RS37** | Split `public/src/timerpi.js` (2,193 lines) and `public/src/board.js` (1,860) | | `db.go` is split |
| **H5b** | A load client that drives a box from another machine | A10 | Today only real phones can load a box over Wi-Fi (HW-DRILLS §4b) |

## 4. Hardware / field proof

Everything in VENUE-CLOUD §9–§14 (cloud↔venue sync, pairing, mesh installer, boot-time updates) is built and tested in software only.

| ID | Drill | Runbook |
|---|---|---|
| H1 | Two-Pi LAN mesh | docs/HW-DRILLS.md §1 |
| H2 | DRM renderer 1080p50 | docs/HW-DRILLS.md §2 |
| H3 | Pi deploy acceptance | docs/PI-DEPLOY.md |
| H4 | Two-machine browser P2P offline drill (also proves S5) | docs/HW-DRILLS.md §3 |
| H5 | Audience load on a Pi 4/5: 200 phones (floor), then 1,000; then real phones over the venue Wi-Fi | docs/HW-DRILLS.md §4 |
| H6 | Portrait walk-in on a real rotated TV (kiosk Pi + a smart-TV browser) | docs/HW-DRILLS.md §5 |
| D1 | Radio drills: built-in radio limits, channel 13 next to a busy venue Wi-Fi, 5 GHz IBSS on channel 36, venue LAN filter, MTU 1532 | docs/VENUE-CLOUD.md §11.3 |

## 5. Done

One line per ID, so citations in code and commits still resolve. Detail: `git log --grep=<ID>`.

**Bugs.** B1 no appliance password · B2 quiz answer hidden until results · B3 survey removed · B4 ideas are entries of their item · B5 edit chrome only in the editor · B6 unknown roles refused, only `controls` sends commands · B7 zone page retired · B8 stale binary against new scripts (root cause C9) · B9 GO on a held cue's row restarts it · B10 stale Show message actions keep the current stage message · B11 repeated reorder IDs preserve dense cue positions.

**Event model.** N1 events, rooms, passwords, sessions · N2 home page and event lobby · N3 Event Technician dashboard, event-wide screens · N4 screen type and rotation, template catalog · N5 two push targets · N6 Q&A entries, upvotes, spotlight, auto-approve · N7 event walk-in tiles and feed · N8 htmx 4.0.1 vendored · N9 layout and per-tile animation.

**Venue and cloud** (software only, see §4). N12 event end releases screens · N13 pairing by 6-digit code, signed mDNS · N14 `timerpi.local` follows the leading box, clock hint · N15 cloud↔venue event copy · N16 phones stay on the cloud · N17 `TIMERPI_ROLE`, protocol version in `/health` and mDNS · N18 box installer and venue mesh · N19 network page radios and peers · N21 boot-time updates; release key embedded 2026-11-12.

**Screens and displays.** U1 handheld screens follow the device, not Mounted · U2 no fullscreen tap or hint · U3 schedule line format · U4 Current & next tile · U5 no codes on screens · U6 "Room: <name>" when there are several rooms · U7 list tiles rebuild only on change · DR1, DR2 display reviews (content fit, ftl components, empty tiles give their room away).

**Layouts and the Screens page.** U8 layout editor modal · U9 layout list on each screen card · U10 built-ins listed, never edited · U11 editing a built-in makes a named event copy · U12, U44 Forget · R1-setup, R2-ops setup-page and phone reviews.

**Run tab.** U13 typing never reaches the shortcuts · U14 breaks carry a location · U15 reorders keep the running cue · U16 duration and alert units · U30–U41, U43, U45 room page tidy (one inline table, transport above it, swatches, row menu) · U31 no Reset · U42 no auto-continue, auto-start or hold after · U47 Event Technician naming, footer, FLASH · U48 owner round (rooms optional, sign out, one Send) · U49, U50 Run tab reviews (tablet and phone fit, h:mm, undo on delete, cue editor). · U53 one live workspace: now band and transport (WRAP UP included) on every tab, On air column beside Run and Audience, Import and join QR drawers, rows Go · edit · ⋯ (2026-11-12) · U54 room switcher in the bar.

**Audience.** U17 ftl components · U18 On air strip over a library · U46 tiles painted in place · UA1 on air is separate from the voting phase.

**Home, event pages, roles.** U19–U24 home and event page wording, Leave event · U25 moderators get Run and Audience only · U28 theme picker for the Event Technician only · U29 no Setup tab · U52 Event Technician page: Live rooms table and Setup tab, settings save on change (password keeps Save).

**Cleanup.** C1 `TIMERPI_HTTP_PORT` · C2 ftl-themes submodule · C3 dead code · C4 content-hash asset URLs · C5 templates from the server · C6–C8 doc references, `docs/UI-CONTRACT.md` · C9 UI embedded in the binary · C10 zones retired.

**Engine.** The browser's offline engine matches the server, held to it by `TestEngineParityWithJS` (2026-11-12).

## 6. Suggested order

1. **Hardware**: H3 (deploy), H1, H2, then H5 and H6; D1 with the mesh. Nothing in §4 has run on a Pi.
2. **N20**: the load harness on the cloud container, before the first show.
3. **Owner decision**: N18 kiosk mode (unblocks N13).
4. **RS37**: split the two large scripts, behind the browser tests.
5. N11, N17, N19, U51, H5b as they come up. N10 waits for the multi-day UI.

## 7. Open questions

None open. VENUE-CLOUD §7 records the sync rules (2026-10-06).
