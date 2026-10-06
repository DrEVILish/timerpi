# TimerPi — Status & Roadmap

> **The distance between [docs/PRODUCT.md](docs/PRODUCT.md) (target) and the
> code (today).** Every row cites a PRODUCT requirement ID. Update this file in
> the same commit that changes a status.
>
> Key: ✅ meets spec · 🟡 works with caveats, or unproven on hardware ·
> ⚠️ partial or buggy · ❌ missing
>
> Baseline 2026-10-05, commit `ccd415f`: `go build`, `go vet` and
> `go test ./...` are green (12 tested packages). Owner answers of 2026-10-05
> are folded in (PRODUCT §7). Old trackers are in `docs/archive/`.

---

## 1. The big shift: today's model vs the spec

| | Today (code) | Spec (PRODUCT) |
|---|---|---|
| Top-level object | **Show** = one room, with its own 8-char code | **Event** with one code, containing **rooms** |
| Grouping rooms | Free-text `zone` label | Rooms belong to an event |
| SuperOperator | Whoever holds the *device* password; transport + blackout only | Event creator with a *supervisor* password; full admin of the event |
| Room operator | Anyone with the room's code (+ optional room password) | Moderator: event code → pick room → optional moderator password set by the SuperOperator |
| Push | One state: `hidden → open → results` (phones + every screen with a poll tile) | Two targets: **Show to Audience** (phones + audience displays) and **Show to Presenter** (DSM) |
| Q&A | One question on air at a time | Approved **wall** + **spotlight**; dismiss / mark answered |
| Screens | Named, themed, with a layout | Plus a **display type** (Audience / Walk-in / Presenter) and **rotation** |
| Days | One running order per show | Single day now; model allows days |
| htmx | 2.0.11 vendored, lightly used | **htmx 4** vendored |

Most existing machinery carries over: the engine, hub, audience lane, layouts, capture, themes and mesh. The event layer sits **above** today's show and becomes its parent. See ARCHITECTURE §14 for the migration shape.

## 2. Reference event, surface by surface (PRODUCT §5)

| # | Surface | Status | Gap |
|---|---|---|---|
| 1 | Event walk-in | ✅ | `event` / `event-portrait` templates: "All rooms now" (live now/next per room), event schedule (one column per room), event map, clock. Fed by `/api/shows/:room/walkin` (5 s). The old `/zone/` page is retired (C10) |
| 2/3 | Room walk-in | ✅ | `room` / `room-portrait` templates; rotation per screen |
| 4/5 | Room audience display | ✅ | `main` template: one large Audience-item tile (poll/quiz bars, Q&A wall + spotlight, word cloud, ideas) + join QR. Text scaling for big screens comes in the UI pass |
| 6/7 | Room presenter display | ✅ | `dsm` template; its Audience-item tile follows the Presenter target. Display types (N4) still to do |
| 8/9 | Room moderator | 🟡 | ✅ Event code → pick room → optional room password. Room-isolated (tested). Dashboard is complete for timing/audience. Tablet pass still to do (**N11**) |
| 10 | SuperOperator | 🟡 | ✅ `/e/<code>/admin`: live room cards (state, now/next, time left, screens) with GO/Pause/Blackout, blackout all, rooms admin (add, inline rename, reorder, delete, passwords), event settings (name, theme, map, password, delete). Moderates every room. Still to do: event-wide screens view (**N3**) |
| 11 | Audience devices | ✅ | Rebuilt phone page: vote/change vote, results with your answer and the quiz verdict, ask/ideas with "waiting for review", upvotes, spotlight, word cloud. WS lane with REST fallback |

## 3. Requirements matrix

| Req | Status | Note |
|---|---|---|
| E1–E3 events, join flow, rooms admin | ✅ | Tested in `routes/events_test.go` |
| E4 event export/import | 🟡 | Room export (`/api/shows/:c/file`) and room import into an event work. A single whole-event file is still to do |
| T1–T5 timing | ✅ | Mature |
| T6 import | ✅ | |
| T7 blackout room / event | ✅ | Room, and event-wide from the SuperOperator dashboard |
| S1 capture | ✅ | Capture asks name, display type (Audience/Walk-in/Presenter), layout (templates for that type), mounting (rotation) and theme; only into rooms you moderate |
| S2 layout editor | ✅ | Editor shows the screen's real canvas (rows × 12, landscape or portrait) in its theme |
| S3 templates per display type | ✅ | 13 templates in a catalog grouped by type, incl. portrait walk-ins (`boards.Templates`) |
| S4 remote screen management | 🟡 | Per room ✅. No event-wide screens view for the SuperOperator (N3) |
| S5 offline resilience | 🟡 | Code + tests. Two-machine drill pending (H4) |
| S6 no chrome on screens | ⚠️ | B5 |
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
| A10 1,000 per room / 200 on a Pi | 🟡 | Dev container: 1,000 phones, p95 30 ms, 1,000/1,000 join storm; poll broadcasts coalesced (≤4/s per room) so vote bursts do not fan out per vote. Not yet on a Pi or real Wi-Fi (H5) |
| L1 ftl-themes, default `blue-future` | ✅ | Default already `blue-future`. ftl-themes fetch not reproducible (C2) |
| L2–L3 animation everywhere, operator-set | ✅ | N9 |
| L4 reduced motion on phones only | ✅ | |
| L5 viewport sizing | ✅ | Layouts are canvases that fill the screen; tile text scales with the tile (container query units), so 720p/4K/portrait all work |
| L6 device-class UX (tablet moderator) | 🟡 | N11 |
| H1–H3 appliance, offline LAN, mesh | 🟡 | Hardware drills pending (H1–H4). UI files not embedded (C9) |
| H4 CuTePi OSC | ✅ | |
| H5 any browser as screen | ✅ | |
| M2 day-ready model | 🟡 | `cues.day` and `events.days` exist (always 1). Queries are not day-scoped yet (N10) |
| htmx 4 vendored | ✅ | N8 |

## 4. Open work

### 4.1 Bugs (fix first: small, independent of the new model)

**Code review 2026-10-06:** 107 more issues (8 critical, 58 warnings, 41 suggestions) are logged with locations, fixes and status in [docs/BUGLOG.md](docs/BUGLOG.md). Fix RC1–RC8 first.

| ID | Bug | Where |
|---|---|---|
| **B1** | ✅ Fixed 2026-10-05: there is no appliance password any more; audience and walk-in pages are open by design. | `routes/access.go` |
| **B2** | ✅ Fixed: quiz requires its answer; it is revealed (index 0 included) only with results. | |
| **B3** | ✅ Fixed: survey removed (not a product kind); leftover rows deleted at startup. | |
| **B4** | ✅ Fixed: ideas are entries of their item, shown as a wall with upvotes. | |
| **B5** | ✅ Fixed: audience tiles render edit chrome only in the editor. | |
| **B6** | ✅ Fixed 2026-10-05: unknown roles are refused at join, and only `controls` may send commands. | `ws/session.go`, `ws/commands.go` |
| **B7** | ✅ Moot 2026-10-06: the zone page is retired (C10). | `routes/zone.go` |
| **B8** | ✅ Fixed 2026-10-06: phones got "vote needs a device id". The live service ran last night's binary but serves `public/` from the working tree, so the new phone script (device id now issued by the server, BUGLOG RW2) met the old server. Rebuilt and restarted; a phone vote is counted again. Root cause is C9 (now urgent). | `bin/timerpi`, C9 |

### 4.2 New work from the spec (in dependency order)

| ID | Work | Spec | Notes |
|---|---|---|---|
| **N1** | ✅ Done 2026-10-05: `events` table; shows are rooms (`event_id`, `room_pos`, `room_pw` hash); PBKDF2 passwords; HMAC session cookies; orphan shows adopted at startup (zones → one event). Left: one whole-event export file | E1, E3, E4 | |
| **N2** | ✅ Done 2026-10-05: new home page (join by code / create event / open a screen / recent), event lobby with room sign-in and SuperOperator sign-in. The old setup wizard and connect sheet are retired | E1, E2 | |
| **N3** | ✅ Done 2026-10-05: SuperOperator dashboard with per-room live cards linking to each room's Run and Screens pages (screens online per room) | E3, S4, T7 | |
| **N4** | ✅ Done 2026-10-05: screens `kind` + `rotation`; capture and Screens page (`screens.js`) rebuilt; layouts have `rows` + `orientation`; template catalog by type | S1–S3, S7, S9 | |
| **N5** | ✅ Done 2026-10-05: `to_audience` / `to_presenter`, results follow the targets, phones receive the audience target only, tiles follow `opts.target` | A4, A5 | |
| **N6** | ✅ Done 2026-10-05: entries with pending/approved/answered/dismissed, upvotes, spotlight, auto-approve; moderator panel rebuilt (`moderate.js`) | A7 | |
| **N7** | ✅ Done 2026-10-05: `rooms` + `eventschedule` tiles, event map default on the map tile, `/api/shows/:room/walkin` feed | S8 | |
| **N8** | ✅ Done: htmx 4.0.0 vendored as `public/src/htmax.min.js` (htmx + bundled extensions); 2.0.11 removed; the `htmx:oobAfterSwap` listener is ported. Existing `hx-` sites use no inherited attributes. New UI should prefer htmx 4. Reference: `docs/reference/htmx4/` | H2 | |
| **N9** | ✅ Done 2026-10-05: layout default animation (`anim`/`animMS`) + per-tile override for every content tile, set in the editor; content tiles animate on change | L2, L3 | |
| **N10** | 🟡 `cues.day` (default 1) and `events.days` added. Left: day-scope the queries when multi-day UI arrives | M2 | |
| **N11** | 🟡 Room page reorganised into Run · Audience · Setup tabs (tablet-friendly, wraps); touch targets ≥44 px on coarse pointers on the new pages. A dedicated tablet layout for the Run tab is still open | L6 | |
| **N12** | Event end date/time; boxes and screens release at end + 4 h (never mid-timer) and on event delete | H8 | [VENUE-CLOUD.md](docs/VENUE-CLOUD.md) §3 |
| **N13** | Pairing codes on unpaired displays; pair from the event dashboard (cloud or `timerpi.local`); event mesh key; signed mDNS (closes BUGLOG RW15) | H3, H7 | §4–5 |
| **N14** | `timerpi.local` alias on the primary; box clock from primary / NTP / SuperOperator browser | H3 | §5 |
| **N15** | Cloud ↔ primary link: pull the event to the venue, stream the copy back | H6 | §6 |
| **N16** | Audience relay through the cloud; "audience paused" when the link is down | H2, H6 | §6 |

### 4.2a Owner feedback 2026-10-06 (UI round)

Owner's list after trying the build, grouped by area. Wording kept close to the owner's.

**Screens and displays**

| ID | To do |
|---|---|
| **U1** | ✅ Done 2026-10-06: on a touch device with an orientation sensor (phones, tablets) screens ignore the Mounted rotation (first paint and live pushes) and follow the device; turning it re-lays the page. Kiosks/TVs keep Mounted. Browser test `device-rotation`. |
| **U2** | ✅ Done 2026-10-06: no tap/F fullscreen and no hint chip on any screen (ready card included). Browser test `display-no-fullscreen`. |
| **U3** | ✅ Done 2026-10-06: schedule tiles show `hh:mm  Title - Speaker` (planned start, 24 h, no duration; no time until the day has a start). Browser test `walkin-format`. |
| **U4** | ✅ Done 2026-10-06: new **Current & next** tile (`nownext`): `Current Session: <title>` / `Next Session: <title>`, each with `Start Time`, `Duration`, `Speaker`. The Room walk-in templates use it; screens already on the old walk-in layout get it by picking the template again. Browser test `walkin-format`. |
| **U5** | ✅ Done 2026-10-06: screens no longer print the room code ("Session XXXX-XXXX" lines, the title tile's code) or the corner join card with the control-room link. The audience join QR tile stays (that is how phones join). Test `TestScreensShowNoCode`. |
| **U6** | ✅ Done 2026-10-06: screens print `Room: <name>` when the event has more than one room (titles, stage status, board title tile, walk-in room cards and schedule columns via the feed's `label`), and the bare name otherwise. Tests `TestScreensRoomPrefix`, browser `room-prefix`. |
| **U7** | ✅ Done 2026-10-06: screens without audience tiles ignore poll updates, and list tiles (schedule, all rooms, event schedule, current & next, messages) only rebuild when what they show changes. That also ends the flicker on the event walk-in's 5 s refresh. Browser test `walkin-ignores-audience` (fails on the old code). |

**Layouts and the Screens page**

| ID | To do |
|---|---|
| **U8** | ✅ Done 2026-10-06: the editor is an almost-full-screen modal that opens already editing (one press), with a legend and labelled handles (⠿ move, ◢ resize, ⚙ settings, bin remove; tooltips and accessible names), buttons grouped right, hover outline, and a "Used by N screens" warning. Fullscreen no longer pops (U2). Browser test `layout-editor-flow`. |
| **U9** | ✅ 2026-10-06: the Layout list on each screen card now offers the room's existing layouts and "Plain timer (no layout)" (applied at once) plus "New from template" (confirm). An unassigned screen no longer shows another layout's name (BUGLOG RW36), and going back to the plain timer or default theme reaches the live TV (RW35). Picking a template and the live TV following already worked in testing; the reported failure may have been the B8 mismatch this afternoon. Browser test `screen-layout-change`. |
| **U10** | ✅ Done 2026-10-06: built-ins are listed in each screen's Layout list as **[built-in] …** and shown as they are (screens.template); they are never edited. Layouts belong to the event (every room lists them; deleting a room keeps them). The built-ins themselves are to be reworked later (owner). |
| **U11** | ✅ Done 2026-10-06: Edit layout on a built-in (or the plain timer) asks for a name and what to start from, lists the same-type screens across the event by room (a moderator sees only rooms they moderate), creates the event layout, switches the ticked screens and opens the editor on it. |
| **U12** | ✅ Done 2026-10-06: Forget is a red X (ftl `btn-danger btn-icon`, close icon) in each screen card's top-right corner, with a confirm that says the screen is released. Browser test `screen-forget`. |

**Running order (Run tab)**

| ID | To do |
|---|---|
| **U13** | ✅ Fixed 2026-10-06: while a cue cell was being edited, the client's row check saw the cell empty and rebuilt the table, destroying the input, so the next keys hit the shortcuts (Space = GO, R = reset). The rebuild now waits for the edit; the Screens page skips its redraw while a field is in use. Browser test `keys-while-typing`. |
| **U14** | ✅ Done 2026-10-06: quick add has a Session/Break switch (Speaker box for sessions, "Where" box for breaks); `cues.location` is edited inline (the Speaker column of a break) and in the details panel; schedules read "Coffee — Great Hall", Current & next shows Location for a break. Browser test `break-location`. |
| **U15** | ✅ Verified 2026-10-06: dragging a row above the running cue, or dragging the running cue itself, keeps it running with the same anchor (engine fix BUGLOG RC2). Browser test `reorder-running`. |
| **U16** | ✅ Fixed 2026-10-06: the reset came from U13 (an "r" or space typed into a title reached the shortcuts). Also fixed: the details panel showed 30 minutes as "30:00" and saved it back as 30 hours, and alert/hold fields labelled m:ss were read as h:mm. Durations stay h:mm (2026-10-05 decision); alerts and hold are m:ss. Browser test `edit-running-cue`. |

**Audience**

| ID | To do |
|---|---|
| **U17** | ✅ Done 2026-10-06: the Audience tab uses ftl `.table`, `.switch`, `.btn-icon` and `<progress class="progress">`; the phone page and the audience screen tiles use `.panel`, `.empty-state`, `.btn`, `<progress class="progress">`, `.alert`, `.list` and `.badge`. Browser tests `audience-table`, `audience-ftl`. |
| **U18** | ✅ Done 2026-10-06: one row per item with Presenter / Audience / Results switches (Results locked until shown), Type, Title / Question (Live/Results/to-review badges), Approve automatically, edit and delete icon buttons; a detail row holds the tally or the moderation queue. Browser test `audience-table`. |

**Home and event pages**

| ID | To do |
|---|---|
| **U19** | ✅ Done 2026-10-06: the screen door is "Open a screen here" with an **Open a screen** button and one hint line. |
| **U20** | ✅ Done 2026-10-06: "Recent on this device" sits above the title, only when this browser has one (hidden again when the last is forgotten), each row with **Resume**. |
| **U21** | ✅ Done 2026-10-06. |
| **U22** | ✅ Done 2026-10-06. |
| **U23** | ✅ Done 2026-10-06 ("Join event", "Create event", error text). Browser test `home-page` covers U19–U23. |
| **U24** | ✅ Done 2026-10-06: event pages and the room page say **Leave event** (`GET /e/<code>/leave`: drops this browser's sessions for that event only); the code at the top is labelled **Event ID**. Test `TestLeaveEventOnlyThisEvent`. |

**Roles and the room page**

| ID | To do |
|---|---|
| **U25** | ✅ Done 2026-10-06: moderators get Run and Audience only. Screens, layouts, presets and screen capture need the SuperOperator (API and page). Test `TestModeratorRunAndAudienceOnly`, browser `room-scope`. |
| **U26** | 🟡 Moderators import a running order from the Run tab (Import panel). Header matching is deferred (owner, 2026-10-06). |
| **U27** | Deferred with U26's header matching (owner, 2026-10-06). |
| **U28** | ✅ Done 2026-10-06: the room page's theme picker shows for the SuperOperator only; screen themes are set on the Screens page (SuperOperator). |
| **U29** | ✅ Done 2026-10-06: no Setup tab. Import → Run (collapsible); Duplicate → SuperOperator dashboard rooms table; presets → Screens page; the old Screens panel (a copy of the Screens page) is gone. |

**Room page tidy (owner, 2026-10-06)**

Decisions: auto-continue, auto-start ("at HH:MM") and Hold after are removed as behaviour too: the engine stops honouring them and imports ignore them (stored values stay unused; midnight/day rollover stays). Reset is removed entirely (no button, no R key; GO on a row restarts it). The inline table columns are: drag handle, #, Type, Title, Speaker / Where, Duration, Start, End, Timer, At zero, Alert 1 + colour, Alert 2 + colour, Notes, row actions. Tags and row colour stay in the details panel.

| ID | To do |
|---|---|
| **U30** | ✅ Done 2026-10-06: Alert colours (details panel, add row, row dots) use ftl `.swatches` plus a native colour input. |
| **U31** | ✅ Done 2026-10-06: Transport above the running order; Stop/Reset and the R key are gone. |
| **U32** | ✅ Done 2026-10-06: Adjust buttons sit right under the readout. |
| **U33** | ✅ Done 2026-10-06: The readout scales with the rail (container units); "0:09.4" fits at any width. |
| **U34** | ✅ Done 2026-10-06: The time of day sits at the top centre of the header bar. |
| **U35** | ✅ Done 2026-10-06: The key hint line and its popover are gone. |
| **U36** | ✅ Done 2026-10-06: Filter and undo button gone; Cmd/Ctrl+Z still undoes. |
| **U37** | ✅ Done 2026-10-06: Left column: Current cue, Messages, Import. |
| **U38** | ✅ Done 2026-10-06: No Next up in the Current cue pane. |
| **U39** | ✅ Done 2026-10-06: One table with the agreed columns; every cell edits inline; a footer row adds a cue (typed values survive live updates). |
| **U40** | ✅ Done 2026-10-06: Rows drag by the grip; no arrows. |
| **U41** | ✅ Done 2026-10-06: Duplicate is in the right-click / long-press row menu (ftl `.context-menu`). |
| **U42** | ✅ Done 2026-10-06: Removed as behaviour too: nothing starts by itself, holds move nothing, Normalize drops them on every write. |
| **U43** | ✅ Done 2026-10-06: Breaks wear the theme's `--accent-2`. |
| **U44** | ✅ Done 2026-10-06: Forget is ftl's `.btn-close` (the theme's own close button), tinted danger. Browser test `screen-forget`. |

### 4.3 Cleanup (hanging leftovers)

| ID | Item |
|---|---|
| **C1** | ✅ Done: `TIMERPI_HTTP_PORT` (legacy name still honoured). |
| **C2** | ✅ Done: ftl-themes is a submodule pinned to an upstream commit. The local `timerpi` theme is shelved (branch `timerpi-theme-local` in the old clone), so it is no longer in the picker. |
| **C3** | ✅ Done: dead `displayPage`, the stale registration guard, `htmx-ext-ws.js` and the `/frag/shows` comments are removed. (`screen` is a valid join role, so the `mesh.js` check stays.) |
| **C4** | ✅ Done: one scheme, the `asset` template func with a content-hash path segment (ARCHITECTURE §12). The versioned copies and `bump-assets.sh` are deleted. |
| **C5** | 🟡 Template presets now come from the server catalog. `board.js` still carries a `FACTORY_DEFAULT` copy for "Reset". |
| **C6** | ✅ References to files that never existed are removed. Citations of `PLAN §…`, `CONTRACT-UI`, `NOTES-board` resolve via `docs/archive/README.md`. |
| **C7** | ✅ Done. |
| **C8** | ✅ `docs/UI-CONTRACT.md`. |
| **C9** | ✅ Done 2026-10-06: `templates/` and `public/` are embedded in the binary (`-dev` still reads disk). ftl-themes stay on disk (pinned submodule, 48 MB, independent of TimerPi's code). |
| **C10** | ✅ Done 2026-10-06 (BUGLOG RW7): `/zone/*` redirects home; `/api/shows/:ident/zone` and `/api/zone-map` are gone; zone maps were migrated to event maps at adoption. Left: the unused `shows.zone` column and the bundle's legacy zone/map fields. |

### 4.4 Hardware / field proof

| ID | Drill | Runbook |
|---|---|---|
| H1 | Two-Pi LAN mesh | docs/HW-DRILLS.md §1 |
| H2 | DRM renderer 1080p50 | docs/HW-DRILLS.md §2 |
| H3 | Pi deploy acceptance | docs/PI-DEPLOY.md |
| H4 | Two-machine browser P2P offline drill | docs/HW-DRILLS.md §3 |
| H5 | **Audience load on a Pi 4/5 over real Wi-Fi**: 200 phones (floor), then 1,000. The access point is usually the bottleneck | to write |
| H6 | Portrait walk-in on a real rotated TV (kiosk Pi + a smart-TV browser) | to write with N4 |

### 4.5 Suggested order

1. **B1–B7**: small, safe, and they unblock demos today.
2. **C2 + N8**: reproducible themes and htmx 4 *before* new UI is written on top of htmx 2.
3. **N1 + N10 → N2 → N3**: the event model, join flow and SuperOperator. This is the big one; land it in that order.
4. **N5 → N6**: push targets, then the Q&A wall and spotlight.
5. **N4 → N7**: display types, rotation and portrait, then the live event walk-in.
6. **N9, N11**: animation everywhere and the device-class UX pass.
7. Remaining cleanup, then **H5**, then H1–H4 and H6 on hardware.

After the 2026-10-06 review: BUGLOG RC1–RC8 come before everything above, and the security (RW1–RW16) and performance (RW53–RW58) warnings come before H5.

## 5. Open questions

None open. VENUE-CLOUD §7 records the sync rules (2026-10-06); confirm the "stable link" time and offline-created events when N15 starts.
