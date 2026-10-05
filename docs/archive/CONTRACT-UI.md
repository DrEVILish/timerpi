# CONTRACT-UI.md — templates & client for Agent D

> Owned by Agent C (templates, static assets, client JS). Agent D wires gin +
> gorilla/websocket to them. If you change anything documented here,
> update this file in the same commit.

## 1. Template files

```
templates/base.html                    layout shell (+head/appbar/appstatus defs)
templates/index.html                   homepage        → template "base"
templates/dashboard.html               operator page   → template "base"
templates/display.html                 TV page         → execute its own root, "display"
templates/fragments/cuelist.html       "frag-cuelist"   id #cuelist
templates/fragments/current.html       "frag-current"   id #tp-now
templates/fragments/messages.html      "frag-messages"  id #messages-panel
templates/fragments/share.html         "frag-share"     id #share-panel
templates/fragments/daybar.html        "frag-daybar"    id #tp-daybar
templates/fragments/display_stage.html "frag-display"    id #d-stage
```

Parse the whole directory once (`template.New("").Funcs(...).ParseFS(templates,
"*.html", "fragments/*.html")`), then:

| Route                | Execute                        |
|----------------------|--------------------------------|
| `GET /`              | `ExecuteTemplate(w, "base", d)` with `d.Page="home"` |
| `GET /c/:showid`     | `ExecuteTemplate(w, "base", d)` with `d.Page="dashboard"` |
| `GET /d/:showid`     | `ExecuteTemplate(w, "display", d)` (standalone document, no app shell) |

`base.html` dispatches the page body itself:
`{{template (printf "page-%s" .Page) .}}` — so `.Page` MUST be `"home"` or
`"dashboard"`. There is no `page-display` in the base set; `/d/` uses the
`display` root directly.

## 2. Page data (field names!)

Shared fields on the dot for every page:

```
.Page   string  "home" | "dashboard" (REQUIRED — dispatch key)
.Title  string  optional <title> prefix (e.g. show title)
.Nav    string  active nav key: "" | "home" | "cues" | "display"
.Role   string  "controls" | "display" (lands on <body data-role>)
.ShowID int64   dashboard/display (lands on <body data-show>)
.Show   {ID int64, Title string}
```

Dashboard/additional:

```
.Current  {Pos, Label, Speaker, DurFmt, IsBreak, Kind}   // active cue view (may be zero)
.Next     {Pos, Label, DurFmt, StartFmt}                 // next-up view (may be zero)
.Runtime  {Running bool, Paused bool, ActivePos, PrevPos, NextPos int64,
           RatePct int (×100, e.g. 100), DayStartTS int64}
.Schedule {DayStartTS int64, DayStartFmt string ("19:00:00"), DayEndFmt string,
           TotalMS int64, TotalFmt string ("2:45:05"),
           Segments []{Pos int64, Label string, Title string,
                       Left string ("12.34%"), Width string ("8.26%"),
                       StartMS, EndMS int64        // relative to DayStartTS
                       IsBreak, IsDone, IsActive bool}}
.Cues     []{Pos, Label, DurFmt, StartFmt, EndFmt, HoldFmt string,
             Kind string ("session"|"break"), IsBreak bool,
             Tags []string, Speaker string, Notes string, Color string,
             TimerKind string, Alert1Fmt, Alert2Fmt string,
             AlertColor1, AlertColor2 string,
             AutoContinue bool, IsNext bool}
             // StartFmt/EndFmt come from timerpi.ComputeSchedule
.Messages []{ID int64, Text string, Color string, IsShown bool, ShownFmt string}
.Share    {Code string (8 digits), CodeFmt string ("1234-5678"),
           DisplayPath ("/d/7"), DisplayURL (absolute, may be "" — JS fills)}
.Peers    int (optional; informational only)
```

Homepage:

```
.Shows  []{ID int64, Title string, CueCount int, TotalFmt string,
           UpdatedFmt string (humanized), ControlPath ("/c/1"), DisplayPath ("/d/1")}
```

Display page additionally: `.Hostname string` (mDNS name for the status line).

Formatting conventions used by JS too: `m:ss` under an hour, `h:mm:ss` from
1 h; planned times of day as `HH:MM:SS`.

## 3. htmx + REST

Vendored htmx is at `/src/htmx.min.js` (Agent A). Client module
`/src/timerpi.js` revives htmx inside WS-inserted fragments (`htmx.process`).

| Element | Behavior |
|---|---|
| homepage `#show-list` | `hx-get="/frag/shows"` every 15 s + manual button, `outerHTML` |
| `/frag/shows`         | **new route**: re-render exactly `{{template "frag-shows" .}}` (needs `.Shows`) |
| homepage create form  | plain JS `POST /api/shows {title}` → `{id}` (json) → client navigates to `/c/:id` |
| dashboard import form | `hx-post="/api/shows/:id/import"` `hx-encoding="multipart/form-data"`, fields `file`, `kind=auto|xlsx|xls|csv|json`, `mode=replace|append` (add `mode` to the route — it maps to `?mode=`), target `#import-result` `innerHTML`; respond with a small HTML fragment, **never** an empty error page |
| cue CRUD / transport / messages / rate | NOT htmx: client JS sends WS commands (§4); hub fans out `oob` fragments per §5 |

One extra convenience route used by the homepage examples:
`GET /api/import-example?fmt=xlsx|csv|json` (no show id needed). If you'd
rather reuse `GET /api/shows/:id/import-example?fmt=…`, change the three
links in `index.html` (`/api/import-example`) — nothing else depends on it.

## 4. WS commands sent by the client

Sent as `{"v":1,"t":"cmd","action":…,"args":{…}}`; join uses
`{"v":1,"t":"join","role":"controls|display|mesh","show":<id>,"peerId":"<uuid>","joinedAt":<ms>}`.

| action | args | meaning |
|---|---|---|
| `go`      | `{pos?:int}`                       | start cue (next unarmed, or pos) |
| `start`   | `{pos}`                            | start the given/active cue |
| `pause`   | —                                  | toggle pause/resume |
| `reset`   | —                                  | re-arm active cue |
| `next` / `prev` | —                            | advance/retreat & start |
| `jump`    | `{pos, start?:bool=false}`         | playhead → pos (armed unless `start:true`) |
| `rate`    | `{rate: float ×0.5–×2.0}`          | rate multiplier (engine re-anchors) |
| `cueAdd`  | `{label, durationMS}`              | append cue |
| `cueMove` | `{pos, dir:"up"\|"down"}`          | swap order with neighbour |
| `cueDel`  | `{pos}`                            | delete cue |
| `cueDup`  | `{pos}`                            | duplicate cue (copy inserted directly after; row action A4, undoable) |
| `addMsg`  | `{text, color?:hex, show?:bool}`   | stage message (`show:true` → shown now) |
| `showMsg` / `hideMsg` | `{id}`                 | message overlay on/off |
| `clearMsgs` | —                                | drop all messages |

Offline (server link down), the mesh master executes these against the local
snapshot with the identical semantics (see `public/src/engine.js`) and pushes
the result on reconnect: `POST /api/shows/:id/sync` with the full
PROTOCOL snapshot JSON (`updatedAt` wins). Server must accept that shape and
re-broadcast `state`.

**Offline render parity (C2, 2026-10-04):** row/list *creation* is
server-fragment-owned (`oob` frames). While the master executes locally there
is no server, so the dashboard now MIRRORS the two mutated fragments from
`snap` client-side (`timerpi.js` `rebuildRowsIfNeeded` + `renderMessagesList`):
cue rows and the messages panel repaint instantly offline, same
`data-cmd`/ids contracts. When the server IS online its oob frames remain the
DOM owner; a signature guard makes both paths converge instead of fighting
(last writer wins, no double-render). Text goes through `textContent` only.

## 5. WS frames consumed by the client

| frame | handler |
|---|---|
| `{"t":"joined","snapshot":…,"you":{peerId,joinedAt},"peers":[…]}` | mesh init |
| `{"t":"state","snapshot":…}` | adopt (updatedAt dominance) |
| `{"t":"ping"} / {"t":"pong","serverTime":…}` | clock offset re-anchor |
| `{"t":"peers","peers":[{peerId,role,joinedAt}]}` | WebRTC connect + re-elect |
| `{"t":"signal","from":"<peerId>","data":{description\|candidate}}` | WebRTC |
| `{"t":"oob","html":"<fragment>","target":"#cuelist"}` | replace target element with `html`, re-run htmx |
| `{"t":"schedule",…}` | optional; also accepted `{"t":"schedule","rows":[{pos,startMS,endMS,holdMS,isBreak}],"totalMS","dayStartTS"}` |
| `{"t":"message","message":{id,text,color,shownAt}}` | overlay update (mirror of snapshot) |
| `{"t":"display","theme":"<slug>"}` | optional operator-pushed theme for displays |
| `{"t":"timer","runtime":{…},"updatedAt","serverTime"}` / `{"t":"cue",…}` | optional partial runtime patch, ≥ `updatedAt` only |

Digits are NEVER server-ticked; do not send per-second frames.

### oob targets used by these templates

| target | triggers when | render with |
|---|---|---|
| `#cuelist`        | cue CRUD/move/import/reset arrives | `frag-cuelist` |
| `#tp-daybar`      | posture/schedule recompute (cue edit, import, dayStartTS change) | `frag-daybar` |
| `#messages-panel` | message add/show/hide/clear | `frag-messages` |
| `#share-panel`    | never (initial only, or if the code changes) | `frag-share` |
| `#tp-now`         | rarely — cue edit of the active cue; digits are JS-painted | `frag-current` |
| `#d-stage`        | display layout change only | `frag-display` |

`oob.html` MUST be exactly one self-contained element carrying the target id
(every fragment file's root is that element — reuse the same template).

## 6. Element-id contract for timerpi.js

Never server-render digits into these; JS owns their text:

- `#tp-clock`, `#tp-tod`, `#tp-state-chip` (functional `class="status"` text),
  `#tp-meter` (`--meter-level`), `#tp-lamp-run`, `#tp-rate` (+`#tp-rate-out`),
  `#tp-next-label/-dur/-start`, `#tp-cue-label/-speaker`, `#tp-daybar-needle`
  (`left:%`), `.tp-alert-badge` per active row, `#tp-live-msg`.
- Display: `#d-clock`, `#d-stage[data-state]`, `#d-label`, `#d-speaker`,
  `#d-next-label/-dur`, `#d-status`, `#d-message`.
- Both pages: `#lamp-ws`, `#lamp-mesh`, `#conn-label`, `#conn-code`,
  `#peers-count`, `#tp-offline`, `#theme-select`, `#toast-region`.

`data-state` values on clocks/stage (JS-set): `idle armed running paused held
overtime alert1 alert2`. Colors come from theme state tokens; per-cue
`alertColor1/2` are copied by JS onto `--clock-alert1/--clock-alert2`.

Server MAY pre-render initial values into these (best-effort snapshot) — the
client overwrites at first `state`.

## 7. Static paths

| URL | dir |
|---|---|
| `/src/…` | `public/src/` (htmx.min.js, htmx-ext-ws.js, timerpi.js, mesh.js, engine.js) |
| `/css/…` | `public/css/` (timerpi.css) |
| `/img/…` | `public/img/` (timerpi.svg placeholder — Agent A may replace) |
| `/ftl/<slug>.css` | `third_party/ftl-themes/dist/` |
| `/ftl/icons/…`, `/ftl/themes.json` | same dist/ + sibling `/ftl/assets/` ← `third_party/ftl-themes/assets/` (load-bearing: fonts resolve `../assets/…`) |

`<link id="theme-css-default" href="/ftl/xbmc.css">` is in every `<head>`;
the inline bootstrap script applies `localStorage["timerpi.theme"]` before
paint and `timerpi.js` re-points `#theme-css-live` + all `<use>` icon hrefs on
change. Default theme: `xbmc` (data-theme value), brand accent `#7C3AED`
(wordmark/hero only via `.tp-brand`).

**Brand decision (owner, supersedes any earlier orange accent):**

- **Primary Purple `#7C3AED`** is the only UI brand accent (wordmark/hero, primary
  triggers `.btn-primary`/`.btn-go` — every other element keeps the theme's own accent).
- **Secondary Green `#22C55E`** lives ONLY in the logo artwork (`/img/timerpi.svg`) —
  never UI chrome, never text, never a message swatch.
- **Orange is banned** from the UI.
- `#ffaa00` (amber) and `#ff4444` (red) are the SPEC-mandated timer-alert **state
  colors** (`--warning`/`--danger` semantics, alert1/alert2). They may appear wherever a
  state needs a warning/danger tone (clock states, chip, stage edge, message swatches)
  but they are **not brand colors** — do not use them for default buttons, links, or
  decorative accents.

### TimerPi theme contract (first-class themes)

All 30 dist themes must load and be selectable, but only three are curated to
TimerPi's broadcast look and get tested per release:

1. **`xbmc` — DEFAULT.** The shipped bundle every other theme is diffed against;
   dark (luminance ≈ .084), blue accent, embedded data-URI icon/fonts. Loader
   fallback for every page (`#theme-css-default` + icon sprite `xbmc.svg`).
2. **`cue-lab` — RECOMMENDED.** Broadcast-ops dark theme (luminance ≈ .114) built
   for cue-list work; best match for the day-bar/meter/instrument vocabulary.
3. **`bloomberg` (Bloomberg Terminal) — RECOMMENDED.** Monospace terminal dark
   (luminance ≈ .039); highest text contrast of the dark set, ideal for
   wall-display legibility at TV distance.

Selection constraint: the default stays `xbmc` (localStorage absent → xbmc link +
`data-theme="xbmc"` + xbmc icon sprite), and pushed/displayed themes may be any
dist slug — the three above are only the ones we guarantee regression-look.

## 8. Interpretations Agent D/B should know

- **HELD** is represented as `paused=true` with `pausedElapsedMS == durationMS`
  (display shows `00:00` / chip HELD).
- **OVERTIME** is display-computed: `remaining < 0` && (`endAction=OVERTIME` or
  `timerKind=COUNTSTOP` keeps counting up). Shared clock math is in
  `public/src/engine.js` (`clockView`); the Go engine should match it.
- `snapshot.show.code` (optional 8-digit session code) is preferred by the
  share panel / status line; fallback is the show id zero-padded (`0000-0007`).
- Keyboard: Space=go, ←→↑↓=prev/next, P=pause toggle, R=reset, F=fullscreen
  (display) — suppressed while typing in form fields.

## 9. Fix-3 design amendments (post REVIEW-2, 2026-10-03)

### 9a. Class vocabulary — size modifiers

Verified against `third_party/ftl-themes/dist/xbmc.css` (and core): **there is no
`.select-sm` and no `.btn-lg`.** Core sizes text controls only via `.input-sm` /
`.input-lg`, applied alongside `.input` or `.select` (`.select.input-sm` keeps
the arrow gutter honest); buttons scale only via `.btn-sm`.

- Small select: `class="select input-sm"` — used by the theme picker and the
  message color pickers (was `select select-sm`, silently full-size).
- Large button: no core modifier exists, so TimerPi sizes it app-side
  (layout glue may size; it may not recolor):

```css
/* timerpi.css, Fix-3 block */
.tp-btn-lg { font-size: 1.15em; padding-inline: 2em; }        /* hero create button */
.transport .btn-go { font-size: clamp(1.4rem, 3vh, 2.2rem); min-inline-size: 7rem; }
```

`.btn-lg` tokens are deleted wherever that fixer owns the file (index.html
create button → `tp-btn-lg`). `frag-current.html`'s GO button still carries the
inert `btn-lg` class — the `.transport .btn-go` glue above makes it redundant,
so only the literal class token needs deleting by that agent.

### 9b. Message color palette

`frag-messages` color `<select>`: **Brand Purple `#7C3AED` is the default**
(`selected`), drafted first. The palette is purple-led; amber `#ffaa00` and red
`#ff4444` remain because they are the SPEC timer-alert state colors re-used for
message semantics (caution / "WRAP UP!"), not brand. `#44ff88` green is removed
(green is logo-artwork-only). "Theme" (`value=""`) stays as the opt-out that
inherits the theme's own text color for maximum legibility.

### 9c. HELD vs IDLE distinction

Engine/JS wiring already exists: `engine.js` emits all eight states and
`timerpi.js` sets `data-state` + the chip text (`held → 'HELD'` vs
`idle → 'READY'`). Fix-3 adds only the paint, app-side:

```css
.tp-clock[data-state="held"]  { color: var(--warning-text, var(--warning)); }  /* held ≠ muted */
#tp-now[data-state="held"] #tp-state-chip { color: var(--warning-text, ...); }
.tp-display-stage[data-state="held"], .tp-display-stage[data-state="paused"] { box-shadow: inset ...; }
```

No further Fix-1 wiring required; the toggle is `[data-state="held"]`
(app CSS class name for the visual toggle group: `.tp-clock`/stage chips read the
same attribute — no separate class added).

### 9d. Accessibility notes (JS-owned, do not re-paint)

- `#tp-clock` (and `#d-clock`) SHOULD be wrapped or annotated by JS with
  `aria-live="polite"` (region note only — JS owns the attribute; server renders
  `--:--` placeholders and must not announce them). Fix-1 note.
- `#share-qr` carries `alt="Display QR code"` — kept; it is meaningful content,
  not decorative.
- Transport buttons: sprite-only buttons need `aria-label` even when `title`
  exists (`title` is not a name). Fix-3 added labels in frag-current; the WS
  toast/`toast-region` remains `popover="manual"` per §R4 elsewhere.

### 9e. Home page honesty

- The hero now carries a **server-backed** badge (`{{len .Shows}}` saved shows)
  instead of any live-session lamp. Structure only; no hub signal is
  consumed on `/`.
- **Fix-1 wiring note (base.html, owned elsewhere):** the `appstatus` footer
  still renders `<span id="conn-label">connecting…</span>`, peers and the fake
  `Session 0000-0000` code on the home page where `initMesh()` never runs.
  Per O9 it must render a neutral `{{if eq .Page "home"}}` status line
  (e.g. `TimerPi · appliance`) and keep the lamp/peers/session cluster for
  dashboard only.

## A1/A2 additions (2026-10-03)
- `/login` — standalone operator login micro-page (routes/auth.go; no template-registry dependency so a broken template set can never lock the operator out). Stores the auth token in `localStorage.tp.atoken` for the WS controls join.
- `templates/settings.html` gains an **Operator Password** panel (`POST /api/auth/password {pw}`; empty clears). Stage displays never need the password.
- `fragments/current.html` gains `#tp-delta` — live over/under chip ("+0:35 vs plan" / "on plan"), data-state `over|under|ontime`; server pre-render is honest first paint, timerpi.js `paintDelta()` keeps it live per frame (COUNTDOWN cues only).
- **A3 filter**: dashboard header gains `#tp-cue-filter` (`/` focuses it, Esc clears, `#tp-cue-filter-count` is the aria-live `n/m` readout). `fragments/cuelist.html` gains `#tp-cue-no-match` (client-toggled). Filtering hides rows via `tr.hidden` inside `renderRows()`, so server oob re-renders come back already filtered; the active-row chase is suppressed while filtered to avoid scrollport yank. Keys doc: the `?` map lists `/` → filter running order.

## 2026-10-03 batch: privacy rework + show password + inline ops
- **Homepage (`index.html`)**: "Saved shows" list and `/frag/shows` are GONE (codes/titles are credentials and are never served publicly). Panels now: Create show, Join by code, **Recent on this device** (`#tp-recent-list`, filled by timerpi.js `recordRecent()` from localStorage — only this browser), import examples.
- **Share panel** gains the per-show **Extra show password** field (`POST /api/shows/:code/passphrase`). When set, `/c/` and `/d/` render `routes/showauth.go`'s standalone lock page (no template registry dependency) which stores `localStorage.tp.show.<code>`; WS joins carry `showToken`.
- **Rate row**: double-click the slider → reset ×1.00 (`initRateExtras.resetRate`); double-click (or double-tap) `#tp-rate-out` → inline numeric editor (×0.5–×2.0 clamp, Enter commits / Esc cancels); `clockUI._rateEditing` pauses the paint loop's slider/readout sync while editing. The slider's rate push captures the value AT INPUT TIME (paint used to stomp the debounced read).
- **Inline cue edit (`initInlineEdit`)**: cue label, speaker and duration cells are editable — dblclick (mouse) or two taps (touch); Enter commits one `cueEdit` (`{pos,label|speaker|durationMS}`), Esc cancels, blur commits. `#`/Start/End/buttons stay read-only (server-computed). Cuelist oob swaps defer while an editor is open (`pendingCuelistSwap`, replayed + re-filtered on finish).

## A6/A7 additions (2026-10-03)
- **Day-bar header control** (`dashboard.html`, outside the oob swap): `#tp-day-start` "Day starts now" → `settings{ts:now}` over WS; hidden-needle contract unchanged (unhides when `dayStartTS` lands); re-anchor asks confirm when a day is already anchored.
  **C2 fix (2026-10-04):** the WS path now FANS OUT (`eng.Notify()` after `ApplyCmd("daystart")`) — the START/END columns + day-bar scale used to stay stale until the next unrelated structural change; regression `TestDayStartFanout`.
  **Layout (2026-10-04):** the whole day-bar block (anchor form + `#tp-daybar`) moved INTO the running-order panel under the cue list (still outside the `#cuelist` oob swap); the add/filter header is two one-line clusters (app-side `.input` width override).
- **Day notes panel** (`#tp-day-notes`): shows-row memo per show, autosaved on a de-bounce to `POST /api/shows/:code/notes {text}` (show-gated, 4 000 char cap, verbatim). Pre-rendered from `.Notes` (PageData); printed on `?view=daysheet&print=1` (`fragments/d-daysheet.html` `.tp-dayprint-notes`); travels in snapshots via `Show.Notes` (json `notes`).

## B-Series additions (2026-10-03)
- **B1 inspector**: row pencil (`fragments/cuelist.html` `data-insp="<pos>"`) opens `<dialog id="tp-inspector">` in `dashboard.html`; `initInspector()` fills from the snapshot and commits one `cueEdit` with the full field set (label/speaker/durationMS/holdMS/tags/kind/timerKind/endAction/autoContinue/alert1MS+alertColor1/alert2MS+alertColor2/color/notes). Hex colours validate with the server's `colorRe` regex client-side.
- **B5 remote control**: `POST /api/shows/:code/cmd/:action` (details in PROTOCOL.md). For roomautomation configs: Companion button = POST …/cmd/go with Content-Type application/json (empty {} body fine).
- **B7 defaults**: `/settings` gains an "Appliance default theme" picker (list = installed ftl bundles); the chosen theme renders server-side on `html[data-theme]`; localStorage per-browser overrides it.

## B3/B4 additions (2026-10-03)
- **Undo**: `public/src/undo.js` — `createUndo({send,snap})`; DELETE undoes via re-create + cueMove-up chain, EDIT undoes by re-sending pre-values, ADD resolves its inverse via `observe(snap)`; header button `#tp-undo[data-undo]` + Ctrl/Cmd+Z (`performUndo`), ledger 10-deep, online + offline-master on the same send path.
- **Day-bar header** now has "Day begins at [HH:MM] Save" (`#tp-day-start-form` → POST `/api/shows/:code/daystart`) next to A6's "Day starts now"; the field prefills from `.DayStartHHMM`.

## Default theme = blue-future (owner decision, 2026-10-03)
- All roots derive their theme from `.DefaultTheme` (config fallback = blue-future). Products of the change: `html[data-theme]`, default `<link>` bundle, `<meta name="tp-default-theme">`.
- JS: `initTheme` always applies the icon sprite (`applyIconTheme`) for the active theme; oob swaps re-run it. Templates keep sprite *names* only.
- `/settings` picker: "blue-future (product default)" wording; `POST /api/theme {"theme":""}` resets to the product default.

## B2 drag reorder (2026-10-03)
- `td.tp-cue-pos` is the constrainted drag handle (`touch-action:none; cursor:grab`); pointer-drag ≥8 px lifts the row (`.tp-dragging`), `.tp-drag-line` renders the insertion edge in `.tp-cuelist-wrap` (position:relative now), drop commits `cueMove {pos,to}` (B2 contract).
- Handlers are DOCUMENT-delegated — oob swaps replace `#cuelist tbody` wholesale; tbody-bound listeners die with the old rows (caught in the browser drill).
- ▲▼ steppers + `TestUndoRestoreChain` undo interplay unchanged.

## B2 drag reorder (2026-10-03)
- `td.tp-cue-pos` is the constrainted drag handle (`touch-action:none; cursor:grab`); pointer-drag ≥8 px lifts the row (`.tp-dragging`), `.tp-drag-line` renders the insertion edge in `.tp-cuelist-wrap` (position:relative now), drop commits `cueMove {pos,to}` (B2 contract).
- Handlers are DOCUMENT-delegated — oob swaps replace `#cuelist tbody` wholesale; tbody-bound listeners die with the old rows (caught in the browser drill).
- ▲▼ steppers + `TestUndoRestoreChain` undo interplay unchanged.

## A4 duplicate + board edit auth (2026-10-04)
- **Duplicate**: each cue row gains the copy button (`data-cmd="cueDup"`, `icon-copy`) between the inspector pencil and the bin; WS `cueDup {pos}` inserts the copy directly after the source; undoable (ledger inverse deletes the copy at pos+1). Tests: `TestCueDupCommand`, `TestDuplicateTemplateShips`.
- **Board edit auth** (NOTES-board §5.4 closed): with the operator password SET, `?edit=1` ships the editing chrome only to credential-carrying requests (`boardEditAllowed` → cookie/Basic); the board itself renders read-only for everyone else. The board REST was already behind A1's AuthGate. Test: `TestBoardEditAuthGate`, live curl drill green.

## Mobile tile editor (NOTES-board §5.3, 2026-10-04)
- On board pages, *editing* under a 760px viewport shows the list editor `#b-editor` (toolbar markup renders it only when Editable; CSS keeps it `display:none` on every other tier — the pointer grid stays the desktop tool). Each tile is a row: `X/Y` steppers move it, `W/H` steppers resize, ⚙ opens the same per-type settings form, 🗑 deletes.
- The steppers operate on the SAME widget documents as the grid: `clampTile` bounds, `applyGeometry` repaint (now dispatching `tp-geom-<wid>` so the row's live digit updates), `scheduleSave` autosave (600 ms debounce PUT), and the overlap guard REVERTS with the same "Blocked: tiles overlap" honesty as the pointer path. Deleting renders through the existing `reloadEditing()` re-lock.
- Live drill (desktop viewport): toggle → 11 rows build; countdown's Y "+" correctly blocked+reverted by the overlap guard; `showtitle` Y "+" moved → repainted → server commit `Saved 14:06:xx`. v42 battery + `node --check` green.

## Tier E additions (2026-10-05)
- **E1 clone**: `POST /api/shows/:code/clone {"title"?}` → 201 `{code,title,cueCount,control,display}`. Fresh code, same cues/notes/day-start, NO passphrase (re-lock explicitly), stopped runtime. Dashboard share panel: `#show-clone-form` + `#show-clone-title` → opens `/c/<new>`. Tests: `TestCloneShowE1`, `TestCloneShowDefaultsE1`, `TestCloneFormShipsE1`.
- **E6 presets**: quick-add `+1m/+5m/+10m` chips (`data-preset-mss`) fill m:ss only — rows are still created by Add with an operator label. Test: `TestQuickAddPresetsShipE6`.
- **E2 notice widget**: 12th registry type (`notice`, 6×2, `opts.text` ≤256 chars server-truncated). Server `frag-b-notice` (escaped text), palette `data-add="notice"`, `TILE_DEFAULTS.notice`, `renderStatic` case, settings textarea (256 maxLength mirrors server), `.b-notice` wrap style, factory layout gains a Welcome tile. Tests: `TestNoticeWidgetRegistered`, `TestNoticeWidgetE2`.
- **E3 blackout**: `shows.blanked` rides `Snapshot.Show.blanked` (and `ShowRef.Blanked` for bodies). WS `blank`/`unblank` + `POST /:code/blank {"on":bool}`; role gating rides A1. All `/d/` surfaces ship `.tp-blanked` (black + logo + STANDBY), visibility purely `body[data-blanked]` — server-rendered AND repainted on every snapshot adopt (`paintBlanked`), HDMI via `drm_provider` `v.Blank`. Dashboard `#tp-blank` toggle (confirm to blank, instant release; `aria-pressed` follows the flag). Tests: `TestBlankRoundTripE3`, `TestBlankVerbsE3`.
- **E4 action log**: `actions` table (capped 200/show, pruned on insert; `LogAction` never fails the caller). WS verbs log `role:peer` on SUCCESS ONLY via `mutDone` (store ops) / `engDone` (engine verbs — `runMutation` already fans out; a second Notify would double-broadcast and shift one-state-per-command readers). REST logs `api` (cmd/moveto/replace/blank/clone/import/messages). `GET /:code/actions?limit=` (default 50, max 200) for debugging. NO dashboard section (owner decision 2026-10-05 — removed `frag-actions`/oob with it). Tests: `TestActionLogTailAndCap`, `TestActionLogE4`, `TestActionLogRestE4`.
- **E5 auto-start**: `cues.start_at` "HH:MM" ("" = off; validated in `Cue.Validate`, carried by all cue SQL + merge equality). Engine `Tick` fires the first past-playhead due cue while `!Running && !Paused` (hand operation never yanks; firing advances ActivePos so no refires; garbage never fires). WS `cueEdit{startAt}` (strict HH:MM, "" clears). Inspector `tp-insp-startAt` time input (client HH:MM check + confirm when already past today); rows show `⏰ HH:MM` (distinct from autocontinue AUTO). Tests: 6 engine cases + merge-remote + WS + surfaces.

## Robustness + Tier F batch (2026-10-05)
- **Oob-proof bindings (rule)**: anything inside an oob-swapped fragment (`#tp-now`, `#tp-daybar`) MUST use document-delegated listeners — direct bindings die on the first re-render. Fixed: `#tp-blank` toggle, rate cluster (slider input/dblclick/tap), `#tp-day-start` button + day-start form (now in the delegated submit listener). Same class fixed pre-emptively for all.
- **Ghost commands**: clicks inside `form[data-cmd]` bubbled to the click delegation and sent empty-arg ghosts ("addMsg needs text" on every message-field focus + bogus undo inverses). The delegation now returns early inside `form[data-cmd]`; submits own their forms.
- **Messages**: custom form gains checked `Show now` (`name="show" value="true"` — string "true" parses via `argBool`); WS accepts `"true"`/`"false"` shapes. `showMsg` on a deleted id is a silent no-op success (UPDATE matches zero rows) — encoded, not changed.
- **GO restarts held cues** (owner decision): `goLocked` clears spent elapsed before firing; explicit `Start(pos)` still refuses `ErrNoTimeLeft`.
- **Drag rebuild**: down-drags compensated (`to = slot>from ? slot-1 : slot` — server means "slot you become"); end-drop line below the last row; `body.tp-drag-body` selection lock (the old `.tp-drag body` never matched); wrap autoscroll; abort when the row detaches mid-drag; post-drag click suppression.
- **Quick-add rule** (owner decision): bare `30` = 30 min, `1:30` = 1 h 30 m, `30s` = 30 s (`views.ParseDuration` + JS `parseDur` in parity; imports keep the seconds rule). Labels/placeholders say H:MM.
- **F3 viewport identity**: `html:has(body.tp-display){font-size:1.5vh}` — display rem scales with output; operator pages keep OS root; 1–3px strokes stay device pixels.
- **F4 client errors**: `theme.js initClientLog()` (all pages) batches `onerror`/`unhandledrejection` (1/min/signature throttle) to `POST /:code/client-log`; DB capped tail + journal lines + `GET` tail. No UI surface (deliberate).
- **E4 panel removed** (owner decision): no Recent Actions section; table + endpoint + logging retained for debugging.

## F1/F2 screens + presets + reconnect (2026-10-05)
- **Screen identity** (owner refinement 2026-10-05): displays self-register a name — `?screen=` URL param (persistent override) → **window-scoped** `sessionStorage tp.screen` → generated `Screen-XXXX` (mesh.js `screenName()`). Deliberately NOT localStorage: N display windows on one browser are N SEPARATE screens (reload keeps a window's identity; a fresh window is new). Rename writes sessionStorage + the URL param. The join frame carries `screen`; the WS upserts the `screens` registry row and pushes that screen's stored assignment back on join (`{t:"display",theme}` reusing the B7 frame + `{t:"screen-board",boardId}`; board.js re-navigates its `?board=` only while locked — an open editor never gets yanked). peers frames carry `screen` for live presence.
- **Dashboard panel** `#screens-panel`: rows = registry ∪ live (name + LIVE ×N/offline + theme + board selects, Apply / Match all / ✕ forget; click the name to rename — the live tab adopts via `{t:"screen-rename"}` and rejoins). Refresh: `{t:"screens"}` frames + debounced peers churn. Endpoints in `routes/screens.go` (all show-gated; `screens/self` plain — not content).
- **Presets** (`display_presets`, SQLite): `POST /presets {name}` snapshots the current registry; apply/DELETE/export/import round-trip a plain JSON file `{kind:"timerpi-display-preset",version:1,name,data:{screens:[…]}}`. Survives server + browser restarts (the panel reads truth from the server each load).
- **Reconnect (owner bug report)**: mesh.js handshake watchdog (6 s — a dead network parks WebSockets in CONNECTING without events and stalled the retry loop), `online`/`visibilitychange` → instant rejoin (backoff reset), `_wsDown` detach+dedupe, `_pruneStaleConnections()` on re-join (half-dead pcs stuck `new` blocked channel re-negotiation after outages). `BLANK` is instant (no confirm, owner decision).
- Tests: `TestScreenJoinAndPushF1` (ws), `TestScreensFlowF1`, `TestPresetsRoundTripF2` (routes), `tools/mesh-reconnect-smoke.mjs` (28 client checks).

## Review fix batch (2026-10-05)
- **XSS purge**: `renderRecent` builds home rows via createElement/textContent (show titles are server content; the old innerHTML template executed markup from any crafted title). Codes filtered to `[A-Za-z0-9]` before hrefs. The file's textContent-only rule is now obeyed everywhere.
- **Undo no-capture**: `sendCommand(action, args, {noCapture})` — the ledger's own replays must not capture (double Ctrl+Z ping-pong fixed).
- **Screens selects** restore missing stored values as synthetic options (a stale/removed theme or board can never POST an accidental clear); boot fetches degrade independently.
- **Offline mirror parity** (engine.js): `blank`/`unblank`/`settings{ts,dayStart,title}` now apply locally so the blackout + day-anchor verbs work server-down (sync push reconciles on reconnect).
- **mesh.js**: heartbeat is OFFLINE-only (online master re-pushes refused syncs instead of flooding mesh-state every 3 s); `_postReconnectSync` keeps the dirty flag until the server ACCEPTS (401/500 retried via heartbeat); `screen-rename` also rewrites `?screen=` via history.replaceState so pinned-URL tabs keep the new identity across reloads.
- **engine.js fmtRemaining**: one tenths-space integer drives seconds AND the tenth — no more 0:10 → 0:10.9 upward jump.
- **board.js**: notice text clipped to 256 BYTES (TextEncoder) matching the server's rune-safe `ClipUTF8` cut; editor-list `tp-geom-*` window listeners torn down per rebuild (no accumulation).
- **theme.js client-log**: `keepalive: true` (pagehide flush survives teardown).
- Server side: QR + `client-log` pass AuthGate (TVs never log in; handlers keep their own gating); request bodies capped (8 MiB, 32 MiB imports); `cues` board assignments validated + cleared when the board is deleted; `RenameScreen(x,x)` is a safe no-op; match reports honest `matched`/`failed`; `DefaultTheme` takes the config RWMutex; `clearMsgs` fans out partial deletes. Regression: `tools/mesh-reconnect-smoke.mjs` (38 checks), `TestConcurrentBroadcastNoRace` (run under `-race`), `routes/review_fixes_test.go`, `timerpi/review_fixes_test.go`.

## Screens gallery + waiting room + session delete (2026-10-05)
- **Gallery page** `GET /screens/:ident` (`templates/screens.html`, nav item, `.Page="screens"`): a live preview card per registered screen — ftl panel/badge/select/button vocabulary; the preview maps the assigned board's 12-col layout as boxes plus current cue label / clock / progress (4 s poll of `/api/shows/:code/screens`, enriched payload: `widgets`, `boardName`, `peers[{peerId,role}]`, `preview*`). All textContent-built.
- **Edit layout**: each card's Edit opens a maximised `<dialog>` embedding `/d/:code?view=board&edit=1&preview=1&board=N` — the FULL board editor (add from palette, delete, drag/resize, X/Y/W/H steppers, new ⯇/⯈ align-left/right tile buttons) saved live via the existing PUT. `preview=1` makes board.js join WITHOUT registering a screen (no phantom presence); closing the modal unloads the iframe.
- **Waiting room** (`waiting.js`, overlay markup shipped on all three /d/ surfaces, `body[data-waiting]` driven like the blackout): a display whose show vanished hits mesh `badshow` → full-screen TimerPi logo + "waiting for connection…" + its screen name; registers via `POST /api/waiting/register` (AuthGate-exempt — TVs never log in) and polls `/api/waiting/mine` every 4 s. The gallery lists waiting displays with **Capture to this show** (`POST /api/waiting/:id/capture {code}` — claim consumed exactly once; the display navigates keeping `?screen=`) and Dismiss.
- **Session delete**: live cards show ⏻ per session (`DELETE /api/shows/:code/sessions/:peer` → `Hub.KickSession`, which delivers `{t:"err","session deleted…"}` before killing — mesh.js sets `_deleted` and STOPS reconnecting until reload). Panel rows gained the same buttons + a Gallery link.
- Tests: `TestGalleryPageF`, `TestWaitingRoomFlow`, `TestSessionKickFlow` (routes), `TestScreenJoinAndPushF1` (ws) unchanged/green.

## Theme audit fix round (2026-10-05, rev v49)
13-theme browser critique (strict 0–9.9 fidelity rubric). Bundles verified clean (required tokens + vocabulary 100% covered); fixes were ours: board `fmtRemaining` import, board boot `clockUI` guard, per-letter tag chips → split to match `views.tagsOf`, `.transport` wrap (BLANK), stage status/HELD contrast rules, toast offset, inspector field floors, `.b-widget` accent-blended border (true-black theme hardening). Upstream bugs drafted in `reviews/upstream-issues/` (windows95 header badge; lcars/death-star `--border`). Per-theme modal/board re-scores pending browser reconnection.
