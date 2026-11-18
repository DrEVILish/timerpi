# TimerPi — Status & Roadmap

> **The distance between [docs/PRODUCT.md](docs/PRODUCT.md) (target) and the
> code (today).** Every row cites a PRODUCT requirement ID. Update this file in
> the same commit that changes a status. This file lists what is **open**;
> finished work is one line in §5 and its detail is in `git log`.
>
> Key: ✅ meets spec · 🟡 works with caveats, or unproven on hardware ·
> ⚠️ partial or buggy · ❌ missing
>
> State 2026-11-15: `go build`, `go vet`, `go test ./...` and the 60 browser
> files (672 checks) are green; `.github/workflows/ci.yml` runs all four on every push.

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
| S1 capture | ✅ | Event Technician captures into any room, choosing name, display type, layout, mounting and theme |
| S2 layout editor | ✅ | Editor shows the screen's real canvas (rows × 12, landscape or portrait) in its theme |
| S3 templates per display type | ✅ | 13 templates in a catalog grouped by type, incl. portrait walk-ins (`boards.Templates`) |
| S4 remote screen management | ✅ | Per room and event-wide screens view (N3) |
| S5 offline resilience | 🟡 | Real RTC/offline/automatic merge verified against a Pi using two browser processes; two-host/power-failure drill remains (H4) |
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
| A10 1,000 per room / 200 on a Pi | 🟡 | Both physical Pis passed native and external wired 200/1,000-client probes. Wireless 200 passed; wireless 1,000 failed operator latency. Separate 500-client/500 ms exploratory follow-up failed. Latest patched .55 passed three Ethernet-only 500-client and three 1,000-client runs at the unchanged 250 ms gate; baseline reversal also passed. Ethernet is the intended client path; BATMAN is inter-Pi sync. Physical phones/AP and sustained-load qualification remain (H5); evidence: HW-DRILLS §7 |
| L1 ftl-themes, default `blue-future` | ✅ | ftl-themes is a submodule pinned to an upstream commit |
| L2–L3 animation everywhere, operator-set | ✅ | N9 |
| L4 reduced motion on phones only | ✅ | |
| L5 viewport sizing | ✅ | Layouts are canvases that fill the screen; tile text scales with the tile (container query units), so 720p/4K/portrait all work |
| L6 device-class UX (tablet moderator) | 🟡 | N11 |
| H1–H3 appliance, offline LAN, mesh | 🟡 | Real ARM64 app, signed discovery/copy/takeover and native KMS tested; permanent bridged installation/boot and remaining field checks are open (H1–H4, D1) |
| H4 CuTePi OSC | ✅ | |
| H5 any browser as screen | ✅ | |
| H8 end and release | 🟡 | N12 release waits for running and paused cues and commits with screen removal. End is editable in Setup. Selecting an end for quick timers and older events is still open ([#2](https://github.com/DrEVILish/timerpi/issues/2)) |
| M2 day-ready model | 🟡 | `cues.day` and `events.days` exist (always 1). Queries are not day-scoped yet (N10) |
| htmx 4 vendored | ✅ | N8 |

## 3. Open work

Bugs from the 2026-10-06 code review are tracked in [docs/BUGLOG.md](docs/BUGLOG.md) (one open: RS37, the JS half).

| ID | Open | Spec | Notes |
|---|---|---|---|
| **N10** | Day-scope the queries. `cues.day` (default 1) and `events.days` exist; nothing reads them | M2 | Waits for the multi-day UI (out of scope for v2) |
| **N11** | A dedicated tablet layout for the Run tab | L6 | 1024×768 shows the band, transport, On air column and 4+ rows (U49, U50, U53). [Optional component-backed UI critique](reviews/ui-critique.md) proposes improvements; no design changes approved |
| **N13** | Pairing code on the DRM splash | H7 | Waits for the kiosk decision (N18). The code already shows on `/d/box` |
| **N18** | Kiosk mode in the box installer | H3 | Owner decision pending. The installer and mesh are built, not tried on hardware |
| **N19** | Warn when two DHCP servers are on the venue network | H3 | |
| **N20** | Run the load harness on the real cloud container | H6 | `deploy/cloud/` and OPS §8 are ready |
| **U26, U27** | Import: match columns by header | T6 | Deferred by the owner (2026-10-06) |
| **U51** | "Done" rows are position-based: a cue dragged above the running one is struck through | T1 | The snapshot keeps no "has run" record (U50's known limit) |
| **RS37** | Review the remaining room/rendering responsibilities before further extraction | S2 | `db.go`, layout geometry/document state and the shared display lifecycle are separated behind behavior tests (C11–C13). Further work must concentrate real behavior, not split files by line count |

## 4. Hardware / field proof

The 2026-11-15 controlled two-Pi session ran real application, transport, recovery, load and native KMS checks. Exact scope, failures and passing measurements are in docs/HW-DRILLS.md §7. Unexecuted gates remain open; this is not a permanent appliance deployment or full wireless qualification.

| ID | Drill | Runbook |
|---|---|---|
| H1 | 🟡 Signed pairing, copy, blackout, mesh-only takeover/reunion passed; fast-restart/name-conflict cases remain. Shared session released after restoration | docs/HW-DRILLS.md §0–§1 |
| H2 | 🟡 Native KMS probe passed real 1080p50/200 changed frames; live appliance-display workflow remains unqualified | docs/HW-DRILLS.md §2 |
| H3 | 🟡 Isolated ARM64 deployment/restarts passed; permanent installer, bridged network, reboot and boot/update rollback acceptance remain | docs/PI-DEPLOY.md |
| H4 | 🟡 Real RTC/visible countdown/offline edits/automatic merge passed; two-host/power-failure drill remains (S5) | docs/HW-DRILLS.md §3 |
| H5 | 🟡 Native and wired 200/1,000-client probes passed on both Pis; wireless 200 passed, wireless 1,000 operator latency failed; latest patched .55 Ethernet-only 500/1,000 bursts passed 250 ms (baseline reversal also passed); exploratory wireless-client failures are separate; venue phones/AP and soak remain | docs/HW-DRILLS.md §4 |
| H6 | Portrait walk-in on a real rotated TV (kiosk Pi + a smart-TV browser) | docs/HW-DRILLS.md §5 |
| D1 | Radio drills: built-in radio limits, channel 13 next to a busy venue Wi-Fi, 5 GHz IBSS on channel 36, venue LAN filter, MTU 1532; temporary channel13/legacy IBSS/rollback verified; permanent shared profile, bridged LAN, 5 GHz/USB, busy RF and 1532-MTU proof remain | docs/VENUE-CLOUD.md §11.3; docs/HW-DRILLS.md §0 |

## 5. Done

One line per ID, so citations in code and commits still resolve. Detail: `git log --grep=<ID>`.

**Bugs.** B1 no appliance password · B2 quiz answer hidden until results · B3 survey removed · B4 ideas are entries of their item · B5 edit chrome only in the editor · B6 unknown roles refused, only `controls` sends commands · B7 zone page retired · B8 stale binary against new scripts (root cause C9) · B9 GO on a held cue's row restarts it · B10 stale Show message actions keep the current stage message · B11 repeated reorder IDs preserve dense cue positions · B12 layout alignment uses the same collision rules as other edits · B13 layout saves stay ordered and acknowledge only the sent document · B14 failed screen assignments leave the prior look and waiting claim intact · B15 installer dry-run preserves existing directory permissions · B16 layout name and geometry updates are atomic · B17 layout editing honours the server tile-height limit · B18 hidden interactions stay off air after restart · B19 overdue offline timers resume · B20 display alerts animate under reduced OS motion · B21 layout confirmations include modal headers and close buttons · B22 next-up labels share countdown rounding · B23 rollback restarts the original database and config · B24 Pi staging supports in-place installation and reruns · B25 backup warnings travel with their archives · B26 updates reserve installation against timer commands and concurrent installers · B27 complete event copies commit atomically and preserve live engines on failure · B28 event copies retain blackout · B29 removed event maps clear on the next copy · B30 Pi IBSS joins fall back from unsupported HT20 · B31 mesh setup fails when every leg or status persistence fails · B32 restarting old primaries follow the healthy primary and retain takeover edits · B33 DRM queries honour Linux ABI counts and connector values, and opted-in hardware failures fail · B34 members and electing boxes reject replica-local REST, WebSocket and OSC mutations · B35 WAL operator reads use a bounded query-only pool instead of queuing behind the writer · B36 phone polls wait for server confirmation and accept one pending vote at a time · B37 the Liquid Glass operator shell follows the upstream single-column grid.

**Event model.** N1 events, rooms, passwords, sessions · N2 home page and event lobby · N3 Event Technician dashboard, event-wide screens · N4 screen type and rotation, template catalog · N5 two push targets · N6 Q&A entries, upvotes, spotlight, auto-approve · N7 event walk-in tiles and feed · N8 htmx 4.0.1 vendored · N9 layout and per-tile animation.

**Venue and cloud** (qualification scope in §4). N12 event end releases screens · N13 pairing by 6-digit code, signed mDNS · N14 `timerpi.local` follows the leading box, clock hint · N15 cloud↔venue event copy · N16 phones stay on the cloud · N17 `TIMERPI_ROLE`, protocol version in `/health` and mDNS, required major match on cloud uplink and registration · N18 box installer and venue mesh · N19 network page radios and peers · N21 boot-time updates; release key embedded 2026-11-12 · H5b external audience load probe and real-Pi browser offline harness.

**Screens and displays.** U1 handheld screens follow the device, not Mounted · U2 no fullscreen tap or hint · U3 schedule line format · U4 Current & next tile · U5 no codes on screens · U6 "Room: <name>" when there are several rooms · U7 list tiles rebuild only on change · DR1, DR2 display reviews (content fit, ftl components, empty tiles give their room away).

**Layouts and the Screens page.** U8 layout editor modal · U9 layout list on each screen card · U10 built-ins listed, never edited · U11 editing a built-in makes a named event copy · U12, U44 Forget · R1-setup, R2-ops setup-page and phone reviews.

**Run tab.** U13 typing never reaches the shortcuts · U14 breaks carry a location · U15 reorders keep the running cue · U16 duration and alert units · U30–U41, U43, U45 room page tidy (one inline table, transport above it, swatches, row menu) · U31 no Reset · U42 no auto-continue, auto-start or hold after · U47 Event Technician naming, footer, FLASH · U48 owner round (rooms optional, sign out, one Send) · U49, U50 Run tab reviews (tablet and phone fit, h:mm, undo on delete, cue editor). · U53 one live workspace: now band and transport (WRAP UP included) on every tab, On air column beside Run and Audience, Import and join QR drawers, rows Go · edit · ⋯ (2026-11-12) · U54 room switcher in the bar.

**Audience.** U17 ftl components · U18 On air strip over a library · U46 tiles painted in place · UA1 on air is separate from the voting phase.

**Home, event pages, roles.** U19–U24 home and event page wording, Leave event · U25 moderators get Run and Audience only · U28 theme picker for the Event Technician only · U29 no Setup tab · U52 Event Technician page: Live rooms table and Setup tab, settings save on change (password keeps Save).

**Cleanup.** C1 `TIMERPI_HTTP_PORT` · C2 ftl-themes submodule · C3 dead code · C4 content-hash asset URLs · C5 templates from the server · C6–C8 doc references, `docs/UI-CONTRACT.md` · C9 UI embedded in the binary · C10 zones retired · C11 layout document/version/save-state module (S2) · C12 atomic screen assignment module (S4) · C13 shared display lifecycle module (S5).

**Engine.** The browser's offline engine matches the server, held to it by `TestEngineParityWithJS` (2026-11-12).

## 6. Suggested order

1. **Hardware**: H3 (deploy), H1, H2, then H5 and H6; D1 with the mesh. Selected checks have passed on Pis; remaining gates are explicit in §4 and HW-DRILLS §7.
2. **N20**: the load harness on the cloud container, before the first show.
3. **Owner decision**: N18 kiosk mode (unblocks N13).
4. **RS37**: review remaining room/rendering responsibilities, behind the browser tests.
5. N11, N19 and U51 as they come up. N10 waits for the multi-day UI.

## 7. Open questions

The [design review](https://github.com/DrEVILish/timerpi/issues/1) has
owner-confirmed scope in PRODUCT §7. A replacement lifecycle and migration
plan remain open. Other decisions needed:

- When to require an end for quick timers, imports and existing events
  ([#2](https://github.com/DrEVILish/timerpi/issues/2)).
- Whether moderators can recover a screen mid-show and which schedule
  fields code-only displays may see ([#3](https://github.com/DrEVILish/timerpi/issues/3)).
- Acceptable state loss after primary failure and how to surface divergent
  browser edits for operator recovery ([#4](https://github.com/DrEVILish/timerpi/issues/4)).
- Measured recovery limits for the first two-room release beyond the
  existing field gates in §4 ([#6](https://github.com/DrEVILish/timerpi/issues/6)).
- Permanent shared CuTePi/TimerPi network profile and rollout
  ([#7](https://github.com/DrEVILish/timerpi/issues/7)).
