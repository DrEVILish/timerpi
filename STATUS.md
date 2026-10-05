# TimerPi — Status & Roadmap

> **What is done, what is broken, what is next.** Measured against
> [docs/PRODUCT.md](docs/PRODUCT.md). Every row cites a PRODUCT requirement ID.
> Update this file in the same commit that changes a status.
>
> Key: ✅ works and verified · 🟡 works with caveats, or unproven on hardware ·
> ⚠️ partial or buggy · ❌ missing
>
> Baseline: full doc reset on 2026-10-05 against commit `ccd415f`. `go build`, `go vet`
> and `go test ./...` are all green (12 tested packages). The previous trackers
> (PLAN.md, PROJECT.md) are archived in `docs/archive/`.

---

## 1. The reference event, surface by surface

| # | Surface | Status | Notes |
|---|---|---|---|
| 1 | Event walk-in | ⚠️ | `/zone/<name>` exists, but it is not a layout, so it cannot be customised. It reloads every 30 s with a frozen clock. "Now" comes from the plan, not the live timer, and it shows everything as done before the day starts. It ignores the configured theme. There is no UI to set a room's zone or upload the map. The `event` board template covers only one room. → **G1, G2** |
| 2/3 | Room walk-in | 🟡 | The `room` template does it: clock, current session, next session, schedule and QR. "Next session time" shows as the next-up tile. Needs a visual check against the brief. |
| 4/5 | Room main | 🟡 | The `main` template plus Show and Results work end to end (demo 2026-10-05). Q&A display is weak (**G3**). The quiz answer is broken (**B2**). |
| 6/7 | Room DSM | ✅ | The `dsm` template: countdown, alerts, messages, plus a poll tile. |
| 8/9 | Room operator | 🟡 | The dashboard is complete for timing and audience items. Room isolation depends on the share code and optional room password. Nav is missing links to the room's audience URL and walk-in (**G5**). |
| 10 | SuperOperator | ⚠️ | `/super` gives a cross-room status panel with transport and blackout only. Nothing links to it. It cannot edit room content, and with no device password set, anyone can open it (**G4**, Q5). |
| 11 | Audience devices | ⚠️ | `/a/<code>` works on an open appliance. **When a device password is set, phones are redirected to /login (B1).** Ideas and survey are half-wired (**B3, B4**). |

## 2. Requirements matrix

| Req | Status | Note |
|---|---|---|
| T1–T5 timing | ✅ | Mature: engine, schedule, transport, messages, rate, auto-start |
| T6 import/export | ✅ | XLSX/CSV/JSON import; v2 full-fidelity show file (cues, interactions, votes, screens, layouts, presets, map) |
| T7 blackout | ✅ | Per room and SuperOperator bulk |
| S1 capture from `/d/` | ✅ | |
| S2 customisable layouts | ✅ | Editor in the `/screens/` preview: 17 tile types, drag, resize, keyboard nudge |
| S3 templates | 🟡 | 10 templates. `event` is single-room (G1) |
| S4 remote screen management | ✅ | Gallery, live preview, theme/layout push, presets |
| S5 offline resilience | 🟡 | Code and tests done. Two-machine P2P drill pending (H4) |
| S6 no chrome on screens | ⚠️ | Audience tiles still render gear/delete buttons (hidden by CSS) (B5) |
| S7 zone aggregate + map | ⚠️ | G1, G2 |
| A1 kinds | ⚠️ | Poll ✅ · Word cloud ✅ · Quiz ⚠️ (B2) · Q&A ⚠️ (G3) · Ideas ⚠️ (B4) · Survey ❌ (data-layer only, B3) |
| A2 room-scoped | ✅ | Interactions are per show |
| A3 hidden until pushed | ✅ | Hidden items are absent from the payload |
| A4 Show = phones + screens together | ✅ | One state change, one broadcast |
| A5 Results: count, %, bars; voting closes | ✅ | |
| A6 Hide with exit animation | ✅ | |
| A7 per-item moderation | 🟡 | Approve/hide/delete works. Approved Q&A questions are not shown as a wall (G3) |
| A8 QR join, one vote per device, change until results | ✅ | |
| A9 ≥1,000 phones per room | 🟡 | 1,000-client harness passes in the dev container (p95 24 ms). **Not run on a Pi or on real venue Wi-Fi** (H5) |
| L1 ftl-themes | 🟡 | All surfaces themed; per-screen themes. The `timerpi` theme is not reproducible from a fresh clone (C2) |
| L2 appear/disappear animation | ⚠️ | Only the poll/qa/wordcloud tiles animate. Messages, tiles appearing, and results swaps do not (G6) |
| L3 operator-customisable animation | ⚠️ | A per-tile gear form exists on audience tiles only (G6) |
| L4 reduced motion on phones only | ✅ | |
| L5 viewport sizing | ✅ | |
| P1–P2 role ladder, room isolation | 🟡 | B1 and B6 are open |
| P3 SuperOperator | ⚠️ | G4 |
| P4 no accounts/PII | ✅ | |
| H1–H2 appliance, offline LAN | ✅ | All assets local. No CDN |
| H3 device mesh | 🟡 | Single-host drill passed. Two-Pi drill pending (H1) |
| H4 CuTePi OSC | ✅ | Wire-tested to a UDP peer |

## 3. Open work

### 3.1 Bugs (fix first)

| ID | Bug | Where |
|---|---|---|
| **B1** | With a device password set, `/a/`, `/api/audience/*` and `/zone/` are not AuthGate-exempt. Phones and walk-in screens get redirected to `/login`. | `routes/auth.go` `authExempt` |
| **B2** | Quiz correct answer never shows: the dashboard never sends `correct`, and `PollView.Correct` is `omitempty`, so index 0 is dropped from JSON. | `templates/dashboard.html`, `timerpi/polls.go:80` |
| **B3** | Survey members drop on WS updates (`render({poll})` ignores `survey[]`). No UI or tile for survey. | `templates/audience.html` |
| **B4** | Ideas submissions are posted with `parent:0` and become top-level rows. There is no ideas tile. "Add mine" upvotes the whole item. | `templates/audience.html`, `public/src/board.js` |
| **B5** | Audience tiles render edit chrome without the `Editable` guard. | `templates/fragments/b-audience.html` |
| **B6** | On an open appliance, a `display` session can send commands. Any role string is accepted on join. | `ws/session.go`, `ws/commands.go` |
| **B7** | Zone page: "now" comes from the plan, not the engine. An unanchored day computes from epoch, so every row shows as done. The configured theme is ignored. | `routes/zone.go` |

### 3.2 Gaps against the brief (build next)

| ID | Gap | Proposal |
|---|---|---|
| **G1** | Event walk-in is not a live, customisable layout | Add zone-aware tiles (`rooms-now`: every room's current session and room name; `zone-schedule`: all rooms' day). Make the `event` template a zone layout, and retire the 30 s-refresh `/zone/` page or make it redirect. |
| **G2** | No UI for zones or maps | Add a zone field to the room dashboard or Settings, and a map upload/picker in the SuperOperator panel. |
| **G3** | Q&A is one-question-at-a-time. Audience questions are top-level rows | Make submitted questions children of an open Q&A item (like word-cloud words). Add a wall tile listing approved questions by upvotes, and an operator "spotlight" for one question. |
| **G4** | SuperOperator is transport-only and undiscoverable | Link every room card to its dashboard and screens. Add an event-wide screens view. Link `/super` from the home page for device-password holders. Decide Q5. |
| **G5** | Discoverability | The dashboard shows and links the room's audience URL/QR, walk-in, and screens. The home page links SuperOperator and zones. |
| **G6** | Animation coverage | Use the same `anim`/`animMS` options on every tile, the stage message overlay, and result reveals. Add a per-layout default animation, so operators set it once. |

### 3.3 Cleanup (hanging leftovers)

| ID | Item |
|---|---|
| **C1** | Rename env `CAPACITIMER_HTTP_PORT` → `TIMERPI_HTTP_PORT` (keep the old name as a fallback). Fix the `mesh.js` header. |
| **C2** | ftl-themes is git-ignored, not a submodule. The `timerpi` theme is only 4 local commits in the dev box's clone, so a fresh checkout has no styling. Decide Q3. |
| **C3** | Remove dead code: `displayPage` (`routes/pages.go`), the stale `RegisterDisplay` note, the unused `htmx-ext-ws.js`, and the `/frag/shows` comments. Remove the `mesh.js` check for a `screen` role nobody sends. |
| **C4** | One cache-bust scheme only. Delete the `*.v59`/`*.v60` copies, and decide between versioned copies and the path-rev scheme. |
| **C5** | `board.js` hard-codes a copy of `DefaultLayout()`. Read it from `/api/board-templates` instead. |
| **C6** | Code comments cite docs that no longer exist (`reviews/UX1-…`, `SUPERVISOR-report`, `NOTES-display`, …) or are now archived (`PLAN §11`, `NOTES-board §5`). Repoint them to ARCHITECTURE/PROTOCOL. |
| **C7** | Fix stale comments: `audience.html` calls itself a "stub", and the `/health` comment claims per-role counts. |
| **C8** | Write a short, current UI contract (element ids, `data-state` values, oob targets, ftl class vocabulary) as `docs/UI-CONTRACT.md`. It replaces the archived append-only `docs/archive/CONTRACT-UI.md`, whose §5–§7 are still the best reference until then. |
| **C9** | Deploy packaging: `templates/`, `public/` and `third_party/ftl-themes` are read from disk (not `go:embed`). PI-DEPLOY §2 now copies them by hand. Consider embedding them, or a `make dist` tarball, so the binary and UI can't drift apart. |

### 3.4 Hardware / field proof

| ID | Drill | Runbook |
|---|---|---|
| H1 | Two-Pi LAN mesh (claim, join, takeover, reunion) | docs/HW-DRILLS.md §1 |
| H2 | DRM renderer 1080p50 on a Pi | docs/HW-DRILLS.md §2 |
| H3 | Pi deploy acceptance (flash → splash → service → mDNS) | docs/PI-DEPLOY.md |
| H4 | Two-machine browser P2P offline drill | docs/HW-DRILLS.md §3 |
| H5 | **Audience load on a Pi 4/5 over real Wi-Fi**: 200 phones (floor), then 1,000. Note: the venue Wi-Fi access point, not TimerPi, is usually the limit at this scale | to write |

### 3.5 Suggested order

1. **B1–B7** (correctness; B1 is a show-stopper the moment a password is set).
2. **G5 + G4** (operators can find everything; SuperOperator becomes usable).
3. **G1 + G2** (event walk-in done properly).
4. **G3** (Q&A wall), then **G6** (animation everywhere).
5. **C1–C8** cleanup, which can be interleaved.
6. **H5**, then H1–H4 on hardware.

## 4. Open questions for the owner

| ID | Question | Default if no answer |
|---|---|---|
| Q1 | Walk-in and main screens need a browser (only the countdown has a native renderer). Are screens kiosk-browser Pis/TVs? | Yes, kiosk browsers. The native DRM renderer stays an optional DSM fallback |
| Q2 | Docs used to say "htmx v4". The code ships htmx **2.0.11** and uses it lightly. Upgrade, stay, or drop htmx? | Stay on 2.x and don't expand its use |
| Q3 | ftl-themes is `DrEVILish/ftl-themes`, the same owner as this repo. May we push the `timerpi` theme upstream and pin ftl-themes as a git submodule? (AGENTS.md currently forbids pushes there.) | Make it a submodule. Keep the theme local until told otherwise |
| Q4 | Q&A on the main screen: a wall of approved questions, one spotlighted question, or both? | Both (G3) |
| Q5 | Should the SuperOperator edit room content (sessions, polls) or only drive transport and blackout? | Full control via links into each room's dashboard |
| Q6 | Are "Send to phones" and "Show on screens" one button (today) or two separate buttons? | One button (as the brief reads) |
| Q7 | Does a room operator need a password, or is the share code enough? | Code enough. Room password optional |
| Q8 | Are multi-day events in scope (one room = one show = one day today)? | Out of scope. Clone the show per day |
