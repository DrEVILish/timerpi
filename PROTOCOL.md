# TimerPi — internal protocol & conventions (for all agents)

Read `/opt/timerpi/PLAN.md` first. This file fixes the API/proto contract so
parallel agents don't drift. Follow CuTePi conventions (github.com/drevilish/CuTePi):
gin + gorilla/websocket + sqlx + mattn/go-sqlite3, vendored htmx, ftl-themes at `/ftl/`.

## Module & layout

- Go module name: `timerpi`. Go 1.25.
- Single port (default 80, env `CAPACITIMER_HTTP_PORT`, config override). HTTP + WS same server. WS path: **`/ws`**. Server MUST listen on `0.0.0.0:<port>` (all interfaces — this is an appliance; curl from other LAN devices must work).
- Data dir: `/var/lib/timerpi` (dev: `./data`). Config: `config/config.go` (json at data dir, atomic 0600).
- Packages: `config`, `timerpi` (domain+db+engine), `ws` (hub), `routes`, `mesh`, `drm`, `importdocs`.
- Templates in `templates/*.html` (html/template, fragments + pages). Static in `public/`.
- ftl-themes lives at `third_party/ftl-themes` (plain clone, not submodule, since no git repo here); served under `/ftl/` via routes; fonts resolve as `../assets/…` so serve `dist/` and `assets/` as siblings.

## Domain (SQLite via sqlx) — Agent B owns

```go
type Show struct { ID int64; Title string; CreatedAt, UpdatedAt int64 }
type Cue struct {
  ID int64; ShowID int64; Pos int64           // Pos = stable visual/run order
  Label string; DurationMS int64; Kind string // kind: "session"|"break"
  Tags string; Speaker string; HoldMS int64   // HoldMS = deliberate changeover/buffer
  TimerKind string                            // COUNTDOWN | COUNTSTOP | CLOCK
  Alert1MS, Alert2MS int64                    // per-cue alert thresholds (0 = none)
  AlertColor1, AlertColor2 string             // display color at each alert (#rrggbb)
  EndAction string                            // HOLD | OVERTIME | BLANK
  AutoContinue bool; Notes string; Color string
}
type Message struct { ID int64; Text string; Color string; ShownAt int64 } // ShownAt 0 = hidden
type Runtime struct {
  ShowID int64; ActivePos, PrevPos, NextPos int64  // 0 = none
  Paused bool; Running bool; EndAction string
  AnchorTS int64             // wall-clock epoch ms when active cue started (0 = idle)
  Rate float64               // countdown rate multiplier, default 1.0
  PausedElapsedMS int64      // elapsed (scaled) at pause
  DayStartTS int64           // for schedule/day-bar math
}
```

### Snapshot (JSON over WS + REST)

```json
{ "updatedAt": 0,
  "serverTime": 0,
  "show": { "id":1, "title":"Pawnee Townhall" },
  "runtime": { "running":false,"paused":false,"activePos":0,"nextPos":0,
               "anchorTS":0, "pausedElapsedMS":0, "rate":1.0, "dayStartTS":0 },
  "cues": [ { "pos":1, "label":"Welcome", "durationMS":600000, "kind":"session",
              "tags":"VT GFX", "speaker":"Leslie", "holdMS":0,
              "timerKind":"COUNTDOWN", "alert1MS":90000, "alert2MS":0,
              "alertColor1":"#ffaa00","alertColor2":"#ff4444",
              "endAction":"HOLD","autoContinue":false, "notes":"" } ],
  "messages": [ ] }
```

### Engine rules (Agent B implements `timerpi.Engine`, unit-tested)

- `Start(pos)`: anchors cue at wall clock, `running=true`. `Pause`: freezes, `PausedElapsedMS` frozen scaled elapsed. `Resume`, `Reset` (active cue back to anchor-less armed state), `Jump(pos)`, `Next`/`Prev`, `Go` = Start(next unarmed cue).
- Displayed remaining for the active cue = `duration*? rate scaling`: `scaledElapsed = (now - anchorTS) * rate` (add `PausedElapsedMS` when paused); `remainingMS = durationMS - scaledElapsed` (countdown kind). Rate change re-anchors: `{anchorTS=now, pausedElapsedMS+=scaledSinceLastAnchor}` — do NOT touch schedule math.
- Zero crossing uses cue `EndAction`: HOLD = stop & hold (running=false, remaining=0); OVERTIME = keep counting up (remaining negative); BLANK = hide countdown, show blank + scheduled end.
- Alerts fire at thresholds during countdown (color states 1/2), surfaced in snapshot state per active cue (`alertState` 0/1/2).
- Schedule computation (pure fn `ComputeSchedule(cues, dayStartTS, rate)`): cumulative times incl. HoldMS and breaks; used for day bar, per-row start/end, over/under.
- AutoContinue: on zero-crossing with `END_ACTION=HOLD` + `AutoContinue`, the engine auto-advances to the next cue after `HoldMS` changeover? NO — keep v1: on autocontinue the next cue starts immediately at zero crossing (HoldMS of the exhausted cue is added to the schedule only when idle-planning. v1: auto-advance immediately).

## WebSocket hub (Agent D implements `ws/hub.go`)

- On connect: client sends `{"v":1,"t":"join","role":"controls|display|mesh","show":<id>, "peerId":"<uuid>", "joinedAt":<ms>, "authToken"?:<token>}`.
- AUTH (A1): when the operator password is set (config `auth_password`), `controls` joins MUST present `authToken` (issued by `POST /api/login`; the browser keeps it in `localStorage.tp.atoken`) and are otherwise refused with `t:"err","message":"operator password required…"`. `display`/`mesh` joins stay open — a fresh stage TV has never logged in; `ws/commands.go` separately refuses ALL `cmd` frames from non-controls sessions while auth is on, so an open role cannot mutate. HTTP: `routes/auth.go` AuthGate — everything except `/health`, `/d/*`, static, `/ftl`, `/login`, `/api/login` needs the `tp_auth` cookie or HTTP Basic (`operator` / password); browsers (GET+Accept html) get `302 /login?next=…`, others a `401` JSON. `POST /api/auth/password {pw}` sets/clears (open when unset). Tokens derive from the password (`config.AuthToken`), so changing the password invalidates all sessions at once.
- SHOW PASSWORD (privacy tier 2): `POST /api/shows/:ident/passphrase {pw}` sets/clears a per-show extra password (itself gated by the current unlock once set); `POST /api/shows/:ident/unlock {pw}` issues the `tp_show_<CODE>` cookie + join token. When set: `/c/` **and** `/d/` render the lock page before any cue content, show-scoped REST (snapshot/cues/sync/import/messages/boards) answers `401`, and WS joins must carry `showToken` (mesh.js stores it from the unlock page) or the upgrade cookie. Tokens live in `localStorage.tp.show.<code>`. See `routes/showauth.go`.
- PRIVACY (2026-10-03): the homepage NEVER lists shows; `GET /api/shows` serves ids/counts only (`code`+`title`+paths appear just when the operator password is set, after AuthGate). "Recent" on the homepage is the browser's own localStorage ledger (`tp.recent.shows`), recorded from the operator dashboard itself; the server keeps no code index.
- Server replies `{"t":"joined", "snapshot":{...}, "you":{...peers list}, "peers":[...]}`; then on any mutation (engine callback) broadcasts `{"t":"state","snapshot":{...}}` AND (for page areas that can be server-rendered cheaply) `{"t":"oob","html":"<fragment>","target":"#cuelist"}` — oob is optional; digits are never server-ticked.
- Commands from clients `{"t":"cmd","action":"start|pause|reset|next|prev|go|jump|rate|addMsg|showMsg|hideMsg|cueEdit|cueAdd|cueDel|cueMove|settings|sync|blank|unblank","args":{...}}` → engine applies (server-authoritative). Engine rejects unknown; server re-broadcasts only on change. `cueEdit` accepts `startAt` "HH:MM" (E5, "" clears; garbage refused). Every successful mutating verb is recorded in the per-show action log (E4, `role:peer` actor); engine verbs fan out inside `runMutation` (no second Notify — it would double-broadcast).
- P2P signaling relayed: `{"t":"signal","to":"<peerId>","data":{...}}` ⇄ `{"t":"signal","from":"<peerId>","data":{...}}`. Peers list on join/leave: `{"t":"peers","peers":[{peerId,role,joinedAt}]}`.
- Keepalive: server pings every 30 s; client pings 20 s; connections silent >75 s are dropped.

## REST (Agent D)

```
GET  /                     homepage (create/show list)
GET  /c/:showid            operator dashboard
GET  /d/:showid            display page (fullscreen obj)
POST /api/shows            {title} → {id}       (html/template apps may use htmx create)
GET  /api/shows            list for homepage
GET  /api/shows/:id        snapshot (same JSON as WS)
PUT  /api/shows/:id/cues   full cue replace/import (JSON body of cues)
POST /api/shows/:id/import file upload multipart; fields: file, kind=auto|xlsx|xls|csv|json → applies cues (replaces or appends via ?mode=)
GET  /api/shows/:id/import-example?fmt=xlsx|csv|json → example doc download
GET  /api/shows/:id/qr?data=<url>&size=N   → PNG QR (skip2/go-qrcode)
POST /api/shows/:id/sync   mesh master push (same as prior session sync; updatedAt wins)
POST /api/shows/:ident/clone {"title"?} → 201 {code,title,cueCount,control,display} (E1: fresh code, cues/notes/day-start copied, no passphrase)
POST /api/shows/:ident/blank {"on":bool} → {blanked} (E3 blackout; Snapshot.Show.blanked flips every display)
GET  /api/shows/:ident/actions?limit=N → {actions:[{ts,actor,action,detail}]} newest-first, default 50 max 200 (E4)
GET  /health               {ok:true,...}
```

Static/theme files: `/public/...` (as `/assets/...`? use gin static for `public/` dir at `/`), `/ftl/...` from submodule (`third_party/ftl-themes` — serve `dist/` + `assets/` siblings so fonts `../assets` resolve).

## Htmx conventions (Agent C + D)

- htmx latest vendored in `public/src/htmx.min.js`.
- Mutating ops return the full target fragment with `outerHTML`; status-only use `hx-swap="none"`; error responses never replace panels.
- Digits/time: NEVER re-rendered server-side — client JS clock (display + dashboard) computes from snapshot (anchorTS/rate/pausedElapsedMS with local offset `serverTime - Date.now()`), re-rendered each rAF (or 50 ms tick). Shares rendering with P2P mesh bridge in `public/src/mesh.js` (Agent C ports the P2P logic patterns from /opt/capacitimer/web-server/mesh.js into vanilla ES module, no build step).

## Branding

Project = **TimerPi**. **Colours: Primary Purple for the UI; Secondary Green accents appear ONLY in the logo. Orange is not used anywhere.** Suggested tokens (documented, agents adopt in app CSS glue): primary purple `#7C3AED` (surface accents, active states, GO affordances may use a purple-500 ramp); logo secondary green `#22C55E`; backgrounds remain dark neutral (`#14161a`-family) so ftl-themes token contrast stays intact. UI text: "TimerPi". Logo for v1: simple SVG wordmark in `public/img/timerpi.svg` + PNG render for splash (Agent A stubs placeholder; visual polish later).

## Do-not-touch

- `/opt/capacitimer/**` — reference only. Never edit.
- Don't restructure other agents' files; if a contract doesn't fit, note it in your summary AND write a `NOTES-<pkg>.md` in your scope dir.

## REST control aliases (B5, 2026-10-03)
`POST /api/shows/:ident/cmd/:action` — `action` ∈ go | start | pause | resume |
reset | next | prev | jump | rate | daystart, body = the same JSON args the WS
command takes (`{"pos":..}` / `{"rate":..}` / `{"ts":..}`). Semantics call the
engine exactly as the WS path does; rate is clamped to ×0.5–×2.0 server-side
(`timerpi.ClampRate`) regardless of caller. Show-gated (passphrase) and
operator-auth-gated; OriginGuard CSRF rules apply (non-browser callers pass).
Answer: `{"ok":true,"action":…,"snapshot":{…}}`.

## Appliance default theme (B7, 2026-10-03)
`config.default_theme` ("" = bundled xbmc). `GET /api/theme` → `{current,
fallback, themes[]}` (bundles discovered from `third_party/ftl-themes/dist`);
`POST /api/theme {"theme":"…"}` sets/clears (charset-validated). Pages render
`html[data-theme]` and `<meta name="tp-default-theme">` server-side; a browser
with localStorage `timerpi.theme` still keeps its own pick.

## Scheduled day start (B4, 2026-10-03)
`shows.day_start` stores "HH:MM"; `POST /api/shows/:ident/daystart {"hhmm":…}`
validates (rejecting garbage: must parse as a real today-clock), persists and
anchors today in one call (empty string clears the automation without moving
an existing anchor). WS `settings {"dayStart":"09:00"}` does the same. Mains:
`Engines.Get` freshly constructs per-show engines; scheduled shows whose
runtime has no manual anchor get today@HH:MM at construction (reboot-safe).

## Appliance default theme — BLUE-FUTURE (2026-10-03 owner decision)
Product default theme = **blue-future** (deep-space navy HUD). Every root
(base / display / display_board / setup / settings / Go micro-pages) renders
`html[data-theme]`, the default stylesheet link and `<meta name="tp-default-
theme">` from `config.DefaultTheme()` — which falls back to "blue-future"
when nothing is configured. The browser bootstrap only loads a SECOND bundle
when localStorage carries a different pick; the default link already serves
the default theme (the earlier xbmc-linked mechanism shipped wrong bundles —
fixed). Icon sprite: templates ship the sprite NAME, JS (`applyIconTheme`)
retargets `<use>` hrefs to the active theme's bundle at boot, after oob
swaps, and on every theme change.

## Full-slot reorder (B2, 2026-10-03)
WS `cueMove` takes a second form: `{"pos":N,"to":M}` — direct full-slot splice
on top of `timerpi.MoveCue` (from/up|down stays for steppers). Same-slot is a
no-op; out-of-range targets clamp at the DB layer (documented MoveCue
behavior). REST twin: `POST /api/shows/:ident/moveto {"pos":N,"to":M}`
(show-gated like all content). The drag client commits ONE {pos,to} per
gesture; the undo ledger's inverse for a splice is a single splice back
({pos:to,to:pos}).

## Tier E (2026-10-05)
- E1 clone show: fresh code, cues/notes/day-start copied, passphrase never inherited, stopped runtime.
- E2 notice board widget (12th type, `opts.text` ≤256): static lobby text tile.
- E3 global blackout: `shows.blanked` → `Snapshot.Show.blanked`; WS `blank`/`unblank` + REST; all `/d/` surfaces + HDMI honor it (black + logo + STANDBY).
- E4 action log: `actions` table capped 200/show; WS logs `role:peer` on success only, REST logs `api`; dashboard `#actions-panel` + `frag-actions` oob (cursor = max id).
- E5 cue auto-start: `cues.start_at` "HH:MM"; engine Tick fires the first due cue past the playhead only while `!Running && !Paused`; validated in `Cue.Validate` + `cueEdit`; inspector `time` input with past-time confirm; rows chip `⏰ HH:MM`.
- E6 quick-add `+1m/+5m/+10m` chips fill m:ss (no auto-submit).

## Screens & presets (F1/F2, 2026-10-05)
- Join frame optionally carries `"screen":"<name>"` (displays only). `ws/session.go` upserts the `screens` registry (SQLite `screens`, per-show: theme, board_id, last_seen) and pushes the stored assignment immediately after `joined` (`{"t":"display","theme":…}` / `{"t":"screen-board","boardId":…}`). peers frames gained `"screen"` per entry.
- REST (`routes/screens.go`): `GET …/screens` (panel view = registry ∪ live), `GET …/screens/self?name=` (plain requireShow — config is not content), `POST …/screens/{config,match,rename,forget}` (gated; targeted pushes via `Hub.SendToScreen` + panel refresh via `Hub.SendToRole("controls")` + `{t:"screens"}` frame), `GET/POST …/presets` + `POST /:pid/apply` + `DELETE /:pid` + `GET /:pid/export` + `POST /presets/import` (preset file: `{"kind":"timerpi-display-preset","version":1,"name":…,"data":{"screens":[…]}}`).
- Client reconnect hardening (mesh.js): handshake watchdog 6 s, `online`/`visibilitychange` instant rejoin, `_wsDown` detach+dedupe, stale peer-channel prune on re-join.

## Review fix batch (2026-10-05)
- Hub: `broadcast` is serialized per show (`showHub.bmu`) — engine.notify runs on every mutating goroutine, so sessions/sigs access without it was a data race the runtime can kill the process for. `fanout` checks the show map.
- AuthGate exempts the two TV surfaces that must work without operator login: `GET /api/shows/<c>/qr` (join-card image) and `POST /api/shows/<c>/client-log` (error reports). Show-passphrase gating inside those handlers is unchanged.
- `POST …/client-log`: kind/message/source clipped rune-safe at storage; journal line flattened (no CR/LF forgery) and emitted once per request. Request bodies capped globally (8 MiB / 32 MiB imports).
- Screens: `boardId` must be a board of the show (config + preset apply); deleting a board clears its screen assignments. Identity rename is a no-op. Match returns `{matched, failed}`.
- Engine: wall-clock auto-start requires the day UNDER WAY (`ActivePos > 0`) — cloned/never-started shows with inherited `startAt` stay dark. Failed `GO` on a vanished cue rolls back its runtime mutation.
- ParseDuration (both engines): negatives and empty parts (`1::30`) rejected; truncation helpers are rune-safe.

## Screens gallery batch (2026-10-05)
- New WS-visible surface: kick sends `{t:"err", message:"session deleted …"}` then closes; mesh.js treats that phrase as terminal (`_deleted`, no reconnect until reload). `peers` frames gained nothing; `/api/shows/:c/screens` payload gained `peers[{peerId,role}]`, `widgets`, `boardName`, `previewLabel/previewClock/previewPct`.
- Waiting room REST (`routes/waiting.go`, table `waiting_screens` keyed (name,host), 10-min staleness): `POST /api/waiting/register {name,host}` + `GET /api/waiting/mine?name&host` (assignment consumed once) are AuthGate-exempt for login-free TVs; `GET /api/waiting`, `POST /api/waiting/:id/capture {code}`, `DELETE /api/waiting/:id` stay operator-gated.
- Gallery page route: `GET /screens/:ident` (code-only addressing; locked shows gate like /c/).
