# TimerPi — Plan (living document, source of truth in this repo)

> **PLAN MAJOR v2** (`2026-10-05`): the Rooms build — multi-room event
> platform (§11 below). Product major version goes **1 → 2** with it; the
> Go-side `appVersion` const ships with Phase 7.
>
> Last rolled up 2026-10-03. Service live on 0.0.0.0:80 via `timerpi.service`.
> Status key: ✅ shipped · 🟡 code-complete, hardware-unproven · ⏳ next.

> Cue-list show timer for Raspberry Pi. One cue list for the whole day, run as
> a web app; the Pi displays it on HDMI and multiple TimerPi devices / browsers mesh over LAN.
>
> Stack standard: **Go 1.25 + gin + gorilla/websocket + sqlx +
> mattn/go-sqlite3 + skip2/go-qrcode**, server-rendered **html/template +
> htmx, use htmax.js version 4** fragments (vendored, no CDN), styling **imported as-is from
> ftl-themes** (git submodule). No client framework; vanilla JS only where
> htmx can't reach (smooth countdown rendering, fullscreen, import dialogs).
>
> **Branding: Primary Purple `#7C3AED`, Secondary Green `#22C55E`.
>
> **Identity: shows addressed by 8-char alphanumeric 4-4 codes** (`K7QP-M3XB`,
> Crockford-base32 minus I/L/O/U, typo-tolerant input). Numeric IDs are
> internal-only and never resolve as addresses.

---

## 1. Core product decisions

| Decision | Choice |
|---|---|
| Project | **TimerPi** (code lives in `/opt/timerpi`, module `timerpi`) |
| Server | Go, single binary serving HTTP + WS on **one port**, listening on **0.0.0.0** (default 80; 443 via user-managed reverse proxy) |
| Realtime | WS hub, server-authoritative state; snapshots carry `updatedAt`, newer wins |
| Client | htmx fragments for structure & mutations; **local browser clock** renders the digits so the display is perfectly smooth |
| Time rate | Operator-controllable **rate multiplier** (e.g. ×0.5–×2.0, "1 s per second"); a rate change re-anchors the countdown so on-screen time speeds up without shifting the underlying schedule |
| Storage | **SQLite** (`sqlx` + mattn/go-sqlite3, CGO) |
| Themes | ftl-themes submodule (`third_party/ftl-themes`), served at `/ftl/`, browser-local theme pick (localStorage), tokens/contract v4 |
| Identity | mDNS advertises the machine's **hostname** (`<hostname>.local`); hostname editable in the WebUI (renames host + re-announces); multiple TimerPi devices may share one LAN |
| Mesh | Each TimerPi can be **server, display, or join another server**; devices discover each other over mDNS and mesh directly. **The first TimerPi up on a network holds authority (primary).** Browsers also keep the P2P WebRTC display mesh from the previous build |
| Display | **Custom framebuffer/DRM renderer** — the Pi drives its HDMI connector directly for a rock-solid 1080p50 timer |
| Splash | **Framebuffer splash service**: logo on `/dev/fb0` until the app is ready; no extra packages |
| Target HW | Pi 4/5, 64-bit Raspberry Pi OS bookworm; no X11/Wayland needed |
| Data | SQLite is the single source of truth (shows, cue lists, messages, settings) |

## 2. Domain model (SQLite)

A show is one day's running order; each row is a timed session.

```
shows:    id, title, created_at, updated_at
cues:     id, show_id (FK), pos (stable order), label, duration_ms, kind,
          tags (VT/GFX/COM/CAM/PRES…), speaker,
          hold_ms (explicit changeover/buffer time on purpose),
          break_flag (break / changeover / host slot),
          notes, color,
          timer_kind (COUNTDOWN | COUNTSTOP | CLOCK),
          alert1_ms, alert2_ms,          -- per-cue alert thresholds
          alert_color1, alert_color2,    -- display colour states at thresholds
          end_action (HOLD | OVERTIME | BLANK),
          autocontinue
messages: id, text, color, shown_at       -- stage overlay ("PLEASE WRAP UP!")
settings: key/value (display layout, theme, hostname)
runtime:  active/previous/next cue pos, per-cue progress, rate, day-bar anchors
```

- No implicit advance beyond per-cue `autocontinue` — a cue runs out, holds, and the operator или GOs the next. Changeover/break/host rows are ordinary cues with `break_flag`, so margin is added on purpose.
- Schedule is computed from sequence + durations + holds, updating against wall-clock as the day runs: computed start, resulting end, over/under, and the running **day bar** (0:00 → total).

## 3. Architecture & repo layout (mirrors CuTePi patterns)

```
config/config.go     load/save (~/timerpi/config/config.json, env-overridable, atomic, 0600)
timerpi/             domain types, SQLite schema + migrations (db.go), engine.go (cue engine)
                     engine = pure state machine: start/pause/reset/next/go, alerts, rate;
                     snapshot + pubsub; current/next cue, progress bar, message overlay
ws/hub.go            WebSocket hub: join {show, role} → current snapshot; commands in; OOB html out
routes/index.go      pages & fragments (dashboard, cue list, display, share)
routes/api.go        cue/show CRUD, import/export, settings, WS upgrade (gin → gorilla)
routes/import.go     XLSX/XLS/CSV/JSON import; example-document download
routes/network.go    mDNS announce/detect, hostname change, device-mesh registry
routes/origin.go     allowed_hosts guard (HTTP 421 on unknown public Host — DNS-rebind protection)
routes/qr.go         QR for the share/open-display links
mesh/                browser P2P (WebRTC signaling over the Go WS) + device mesh protocol
drm/                 framebuffer/KMS display renderer (1080p50) + splash handoff
templates/*.html     dashboard, cuesheet, cueinspector, display, share, importmodal, …
public/              css/, src/ (htmx vendored), img/ (TimerPi logo), fonts/
third_party/ftl-themes/  git submodule — theme bundles & tokens
main.go              wiring
```

**WS frames (server→client)**: `state` (full snapshot), `timer` (countdown anchor + rate), `cue` (current/next change), `oob` (htmx fragment swaps), `schedule` (recomputed schedule), `message` (stage overlay), `display` (layout/theme change).
**Client→server**: `start / pause / reset / go / next / prev / jump`, `cue.*` CRUD, `import`, `settings`, `display_cfg`, `rate`, `message.show/clear`, `mesh ops`.

## 4. UI plans

- **Dashboard/operator** (any browser on the LAN, matching the reference shots): left panel — current clock with progress bar, next-up, speaker, running clock; centre — the cue list (running order) with duration, computed start, tags, per-row transport; header — show title, import/export, wrap-up/panic controls; live-connections counter; bottom — day progress bar.
- **Display page** (`/d/<show>`): fullscreen current timer, overtime state, next-up strip, message overlay, themed by ftl-themes; multiple independent displays.
- **Share**: QR + 4-4 format code link on the control page for displays/phones.

## 5. Mesh & roles (protocol)

- **Server-authoritative**: operator taps mutate state; WS fans out snapshots. Conflict rule shared across all layers: newer `updatedAt` wins.
- **Device mesh**: mDNS `_timerpi._tcp` TXT records: hostname, role (`primary` / `display` / `idle`), show id, epoch. First device up = primary; others join as members (apply snapshots locally, serve their own HDMI displays or browser pages). If the primary goes stale (>8 s without presence), the next device promotes itself with the last known snapshot.
- **Browser mesh**: as built for the previous web app — WebRTC data channels, master = earliest `joinedAt`, offline commands execute locally at the master, master pushes state back on reconnect via `POST /api/session/:code/sync`-equivalent (last-writer-wins).
- **Clock sync**: every snapshot carries `serverTime`; clients re-anchor their offset on receipt, so countdowns stay aligned across devices without NTP dependency.

## 6. Phase plan (all phases landed; notes are the delta)

0. **Scaffold** ✅ — as planned + open-by-default host guard (custom domains/reverse proxy work; strict `allowed_hosts` opt-in) + kiosk-window display links (`data-kiosk` → new window, not tab).
1. **Domain + engine** ✅ — as planned; per-cue `updatedAt` now on the wire for merge.
2. **Server + templates I** ✅ — as planned + `schedule` WS frame on every structural change + `/settings` identity page + `/setup` first-run wizard with printable QR connect sheet.
3. **Display + share** ✅ — stage + `?view=next|daysheet|clock` variants + `?view=board` **widget board** (11 draggable widgets, per-show layouts in SQLite, edit-lock toggle) + QR + alphanumeric codes + show-file JSON export/import.
4. **Device mesh** ✅ code, 🟡 two-Pi drill pending — discovery/claim/takeover/hostname-edit live; single-host drill exposed and fixed 3 same-hostname identity bugs (mdns dedupe + exclusion, seniority); live drill: member-yield, 8s takeover, seniority reclaim all PASS.
5. **Import/export** ✅ — as planned + show-file JSON bundles.
6. **Output renderer** ✅ code, 🟡 hardware run pending — `drm/` KMS 1080p50 + fbdev fallback wired behind `TIMERPI_DISPLAY`, 25 tests pass; needs `TIMERPI_HW_TEST=1` on a Pi with HDMI.
7. **Install hardening** ✅ code, 🟡 Pi run pending — units, `install-pi.sh` (idempotent, incl. 1080p50 cmdline pin), `update/backup/restore/healthcheck` scripts with gates, OPS + PI-DEPLOY runbooks, arm64 binaries building.

## 9. Offline editing (shipped post-plan, owner decision)

Operators may rewrite the running order while dark. Offline master executes cue add/edit/delete/move locally (stamped, tombstoned deletes capped at 200); reconnect merges per-cue LWW via `MergeCues` with dense renumber; operator gets a "Merged N remote change(s)" toast. Accepted caveat: concurrent reorders on both sides may interleave; server full-replace racing an offline editor duplicates loudly. Spec: `docs/OFFLINE-EDIT.md`.

## 7. Compatibility notes

- Rate multiplier: the engine anchors each running cue at its wall-clock start; displayed time = elapsed × rate; changing rate re-anchors — official schedule unaffected, display visibly speeds.
- Hostname change updates the system hostname and re-publishes mDNS, so `<newname>.local` works immediately.

## 8. Risks / open points

1. **Cross-compile**: mattn/go-sqlite3 needs CGO → build on the Pi or use an `aarch64-linux-gnu-gcc` cross toolchain in this container (recommended; the Pi needs no toolchain).
2. **Legacy XLS import**: best-effort (tealeg/xlsx); recommend users re-save as XLSX/CSV. XLSX is the supported path in v1.
3. **1080p50 mode persistence**: display mode comes from the kernel cmdline at boot; runtime modesetting via UI is a later phase.
7. **ftl `timerpi` theme**: built 2026-10-04 (see PROJECT D1) — lint/budgets green; rendered/axe gates need Playwright on a machine with a real browser; product default remains blue-future.
4. **Split-brain**: transport/message state resolves LWW by `updatedAt`; cue-list merges per-cue LWW with tombstones (see §9); concurrent reorders may interleave — documented, accepted.
5. **Hotspot/offline first-boot**: device needs network to be reachable; a first-boot fallback (e.g. USB gadget or DHCP retry UI) is out of scope for v1 unless requested.
6. **TimerPi ftl-theme spec** (`docs/TIMERPI-THEME-SPEC.md`) handed to the ftl-themes developer; UI supervised pass complete (coherence rules in `reviews/SUPERVISOR-report.md`).

## 10. What's next (owner-ordered)

1. Two-Pi LAN mesh drill on real hardware (single-host drill passed).
2. Pi hardware deploy (flash → splash → 1080p50 → mDNS → `TIMERPI_HW_TEST=1`).
3. ~~ftl `timerpi` theme build-out~~ ✅ built 2026-10-04 (PROJECT D1).

> Theme bundle note (2026-10-04): the `timerpi` ftl theme ships out of
> `third_party/ftl-themes/themes/timerpi/` (theme.css, README, icons.svg —
> TV-legibility redraws of pause/stop/skip/microphone/warning). Submodule
> commits: `f605048` (theme + dist), `ae648e4` (index row). The outer repo
> must bump the submodule pointer once its own git history exists.

---

## 11. PLAN MAJOR v2 — Rooms: multi-room event platform

Owner brief (paraphrased): one appliance runs a whole event — walk-in lobby,
per-room walk-in + main + DSM displays, per-room operators, a SuperOperator,
and a moderated Slido-style audience surface (polls, Q&A, word clouds) joined
by QR, scaled for 1000+ phones pushing at once. Everything ftl-themes styled,
with operator-customizable appear/disappear animations. CuTePi evaluated as a
destination display. No. 3 (zones) + No. 8 (OSC only) from the 10-proposal
round are in scope.

### 11.0 What already exists in this repo (discovered 2026-10-05)

Committed / long-shipped:

| Facility | Where | Notes |
|---|---|---|
| Show-level cue engine + WS hub, per-show fanout | `timerpi/engine.go`, `ws/hub.go` (`byShow`) | the room model rides this |
| Widget-layout engine (drag, geometry, 49/0-widget layouts) | `boards/` package | display templates extend it |
| Waiting room: register/capture (consume-once), orphan overlay | `routes/waiting.go`, `public/src/waiting.js`, mesh badshow state | `/d/` no-code reuses this |
| Show auth + device operator password | `config.AuthPassword`, `routes/auth.go` | role ladder below |
| Device mesh (mDNS, roles, offline mirror) | `routes/network.go`, `public/src/mesh.js` | CuTePi pairing extends it |
| ComputeSchedule + runtime variant (day plan) | `timerpi/engine.go` (tested) | walk-in schedule slot source |
| QR generator | `skip2/go-qrcode` | audience join reuses it |
| `display_presets` (F2 named screen presets) | `timerpi/db.go` | layout templates reuse this table |

**In-flight WIP, uncommitted** (parallel session; currently does NOT
compile — treat as raw material, stitch before building on it):

| Facility | State |
|---|---|
| `timerpi/polls.go` — one-table interaction engine: 6 kinds (`poll`, `qa`, `wordcloud`, `ideas`, `quiz`, +1), state machine `hidden → open → results`, `Vote` upsert per (poll,peer,choice), moderated `Submit` (questions/words are rows), `PollCounts` → `PollView` (counts + %), `ActivePoll`, `AudienceRead`, `ListOpenSurvey` | solid; tests exist? **suite status unknown** — write missing unit tests in Phase 0 |
| `routes/audience.go` — REST surface (`/api/audience/read|ask|vote|qr`, poll CRUD) + `/a/:code` audience page | page renders `audience.html` which **does not exist yet** → 500; ask rate-limit exists (`askAllowed`) |
| `routes/zone.go` + `shows.zone` label + `/zone/<name>` walk-in event page (wall clock, one card per room: current session + next time + full day schedule; server-rendered soft refresh, no WS) | works for labels; lacks per-room walk-in variants, map asset, layout control |
| `oscbridge/` — pure-Go OSC codec (parse + build, no deps), UDP inbound listener → engine verbs, outward `FireOut` hooks ("go"/"panic" per oscapi) | codec compact; **build broken**: (a) `routes/api.go:463,465` references `oscbridge` without import, (b) `routes/oscapi.go:47` passes `log.Printf` where the `SetInbound` error callback signature is expected, (c) `routes/oscapi.go:60` `undefined: timerpi` (missing import) |
| `ws/hub.go` `pollsFn` — active poll merged into EVERY fanout frame | works; capacity problem at audience scale (§11.5) |
| Board/inspector poll client rendering | thin: 1 grep hit in `board.js`, missing template hooks — Phase 5 |

### 11.1 Surface map (the owner's 11 items → mechanism)

| # | Surface | Mechanism | Status |
|---|---|---|---|
| 1 | Event walk-in (wall clock, sessions + rooms, space map, day schedule) | `/zone/<name>` aggregate, slot layout §11.2, map slot | 🟡 labels exist; page needs template slot system |
| 2/3 | Room A/B walk-in (clock, current + next-in-room, schedule) | own show per room + walk-in template | ⏳ |
| 4/5 | Room main display (polls/questions/results on demand) | board page + `pollsFn` frame + results renderer | 🟡 data layer there; display layer Phase 5 |
| 6/7 | Room DSM/timer display (progress + polls wedge) | standard timer board + poll slot | 🟡 |
| 8/9 | Room operators | show-scoped operator (code + optional show passphrase) | ✅ exists; panel polish Phase 3 |
| 10 | SuperOperator (all rooms) | device `AuthPassword` role + cross-room panel: engine registry keys every show; SuperOps may fire every room's verbs; zero-UI "all rooms" list | 🟡 |
| 11 | Audience phones via QR, hidden until pushed | `/a/:code` + QR + moderation states incl. `AudienceRead` visibility contract | 🟡 REST there; capacity lane §11.5, page §11.4 |

Role ladder (fixed v2): `screen | display` (no auth) → `audience` (QR, read
+ one vote/ask per peer, peer = device token) → `operator` (per-show; one
password per show, off = open like today) → `super` (device password;
cross-room verbs + settings). Room operators never see other rooms' pages —
enforced at route layer (`showGateByShowID` already gates, extend to zones).

### 11.2 Display templates (slot system)

One slot schema, rendered by the same board renderer that already does
widget layouts — no second layout engine:

- Template = named preset in `display_presets`, kind `layout`, payload:
  `{template: "event"|"room"|"main"|"dsm"|"timer", slots: [{slot, enabled, size, animation}], zone, mapAsset}`.
- Slots: `clock` (server wall clock), `session` (current session card),
  `next` (next-in-room), `schedule` (day plan via ComputeSchedule; event page
  aggregates all shows in the zone), `timer` (cue countdown, DSM), `poll`
  (question/results showcase), `qa` (moderated question wall), `wordcloud`,
  `map` (operator-uploaded image asset per zone), `logo`, `blank`.
- Templates: `event` (1, event lobby), `room` (2/3 walk-in), `main` (4/5),
  `dsm` (6/7, timer + poll wedge), `timer` (today's board, unchanged default).
- Animation enum per slot + per push: `none | fade | slide | pop` (+ fade-ms
  knob) — implemented with the ftl-themes live/data-state component classes
  (upstream live.css vocabulary); verify tokens against CONTRACT-UI appendix
  as in the audit. Operator sets the enum in the inspector like any widget
  field. All new elements: ftl component vocabulary only (rules theming the
  build, no ad-hoc CSS outside app layout).
- Zone `mapAsset` upload joins the existing asset pipeline (screens gallery
  preview pattern); render `object-fit: contain`.

### 11.3 Phases (each lands with tests; asset rev bumps as UI ships)

0. **Stitch the in-flight WIP** — ✅ 2026-10-05: fixes went deeper than
   the 3 listed build breaks: the WIP's OSC codec was wire-broken
   (under-padded strings, missing tag terminator — Build/Parse rewritten
   with a shared `oscString` framer), the three route groups were never
   mounted, `hub.SetPollsFunc`/`Engines.OnStart`/`oscbridge.Target` were
   never wired in main, and the audience REST lane bypassed the
   show-passphrase gate its own comment promised (fixed once in
   `resolveAudienceCode`). `audience.html` ships as the working REST-lane
   stub (rev v51). Tests: `timerpi/polls_test.go` (lifecycle), 
   `oscbridge/oscbridge_test.go` (codec + real UDP listener), 
   `routes/audience_test.go` (page + gate + flow). Suite: 12 pkgs green,
   `-race` clean (took two pre-existing flakes with it: mdns
   `collectEntries` data race, drm test window).
1. **`/d/` no-code display + home button** — ✅ 2026-10-05: `GET /d/`
   (gin static sibling of `/d/:ident`; bare `/d` 301s) renders the READY
   overlay — the page IS the waiting overlay (`body[data-waiting]`
   server-side, same CSS gate as the blackout) — and `waiting.js` gained
   `runWaiting()`: the same register/poll/hop loop orphaned displays run
   after badshow, started without a mesh. Identity rules are the mesh's
   own (`?screen=` > sessionStorage `tp.screen` > generated `Screen-XXXX`)
   — several `/d/` tabs on one host stay separate rows. Home page gained
   the "Open as display" panel (a plain `target="_blank"` anchor — no JS).
   Tests: `routes/dready_test.go` (page, redirect, home button, capture
   loop register→capture→mine consume-once); rev v52.
2. **Display templates** — ✅ 2026-10-05: implemented AS the plan intended
   — slots are board widgets, no second renderer. New widget types
   `poll | qa | wordcloud | map | joinqr` join the registry (palette +
   PUT validation + fragments + board.js renderers); the existing
   countdown/cuelabel/speaker/nextup/wallclock/schedule/notice types
   already covered the timer/clock/session/next/schedule/logo slots.
   Rooms templates `event | room | main | dsm` live in Go
   (`boards.TemplateLayouts`, overlap-tested, single source of truth)
   served at GET /api/board-templates and applied from the board chrome.
   Venue maps: `assets` blob table + POST/DELETE /api/assets (sniffed
   mime, 4 MiB cap) + public GET /assets/:id (AuthGate-exempt — a floor
   plan is not a credential) + zone pointer (POST /api/zone-map) rendered
   on the walk-in page. Word clouds: approved child words ride
   `PollView.children` (loudest first). **Capture modal (owner round)**:
   "Capture to this show" opens a modal — Name (prefilled with the next
   free sequential `Screen N`), Theme (operator default = the appliance
   theme), Location/room, Layout (board) — the config lands on the
   screens registry BEFORE the display hops, the poll reply carries the
   adopted name, and the claimed display leaves the waiting list
   immediately (consume-on-claim deletes the row; a later poll
   re-registers fresh, which is the display re-appearing — correct).
   Poll cadence 4 s → 2 s plus front-loaded gallery refreshes make
   capture→visible ~2-3 s. The waiting-list flow also gained the ftl
   dialog helpers (tpConfirm/tpPrompt) replacing browser confirm/prompt
   everywhere, and `/d/` shows "This screen is <name>" bold + highlighted.
Animation enum per tile
   (none|fade|slide|pop + animMS) with entrance AND exit keyframes —
   displays always animate per the owner's scoping. Two semantics fixed
   en route: word approval ACCUMULATES (single-focus stays top-level
   only — the flat rule made clouds impossible) and ActivePoll focus is
   top-level only (an approved word must not steal the screen).
3. **Role ladder + SuperOperator** — ✅ 2026-10-05: the ladder stands as
   §11.1 (display → audience → room operator (show passphrase, shipped) →
   super = device-password session past AuthGate). New: `GET /super`
   cross-room panel (live per-room cards: state, active label, remaining,
   blanked, connected count) polling `GET /api/super/rooms?zone=`;
   `POST /api/super/verb` (transport + blackout verbs only — content stays
   room-owned) and `POST /api/super/bulk` (blank/unblank/go/next/pause/
   resume across all rooms or one zone, per-room outcomes). Zone filter
   landed where it is operationally meaningful: the panel's room list and
   bulk scope. Waiting rows are ORPHANED (no show → no zone), so a zone
   filter there would be fiction; capture stays show-scoped in the
   gallery. Note: on an open appliance (no device password) the panel is
   reachable — the same documented LAN-trust model as every operator
   surface.
4. **Audience capacity lane (1000+)** — §11.5. Audience WS endpoint,
   scope-filtered frames, vote storms, load harness.
5. **Interaction UX on displays** — results bar graphs (`ProgressBar`
   ftl component rows: label, filled bar, `% of total`, total-votes line),
   question wall (moderated list, show/hide), word cloud (DOM tiles sized by
   votes, themeable badge/tag vocabulary), operator transport: PER-CARD
   `Ask | Show | Results | Hide` buttons wired to the existing state machine;
   audience page sections appear/hide with the slot animation; quiz flow
   kept as last kind (right/wrong reveal = results state reuse).
6. **CuTePi destination bridge** — §11.6 (finish the otrientation the WIP
   started; outbound fire on GO/BLANK; pairing UI in Settings).
7. **Hardening + version const** — `appVersion = "2.0"` in Go (health +
   footer), README/PROJECT bump, load-harness numbers recorded, review pass,
   `-race` suite, deploy to the Pi.

Build order is deliberate: 0/1 are small and ship value alone; 5 depends on
4's lane; 6 is independent of 2–5 (reorder safe).

### 11.4 Audience page (`/a/:code`)

Pages stay zero-login: QR → join (device token minted client-side,
persisted localStorage; server stores no PII, only the token hash for vote
dedupe + rate keys). Sections render **only** what the room operator has
pushed (`AudienceRead` visibility contract): an open poll shows the question
+ options (tap to vote, one vote per peer, switchable until results), an open
qa shows the ask form + (optionally) own submitted question ("you asked ·
pending"), wordcloud/ideas show the input chip. Hide = section collapses
with the slot animation, NOT bare removal, so phones can't infer an operator
action by a layout pop if animation is `none` — §11.5 payload drops the
section entirely instead (hidden by absence, the load-bearing design rule).

### 11.5 Capacity budget (the 1000-phone problem)

Constraints measured/planned, not vibes: hub currently merges the active
poll into EVERY full-show frame (`pollsFn` in the fanout loop) and caps
sessions at `maxShowSessions = 512`/show.

- **Lane separation.** Audience peers join a dedicated WS endpoint and a
  dedicated session class (`audByShow[showID]`), leaving the 512 cap for
  boards/operators untouched (its purpose was runaway openers, not phones).
  Audience default cap 4,000/show (config knob), one readLoop each —
  ~2,000 goroutines ≈ fine on the Pi; memory ≈ frames × sizes below.
- **Payload scoping.** Audience frames never carry cue state. Body is the
  visible-interaction object only (§11.4): open poll + question counts +
  word top list — ≲ 1 KB, vs the full Snapshot (KBs). Frame format: the
  existing `{v:1,t:"poll"|...}` envelope, additive.
- **Hot-path math.** A vote must NOT trigger a full-snapshot mutation
  fanout to boards: `Vote` writes via the existing SQLite upsert (prepared,
  WAL, single writer ≈ tens of µs each — 1,000-burst comfortable) and
  broadcasts a small poll-only delta frame (~KB) to (a) that show's
  audience lane and (b) the room's boards (results view). Rate guards:
  per-peer 1 vote / 300 ms + per-show soak limiter (default ~600
  requests/s accepted; beyond it HTTP 429 w/ Retry-After on REST and a
  server-side drop acknowledgment on WS — phones retry with jitter).
- **Boards stay sparse.** Boards receive at most {state change, count
  deltas, word list top-N}; the full re-tally ships only in `results`.
- **Load harness (test, not claim).** `ws/load_test.go`: 1,000 synthetic
  clients join, 50 votes/s for 10 s, assert p95 fanout frame latency < 100
  ms on this box and zero missed frames ≥ 99%. Runs marker-gated
  (`-run TestLoad`) so the normal suite stays fast; numbers recorded in
  PROJECT.md at Phase 4/7.
- **Non-goals.** No sharding, no Redis, no audience prediction
  infrastructure — SQLite single-writer + scoped frames covers 1,000 at
  this weight; re-evaluate only if the harness disagrees.

### 11.6 CuTePi as destination display — verdict + wiring

**Verdict: yes, as a controlled media destination — not as a webpage
display.** CuTePi is the sibling appliance: Go + GStreamer directly on KMS/HDMI
(it holds DRM master; no browser runs on it), with its own WS hub, media pool,
cue sheet and — decisive for us — **remote protocol listeners: QLab-style OSC
UDP :53000, OSC TCP/SLIP, HyperDeck server mode** (DrEVILish/CuTePi DESIGN
§12.8). It cannot serve TimerPi pages itself, and the two services cannot own
one HDMI connector; so the build:

- **Different Pis / outputs.** A room pairs one CuTePi box (media wall /
  walk-in video loop) with the room's TimerPi surfaces (its own browser
  display pages; TimerPi's Pi renders its own framebuffer/DRM timer).
  Pi 5's second HDMI may also host them separately on one box — hardware
  untested, documented as the 🟡 path.
- **Control path: OSC (ship v1).** Finish the WIP: TimerPi → CuTePi over
  its QLab channel — `GO`/cue start on TimerPi fires the paired CuTePi cue
  (engine `onFire` hook, outbound OSC to `osc.out.host:port`),
  `BLANK on → panic` (CuTePi panic holding image), `BLANK off → go`
  (resume). One paired CuTePi per show (settings kv: `osc.out.host/port`),
  pairing UI in Settings (a host picker that pings and shows CuTePi's
  current QLab channel), off by default.
- **Deeper join, filed upstream:** request logged as
  [CuTePi #4](https://github.com/DrEVILish/CuTePi/issues/4) — a "live
  endpoint" cue item so CuTePi can show TimerPi pages (timer / polls /
  walk-in schedule) as cue content on its own wall, with panic fallback
  and revocable room-scoped pairing tokens. Until that ships, rooms
  needing a live wall run the TimerPi page on a second output/screen.
- **Capability records, not a new protocol.** The CuTePi partner is a
  device-mesh entry with `role=cutepi` + OSC host (network.go identity
  vocabulary) so panels can show "Room A media: <host>.local alive".
- **What we do NOT build:** HTTP/WS API coupling into CuTePi internals, a
  native CuTePi client, vendoring, and **any push to the CuTePi or
  ftl-themes repos** (AGENTS rule). Problems found there are filed as .md
  under `reviews/upstream-issues/` or GitHub issues at the project —
  never pushed.
- **Upstream candidates to note (do not implement):** TimerPi OSC verb
  set is new-on-the-market even for CuTePi; if CuTePi later wants a native
  "TimerPi mode", that is an issue-on-their-repo conversation, not our code.

### 11.7 Decisions John should veto/confirm (defaults ship otherwise)

1. Audience lane = WebSocket (default) with REST fallback — REST-only
   would be simpler but triples phone battery at 1,000 clients.
2. Word cloud = DOM tiles scaled by votes (themeable/animatable) over a
   canvas cloud (pretty, unthemable).
3. One operator password per room-show (off = open) — super-operator is
   the device password. (No per-person accounts: not this release.)
4. Zone map = operator-uploaded image asset per zone.
5. Product version string **2.0 "Rooms"**; plan section §11 in PLAN.md.

### 11.8 Addendum — owner cross-check gaps (2026-10-05, folded into §11.3)

Cross-checking §11 against the brief found five items missing; folds:

6. **Join-QR slot.** §11.2 gains a `joinqr` slot (the room's audience QR
   card, always-available on `room`/`main` templates, refreshToken when
   Count link changes). The owner brief says members "scan a QR code
   displayed on screen in the Room" — it was only implied before.
7. **Per-item moderation.** Phase 5 also ships approve/hide/delete per
   individual question or word (DB verbs exist; only poll-level existence
   is handled today) — a mass abuse line in Q&A shouldn't take the room's
   whole set down.
8. **Join-storm reality.** §11.5 harness gains a second scenario: 1,000
   phones scan within ~30 s (opening) — staggered joins + per-show WS
   accept queue so the room's boards stay interactive during the crowd
   stampede. Steady-rate alone would have hidden this.
9. **Reduced motion — audience devices ONLY.** `prefers-reduced-motion` is
   honored **exclusively on the audience phone page** (`/a/`): animations
   become plain state changes there if the phone asks. Display and screen
   pages (`/d/`, boards, zone pages, walk-in layouts) **never** follow the
   setting — the room must always run its designed animation, every client,
   whatever that device's OS preference is.
10. **Deployment shape (open, for John).** The browser-rendered walk-in
    pages (`event`, `room`, `main`) need browser-capable hardware behind
    the physical screens — today only `timer` is DRM-native. Decision due
    Phase 2: kiosk-browser Pis, or extend the native renderer with the
    slot system. CuTePi's second HDMI on a Pi 5 is the third option.
11. Inherited debt: open review findings (waiting-room atomicity,
    claim fail-open, `UpsertScreen` cap, etc.) fold into Phase 7 hardening.

### 11.9 Show-file full fidelity (owner directive: ALL items export/import)

Today `.timerpi.json` carries cues + messages + schedule only. The v2
bundle (manifest **v2**, ZIP like CuTePi's `.CTP`: `manifest.json` +
`assets/`) round-trips **every item of a show**:

- cues + messages + schedule (today's content, unchanged shape);
- **all interaction items** — polls/Q&A/wordcloud/ideas/quiz rows with
  moderation state (`hidden/open/results`), submitted questions and words,
  and vote rows (peer-token hashes; re-import dedupes by `(poll,peer)`);
- **screens config** (per-show screen identities + preset links) and
  **display_presets** of that show (layouts/templates + their slot data);
- **zone label** + the zone **map asset** (file lands in `assets/`,
  referenced by hash);
- current skill-file hygiene stays: exported only when the show is HOLD;
  runtime playhead state remains non-portable (lands ARMED), documented.

Import: manifest v2 accepted; v1 (old plain-JSON cues+messages) keeps
importing for one major lifetime. Version const + health exposure in
Phase 7 with §11.3.
