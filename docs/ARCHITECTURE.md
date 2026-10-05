# TimerPi — Architecture

> **How TimerPi is built.** This file describes the code as it exists today.
> Wire-level details (every route and frame) are in [../PROTOCOL.md](../PROTOCOL.md).
> What the product must do is in [PRODUCT.md](PRODUCT.md). What is done and open is in [../STATUS.md](../STATUS.md).
>
> Last verified against the code: 2026-10-05 (commit `ccd415f`, app version 2.0).
> §1–§13 describe the code **as it is**. §14 sketches how it moves to the
> event model in PRODUCT. Update §1–§13 as each STATUS item lands.

---

## 1. Stack

| Layer | Choice |
|---|---|
| Server | Go 1.25, single binary, **gin** for HTTP and **gorilla/websocket** for WS, on **one port**, listening on `0.0.0.0` (default 80) |
| Storage | **SQLite** via `sqlx` + `mattn/go-sqlite3` (CGO), WAL mode. One file: `<data dir>/timerpi.db` |
| Templates | Go `html/template`, server-rendered pages and fragments |
| Client JS | Vanilla ES modules in `public/src/` with no build step and no framework. **htmx 4.0.0** is vendored as `public/src/htmax.min.js` (htmx plus its bundled extensions: ws, sse, preload, …). Reference docs: `docs/reference/htmx4/`. Today it is used lightly (setup, settings, import form); most interactivity is plain JS plus WS frames |
| Styling | **ftl-themes**, served at `/ftl/` from `third_party/ftl-themes`. TimerPi adds layout-only CSS in `public/css/timerpi.css` |
| QR | `skip2/go-qrcode` |
| Import | `tealeg/xlsx` + `excelize` (XLSX/XLS), CSV, JSON (`importdocs/`) |
| Discovery | mDNS `_timerpi._tcp` (`mdns/`, `mesh/`) |
| Pi output | Optional native DRM/KMS 1080p50 renderer for the countdown (`drm/`), plus a framebuffer boot splash (`scripts/splash/`) |
| Dependency pin | `tools/deps/deps.go`. **Never run `go mod tidy`** (see AGENTS.md) |

Brand: primary purple `#7C3AED`. Secondary green `#22C55E` appears **only in the logo**. No orange anywhere.

## 2. Repository map

```
main.go              wiring: config → DB → engines → hub → routes → mesh/mDNS → DRM → OSC
config/              config.json load/save (atomic, 0600, env overrides)
timerpi/             domain + SQLite schema/migrations + cue engine + schedule + polls + merge
ws/                  WebSocket hub: sessions, per-show fanout, audience lane, commands
routes/              every HTTP route (pages, REST, auth, screens, boards, audience, zone, super, OSC)
boards/              layout ("board") model, widget registry, built-in templates
views/               view-model structs + formatting helpers for templates
importdocs/          XLSX/CSV/JSON running-order import + example files
mesh/  mdns/         device mesh (primary election, takeover) + mDNS announce/browse
oscbridge/           OSC codec + UDP listener + outbound hooks (CuTePi/QLab)
drm/                 native KMS/fbdev countdown renderer for the Pi's HDMI
templates/           pages + fragments (*.html)
public/              css/, src/ (JS modules), img/
third_party/ftl-themes/   theme bundles (git submodule — see §9)
scripts/ systemd/    install, update, backup, restore, healthcheck, splash
docs/                product, architecture, runbooks; docs/archive/ = historical notes
```

## 3. Domain model

**A room is a show.** Every show has a unique 8-character share code in a 4-4
format (`K7QP-M3XB`). The alphabet is Crockford base32 without I, L, O or U,
and input is typo-tolerant. Codes are the only public address; numeric IDs
return 404 everywhere.

SQLite tables (in `timerpi/db.go` unless noted):

| Table | Holds |
|---|---|
| `shows` | title, share code, `zone` label, notes, `day_start` (HH:MM), passphrase hash, `blanked` |
| `cues` | sessions: pos, label, duration, kind/break, tags, speaker, hold, timer kind (COUNTDOWN/COUNTSTOP/CLOCK), alerts 1/2 + colours, end action (HOLD/OVERTIME/BLANK), autocontinue, `start_at`, notes, colour, `updated_at` |
| `messages` | stage messages per show (`shown_at` 0 = hidden) |
| `runtime_state` | engine state per show: active/prev/next pos, anchor, rate, paused elapsed, day start |
| `settings` | key/value appliance settings |
| `polls` | interactions: kind, question, options, correct, state, **parent** (child rows = submitted questions/words), author token hash |
| `votes` | (poll, peer, choice), unique per peer |
| `screens` | per-show screen registry: name, theme, board id, room label, last seen |
| `waiting_screens` | uncaptured `/d/` screens waiting for an operator |
| `display_boards` (`boards/`) | layouts: name + JSON layout of tiles |
| `display_presets` | named bundles of per-screen assignments (export/import as JSON) |
| `assets` | uploaded blobs (venue maps), sniffed MIME, 4 MiB cap |
| `actions` | operator action log (200 per show) |
| `client_errors` | browser error reports (200 per show) |
| `mesh_state` (`mesh/`) | device mesh identity |

**Zones.** `shows.zone` is a free-text label. All shows with the same zone
appear on `/zone/<name>`. The zone's map is an `assets` row referenced from
settings.

## 4. The cue engine

`timerpi/engine.go` is one `*Engine` per show, created lazily by the `Engines` registry.

- It is a pure state machine: start, pause, resume, reset, go, next, prev, jump, rate, daystart, blank.
- A server-wide ticker (250 ms) handles zero crossings, alert edges, auto-continue and wall-clock auto-start. It emits **only on state change**.
- **Digits are never server-ticked.** Snapshots carry `anchorTS`, `rate`, `pausedElapsedMS` and `serverTime`. Every client computes `remaining = duration − ((now − anchor) × rate + pausedElapsed)` from its own clock, corrected by the server offset. `engine.js` mirrors `timerpi.DisplayedRemaining`.
- A rate change re-anchors the countdown and never alters the planned schedule.
- `ComputeSchedule` / `ComputeScheduleRuntime` give planned and actual start/end and over/under for every row.
- The `OnStart` hook fires on every cue start (hand GO, auto-continue, scheduled start). The OSC bridge uses it.

## 5. Realtime: the WS hub

`ws/hub.go` and `ws/session.go` serve one endpoint, `/ws`. The first frame is a
`join` with a role and a show code.

| Role | Bucket | Receives | Can send commands |
|---|---|---|---|
| `controls` (operator) | show bucket (cap 512/show) | full snapshots, oob fragments, schedule, poll, screens | yes (needs operator token if a device password is set) |
| `display` / `screen` (stage, variants, boards) | show bucket | snapshots, poll, theme/board pushes | no (read-only) |
| `audience` (phones) | **separate audience bucket** (cap 4000/show) | `poll` frames only, never cue state | never |

- The fanout is serialised per show.
- Votes do **not** trigger a snapshot fanout. `Hub.BroadcastPoll` sends a small poll-only frame (~150 B) to the audience bucket and the boards.
- Votes and asks go over REST with rate limits: 300 ms per peer per vote, 3 s per peer per ask, and a 600 votes/s per-show soak limit that answers 429 with Retry-After.
- Load harness `ws/load_test.go` (run with `TP_LOAD=1`): 1,000 phones, 1,000/1,000 join storm, p95 broadcast 24 ms in the dev container. **Not yet measured on a Pi.**

## 6. Screens, layouts and templates

- **Screen identity.** Precedence: the `?screen=<name>` URL parameter, then per-window `sessionStorage`, then a generated `Screen-XXXX`. The name rides the WS join, which upserts the `screens` row. The hub pushes the stored theme/board assignment back immediately.
- **Waiting room.** `/d/` (no code) shows a READY card and registers in `waiting_screens`. An operator **captures** it from `/screens/<code>`, choosing name, theme, location and template. The screen then hops to its room. The claim is atomic and consumed once.
- **Layouts ("boards")** live in `boards/`. A 12-column grid holds at most 48 tiles. **17 widget types**: countdown, cuelabel, speaker, nextup, wallclock, progress, dayprogress, messages, showtitle, rate, schedule, notice, poll, qa, wordcloud, map, joinqr.
- **Templates** (`boards.TemplateLayouts`, the single source; served at `GET /api/board-templates`):

| Template | Tiles | Serves PRODUCT surface |
|---|---|---|
| stage | countdown, messages, cuelabel, nextup, progress, dayprogress, wallclock, rate, showtitle | default timer |
| lobby | showtitle, wallclock, messages, schedule | — |
| event | showtitle, map, schedule, wallclock, notice | #1 (single-room only; see STATUS N7) |
| room | showtitle, wallclock, cuelabel, speaker, nextup, schedule, joinqr, notice | #2/#3 |
| main | showtitle, poll, qa, wordcloud, joinqr, notice | #4/#5 |
| dsm | countdown, poll, progress, cuelabel, speaker, nextup, wallclock, joinqr, dayprogress | #6/#7 |
| speaker, qawall, clockroom, break | fillers | — |

- **Steering.** A named screen with an assigned board that loads the bare stage URL is redirected (302) to its board. Live reassignment arrives as a `screen-board` frame.
- **Editing.** The layout editor runs only inside the operator's `/screens/` preview iframe (`?edit=1&preview=1`, auth-gated). Real screens never render chrome.
- **Display page variants.** `/d/<code>?view=stage|next|daysheet|clock|board`.

## 7. Audience interactions

`timerpi/polls.go`:

- **Kinds:** `poll`, `quiz`, `qa`, `wordcloud`, `ideas`, `survey`. (`survey` exists in the data layer only.)
- **States:** `hidden → open → results`, plus back to `hidden`. New items are always created `hidden`.
- **Single focus:** opening a top-level item hides every other top-level item in that show. Child rows (submitted questions/words) are approved independently and accumulate.
- **Visibility contract:** `AudienceRead` returns only what is on air. Hidden items are **absent** from the payload, not hidden client-side.
- **Surfaces:**
  - Phones: `/a/<code>`. WS first, with REST fallback.
  - Boards: poll, qa and wordcloud tiles, which render bars with counts and %, the question wall, and word tiles.
  - Operator: the dashboard Audience panel (create, Show/Results/Hide/Delete, moderation queue).

## 8. Access control

Middleware order: no-cache → recovery → body cap (8 MiB, 32 MiB on imports) → **OriginGuard** → **AuthGate**.

| Layer | Mechanism |
|---|---|
| OriginGuard | Open by default (any Host). A non-empty `allowed_hosts` setting switches to strict mode (HTTP 421 for unknown public hosts). The WS upgrader calls `routes.SameOriginRequest` |
| Device password (`config.AuthPassword`) | `AuthGate`: cookie `tp_auth` or Basic `operator:<pw>`. Exempt: `/health`, login, `/d/*`, `/ws`, static, `/assets/*`, the waiting-room register/mine calls, and the show QR and client-log calls. **Holding it = SuperOperator** |
| Room password (show passphrase) | Optional per show. Cookie `tp_show_<CODE>`. Gates `/c/`, `/d/`, `/a/`, show-scoped REST and WS joins |
| Audience | No login. A device token is minted client-side and only its hash is stored |

**Known gaps** (tracked in STATUS): `/a/` and `/zone/` are not AuthGate-exempt
(B1). With no device password set, everyone is effectively SuperOperator
(documented LAN-trust model).

## 9. Themes (ftl-themes)

- `third_party/ftl-themes` is a **git submodule** of `github.com/DrEVILish/ftl-themes`, pinned to an upstream commit. Clone with `--recursive` (or run `git submodule update --init`).
- **Default theme `blue-future`.** A custom TimerPi theme will be designed later. An earlier local `timerpi` theme is shelved; it is kept only on the branch `timerpi-theme-local` inside the old dev box's clone.
- **Default theme:** `blue-future` (`config.DefaultTheme`), changeable at `/settings` or `POST /api/theme`. Every page server-renders `html[data-theme]` and the default bundle link. `theme.js` applies a browser-local pick (`localStorage timerpi.theme`) or an operator-pushed per-screen theme, and retargets icon sprites to `/ftl/dist/icons/<theme>.svg`.
- **Rule:** new UI uses ftl-themes component classes and tokens. `timerpi.css` carries layout only.

## 10. Resilience

- **Browser P2P mesh** (`public/src/mesh.js`): WebRTC data channels between the browsers of one show. If the server dies, the earliest-joined browser becomes master, keeps the timer running, and can even edit the running order (tombstoned deletes). On reconnect it pushes `POST /api/shows/:code/sync`, which merges per cue with last-writer-wins. Spec: [OFFLINE-EDIT.md](OFFLINE-EDIT.md).
- **Device mesh** (`mesh/`, `mdns/`): TimerPi boxes discover each other. The first one up is primary; after 8 s of silence another takes over.
- **Reconnect:** 6 s handshake watchdog and instant rejoin on `online`/`visibilitychange`.

## 11. Integrations

- **OSC / CuTePi** (`oscbridge/`, `/settings`):
  - Inbound UDP `/timerpi/<code>/<verb>`. This is **unauthenticated** by design, for LAN control desks.
  - Outbound: every cue start sends `/cue/<pos>/start`, BLANK on sends `/panic`, BLANK off sends `/go`, all to the configured host:port.
- **Remote control (Companion / Stream Deck):** `POST /api/shows/:code/cmd/:action`.

## 12. Static assets and cache-busting

`Cache-Control: no-cache` is sent on everything. Templates link JS/CSS
through the `asset` template func (`views/assets.go`):
`{{asset "/src/timerpi.js"}}` becomes `/src/v<rev>/timerpi.js`. `<rev>` is a
hash of `public/src` + `public/css`, computed at startup. `registerStatic`
strips the `/v<rev>/` segment. The revision is a path segment, so relative
ES-module imports (`./mesh.js`) inherit it, and query-ignoring caches still
see new URLs after an update. There are no versioned file copies and no bump
script. Restart the server after JS/CSS edits.

## 13. Deployment shape

| Target | How |
|---|---|
| Dev | `make run`. Port 8080, data in `./data` |
| Linux server / Pi | `make build` (or `build-arm64`), `timerpi.service`. Data in `/var/lib/timerpi`. Port via `TIMERPI_HTTP_PORT` |
| Screens | Any browser in kiosk mode pointed at `/d/`. The Pi's own HDMI can instead run the native DRM countdown (`TIMERPI_DISPLAY`) |

Runbooks: [OPS.md](OPS.md), [PI-DEPLOY.md](PI-DEPLOY.md), [HW-DRILLS.md](HW-DRILLS.md).

## 14. Target shape: the event model (PRODUCT §3, STATUS §1)

The direction for STATUS N1–N10, so new code lands in the right place. These
are not built yet.

**Data**

```
events      id, code (8-char), name, supervisor_pw_hash, theme, map_asset_id, created_at
event_days  id, event_id, date, pos              -- v2: exactly one row per event
shows       + event_id, + room_name, + room_pos  -- a show IS a room; keeps its own code
            passphrase → the room's moderator password (set by the SuperOperator)
cues        + day_id (defaults to the event's only day)
polls       + to_audience, + to_presenter (replace single-focus 'open')
            child rows: + status pending|approved|answered|dismissed (Q&A wall)
screens     + type audience|walkin|presenter, + rotation 0|90|180|270,
            + event_id (event-level walk-ins have no room)
```

**Addresses**

| Address | Opens |
|---|---|
| Event code | The SuperOperator and moderator entry (`/e/<event code>`, then pick a room) |
| Room code | The short machine address for QR and screen URLs (`/a/<room>`, `/d/<room>`). It is never typed by people |

**Auth**

| Role | Gate |
|---|---|
| SuperOperator | Event-scoped session from the supervisor password. Grants every room of that event, including content |
| Moderator | Room-scoped session from the event code + room pick + optional moderator password |
| Appliance password | **Removed.** `/settings` needs any event's supervisor session; it is open while no events exist |

**Hub**

- The per-show buckets stay as they are.
- Walk-in event screens subscribe to an **event bucket** that receives a light per-room summary frame (`rooms`: current/next per room) on any room's state change. Full snapshots never go to the event bucket.
- Poll frames are filtered by target: audience bucket and audience screens get `to_audience` items; presenter screens get `to_presenter` items.

**Migration**

- Each existing show becomes a one-room event.
- Its zone label becomes the event name when several shows share a zone; those shows merge into one event.
- Zone maps become event maps.
- Existing v2 room bundles keep importing as a one-room event.
