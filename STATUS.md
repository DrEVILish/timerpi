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
| 1 | Event walk-in | ⚠️ | `/zone/<name>` page reloads every 30 s with a frozen clock. "Now" comes from the plan, not the live timer. Not a layout, no rotation → **N7**, **N4** |
| 2/3 | Room walk-in | 🟡 | `room` template works. Needs portrait form and rotation → **N4** |
| 4/5 | Room audience display | 🟡 | `main` template, Show/Results end to end. Needs the audience target (**N5**), Q&A wall/spotlight (**N6**), quiz fix (**B2**) |
| 6/7 | Room presenter display | 🟡 | `dsm` template is solid. Needs the presenter target (**N5**) |
| 8/9 | Room moderator | ⚠️ | Dashboard is complete for timing/audience, but it's entered by room code, not event → room pick (**N1, N2**). Tablet pass needed (**N11**) |
| 10 | SuperOperator | ⚠️ | `/super` is transport-only, gated by the device password, and nothing links to it (**N3**) |
| 11 | Audience devices | ⚠️ | Works on an open appliance. Locked out when a device password is set (**B1**). Ideas/survey half-wired (**B3, B4**) |

## 3. Requirements matrix

| Req | Status | Note |
|---|---|---|
| E1–E3 events, join flow, rooms admin | ❌ | N1, N2, N3 |
| E4 event export/import | 🟡 | v2 show bundle is full-fidelity per *room*. Needs event-level wrapping (N1) |
| T1–T5 timing | ✅ | Mature |
| T6 import | ✅ | |
| T7 blackout room / event | 🟡 | Room ✅. Event-wide exists as `/super` bulk (zone-scoped) → re-scope to event (N3) |
| S1 capture | 🟡 | Works. Must also set display type + rotation (N4) |
| S2 layout editor | 🟡 | Works (17 tile types). Needs rotation/portrait preview (N4) |
| S3 templates per display type | 🟡 | 10 templates, not grouped by type, no portrait variants (N4) |
| S4 remote screen management | 🟡 | Per room ✅. No event-wide screens view for the SuperOperator (N3) |
| S5 offline resilience | 🟡 | Code + tests. Two-machine drill pending (H4) |
| S6 no chrome on screens | ⚠️ | B5 |
| S7 rotation | ❌ | N4 |
| S8 live event walk-in | ❌ | N7 |
| S9 per-screen theme + layout, inline rename | 🟡 | Theme + layout per screen ✅. Rename is a button/prompt, not inline double-click (N4) |
| A1 kinds | ⚠️ | Poll ✅ · Word cloud ✅ · Quiz ⚠️ B2 · Q&A ⚠️ N6 · Ideas ⚠️ B4 |
| A2 room-scoped | ✅ | |
| A3 hidden until pushed | ✅ | Absent from the payload |
| A4 two push targets | ❌ | N5 |
| A5 results bars, voting closes | ✅ | |
| A6 hide with animation | ✅ | |
| A7 Q&A wall / spotlight / dismiss / answered | ❌ | N6 |
| A8 per-item moderation | 🟡 | Works for words. Ideas broken (B4) |
| A9 QR join, one vote per device | ✅ | |
| A10 1,000 per room / 200 on a Pi | 🟡 | 1,000 passes in the dev container (p95 24 ms). Not on a Pi or real Wi-Fi (H5) |
| L1 ftl-themes, default `blue-future` | ✅ | Default already `blue-future`. ftl-themes fetch not reproducible (C2) |
| L2–L3 animation everywhere, operator-set | ⚠️ | Only poll/qa/wordcloud tiles (N9) |
| L4 reduced motion on phones only | ✅ | |
| L5 viewport sizing | ✅ | Portrait still to verify (N4) |
| L6 device-class UX (tablet moderator) | 🟡 | N11 |
| H1–H3 appliance, offline LAN, mesh | 🟡 | Hardware drills pending (H1–H4). UI files not embedded (C9) |
| H4 CuTePi OSC | ✅ | |
| H5 any browser as screen | ✅ | |
| M2 day-ready model | ❌ | N10 |
| htmx 4 vendored | ❌ | N8 |

## 4. Open work

### 4.1 Bugs (fix first: small, independent of the new model)

| ID | Bug | Where |
|---|---|---|
| **B1** | With a device password set, `/a/`, `/api/audience/*` and `/zone/` are not AuthGate-exempt. Phones and walk-ins get redirected to `/login`. | `routes/auth.go` `authExempt` |
| **B2** | The quiz correct answer never shows. The dashboard never sends `correct`, and `PollView.Correct` is `omitempty`, so index 0 is dropped. | `templates/dashboard.html`, `timerpi/polls.go:80` |
| **B3** | Survey members drop on WS updates, and there is no survey UI or tile. *Proposal: remove `survey` (not in PRODUCT A1).* | `templates/audience.html`, `timerpi/polls.go` |
| **B4** | Ideas are posted with `parent:0` and become top-level rows. There is no ideas tile. "Add mine" upvotes the whole item. | `templates/audience.html`, `public/src/board.js` |
| **B5** | Audience tiles render edit chrome without the `Editable` guard. | `templates/fragments/b-audience.html` |
| **B6** | Any role string is accepted on join. On an open appliance a `display` session can send commands. | `ws/session.go`, `ws/commands.go` |
| **B7** | Zone page: an unanchored day computes from epoch, so every row shows as done. The configured theme is ignored. (Superseded by N7, but cheap to fix meanwhile.) | `routes/zone.go` |

### 4.2 New work from the spec (in dependency order)

| ID | Work | Spec | Notes |
|---|---|---|---|
| **N1** | **Event model.** `events` table (code, name, supervisor password hash, theme, map asset, day list), with `shows` becoming rooms (`event_id`, room name, position). Moderator password = today's show passphrase, now set by the SuperOperator. Migrate each existing show into a one-room event. Event-level export/import wraps the v2 room bundles | E1, E3, E4 | Keep room codes as the short address for `/a/` and `/d/` QR URLs |
| **N2** | **Home page join flow:** create event (name + supervisor password + rooms) · enter event code → room list → optional moderator password → moderator view · "SuperOperator" → supervisor password | E1, E2 | Replaces the room-code-first home page |
| **N3** | **SuperOperator = event admin.** Event dashboard: every room's live card, links into each room with full rights, add/edit/remove rooms, moderator passwords, event-wide screens view, event blackout, map, theme. Retire the appliance password (PRODUCT §7) | E3, S4, T7 | The appliance password is removed. Box settings need any event's supervisor password, and stay open with no events |
| **N4** | **Display types + rotation.** Screens registry gets `type` (audience/walkin/presenter) and `rotation` (0/90/180/270). Capture modal sets both. Rotation is a CSS transform of the whole layout. Templates are grouped by type, with portrait walk-in variants. The editor previews rotation. Screen names rename inline by double-click/double-tap (S9) | S1–S3, S7, §3.2 | |
| **N5** | **Two push targets.** Interaction gains `toAudience` and `toPresenter` flags. `results` applies to wherever it is shown. The audience read and phone frames carry audience-targeted items only. Boards render by screen type (audience displays ← audience target; presenter displays ← presenter target). The dashboard gets **Show to Audience**, **Show to Presenter**, **Results** and **Hide** | A4, A5 | Replaces single-focus `open`. Keep "one on-air item per target per room" |
| **N6** | **Q&A wall + spotlight.** Submitted questions become children of the open Q&A item (like words), with status `pending → approved → answered` or `dismissed`, plus upvotes per question. New tiles: `qa-wall` (approved, by upvotes) and `spotlight`. Moderator: approve / spotlight / mark answered / dismiss | A7 | Fixes today's "each question replaces the on-air item" |
| **N7** | **Live event walk-in.** Event-aware tiles (`rooms-now`, `event-schedule`, event map) on the normal board renderer, with live WS updates. The `event` template uses them. Retire `/zone/` (redirect) | S8 | |
| **N8** | **htmx 4.** Vendor `htmx.org@4.0.0` locally (`public/src/htmx.js`, no CDN). Remove 2.0.11 and the unused `htmx-ext-ws.js`. Port the existing `hx-` usage (setup, settings, import form) to htmx 4 semantics. New server-rendered UI prefers htmx 4 over hand-written fetch code where it fits | H2, PRODUCT §7 | htmx 4 changes attribute inheritance and event names. Re-test every `hx-` site |
| **N9** | **Animation everywhere.** One `anim`/`animMS` option on every tile, stage messages, spotlight and result reveals. A per-layout default plus a per-tile override, set from the editor UI | L2, L3 | |
| **N10** | **Day-ready model.** Sessions carry `day` (default = event's day 1). All queries are scoped by day. No UI yet | M2 | Do it inside N1's migration |
| **N11** | **Device-class UX.** Moderator view is touch-first for tablets (44 px targets, no hover-only actions). SuperOperator view is laptop-first. Audience page is phone-first | L6 | |

### 4.3 Cleanup (hanging leftovers)

| ID | Item |
|---|---|
| **C1** | Rename env `CAPACITIMER_HTTP_PORT` → `TIMERPI_HTTP_PORT` (keep the old name as a fallback). Fix the `mesh.js` header. |
| **C2** | ftl-themes is a git-ignored clone, not a submodule, so fresh checkouts have no styling. Pin it as a **submodule at an upstream commit**. The local `timerpi` theme commits are shelved (owner: blue-future is the default; a custom theme comes later). Remove the `timerpi` theme from the picker until then. |
| **C3** | Remove dead code: `displayPage` (`routes/pages.go`), the stale `RegisterDisplay` note, the `/frag/shows` comments, and the `mesh.js` check for an unsent `screen` role. |
| **C4** | One cache-bust scheme. Delete the `*.v59`/`*.v60` copies. |
| **C5** | `board.js` hard-codes a copy of `DefaultLayout()`. Read it from `/api/board-templates`. |
| **C6** | Code comments cite archived or never-existing docs (`PLAN §11`, `NOTES-board`, `reviews/UX1-…`). Repoint them. |
| **C7** | Stale comments: `audience.html` calls itself a "stub", and the `/health` comment claims per-role counts. |
| **C8** | Write `docs/UI-CONTRACT.md` (element ids, `data-state`, oob targets, ftl vocabulary) to replace the archived CONTRACT-UI. |
| **C9** | `templates/`, `public/` and ftl-themes are read from disk, not embedded. Embed them, or add a `make dist` tarball. |
| **C10** | The zone concept goes away with N1/N7 . Remove `shows.zone`, `/api/shows/:ident/zone`, `/api/zone-map` and the `/super?zone=` filter once events land. Migrate zone maps to event maps. |

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

## 5. Open questions

None. All owner questions are answered (PRODUCT §7).
