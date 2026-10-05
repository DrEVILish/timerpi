# NOTES-board.md — display-board handoff (Agent N → UI supervisor)

Customizable display board: operators/clients compose their own screens from
draggable widgets fed by live show data. Route
`GET /d/:code?view=board&board=<bid>` (default = show's first board, seeded
"Main"). Status: built, `go build ./...` + `go test ./...` green,
`node --check` clean, throwaway-:8093 curl battery green (see §7).

## 1. Files created / touched

| File | Change |
|---|---|
| `boards/boards.go` | NEW — layout model, WidgetTypes registry (11 types), Normalize/Validate, sqlite persistence (`display_boards`, own `Migrate` vs the same handle) |
| `boards/boards_test.go` | NEW — overlap/clamp/normalize/registry/persistence tests |
| `routes/boards.go` | NEW — REST + `boardView`/`boardData` page builder |
| `routes/board_test.go` | NEW — CRUD 200s, numeric 404s, view widget-id assertions, ParseFS |
| `templates/display_board.html` | NEW — `display_board` standalone doc |
| `templates/fragments/b-widgets.html` | NEW — 11 `frag-b-*` tiles |
| `templates/fragments/b-chrome.html` | NEW — `frag-b-chrome` (offline strip + join card), `frag-b-toolbar` (edit chrome) |
| `public/src/board.js` | NEW — the only client file (imports `engine.js` read-only) |
| `routes/display.go` | EXTENDED (not replaced) — `"board"` in the view switch + branch to `boardView` |
| `routes/routes.go` | ONE mount line — `RegisterBoards(r, d)` |
| `views/views.go` | ADDITIVE structs only — `BoardWidgetVM/BoardVM/BoardInfo/BoardJoinVM/BoardPage/WidgetDot` |
| `public/css/timerpi.css` | APPENDED one fenced `b-` block at end |
| `timerpi/db.go`, `timerpi.js`, `mesh.js`, `engine.js`, `display.html`, `display_variants.html`, `d-*` frags, `go.mod` | untouched |

## 2. Widget registry (11 types, v1)

`countdown cuelabel speaker nextup wallclock progress dayprogress messages
showtitle rate schedule` — see `WidgetDef` table in `boards/boards.go`.
Tile root: `<section class="b-widget" id="b-w-<id>" data-widget="<type>">`.
Live paint hooks are `.b-js-*` classes inside each tile (no per-type id
registry to keep in sync). Server renders zero-JS initials for every tile
(countdown text+state via `timerpi.DisplayedRemaining`, wall clock,
progress/day widths, next-up, schedule rows, shown messages, title+code,
rate chip).

## 3. REST shapes (all code-addressed; numeric show ident → 404)

- `GET /api/shows/:code/boards` → 200 `[{id,name,layout:{v,widgets},updatedAt}]` (seeds "Main")
- `POST …/boards {name}` → 201 (factory layout)
- `PUT …/boards/:bid {name?, layout?}` → 200; unknown type / overlap / empty → 400; missing board → 404
- `DELETE …/boards/:bid` → 200 (`{ok:true}`)
- `PUT` also `eng.Notify()`s so open WS clients re-adopt (layout itself is
  NOT in the snapshot — editors re-fetch; see §6).

## 4. Template ids (for the supervisor's selector audit)

`#b-grid`, `#b-offline` (offline strip, `b-offline` paint), `#b-join-qr`,
`#b-layout` (embedded layout JSON, `template.JS`), `#b-toolbar`,
`#b-edit-toggle`, `#b-boards`, `#b-new`, `#b-save-state`, `#b-palette`,
`#b-palette-list` (`[data-add]` × 11), `#b-reset`, `#b-settings`,
per-tile `b-w-chrome` / `[data-gear]` / `[data-del]` / `[data-resize]`
(visible only under `body[data-editing]`). `BoardWidgetVM.Style` is
`template.CSS` (ints-only, bypasses the style-attr escaper — do not put
user text through it).

## 5. What the UI-supervisor pass should review

1. **Layout defaults per role** — one factory layout for everyone today
   (`DefaultLayout()` ≈ stage + next-up + schedule). Consider role presets
   (e.g. lobby = wallclock+showtitle+messages; stage-side = countdown+nextup).
2. **Widget chrome wording** — palette labels, toolbar ("Edit/Lock", "Reset
   layout", "Saved HH:MM:SS"), empty states ("No messages on stage").
3. **Mobile edit ergonomics** — drag/resize use pointer events + capture
   (touch-action:none on handles) but 4.5rem rows + 12 cols are tight on
   phones; consider a list-based tile editor <760px.
   → SHIPPED 2026-10-04: `#b-editor` list editor (steppers X/Y/W/H per tile,
   same clamp/overlap-revert/autosave contracts as the grid) shows on the
   editing tier ≤760px; desktop keeps the pointer grid. Live-drilled both
   paths; CONTRACT-UI "Mobile tile editor" section documents it.
4. **Edit auth** — `?edit=1` was the only gate (anyone with the URL could
   compose). → CLOSED 2026-10-04: with a password set, `?edit=1` ships the
   editing chrome only to credential-carrying requests (`routes/boards.go`
   `boardEditAllowed`); the board renders read-only for strangers. The
   REST was already behind A1's gate. Test `TestBoardEditAuthGate`.
5. **Reset semantics** — Reset restores `FACTORY_DEFAULT`, a JS mirror of
   Go's `DefaultLayout()` (duplication documented in board.js). Single-source
   alternative: `GET /api/shows/:code/boards/default` or a `?factory=1` read.
6. **Multi-editor convergence** — PUT validates server-side (last writer
   wins, no merge); open editors don't live-reload чужой layout. Fine for
   v1, flag if two operators compose at once.
7. **Offline honesty** — `#b-offline` shows whenever board.js's WS is down.
   → FIXED in code 2026-10-04 (the module header cites it): board.js joins the
   SHARED display mesh itself (its own Mesh, full snapshot adoption —
   heads-up though: before 2026-10-04 13:5x the page also booted
   timerpi.js's mesh → TWO sessions per screen; timerpi.js now skips its
   join on `dataset.view === 'board'` pages, board.js remains the single
   data path, and the `{"t":"display","theme"}` swap repaints there via
   the shared `theme.js` leaf module). Reader: `templates/display_board.html` boot list.

## 6. Deliberate non-goals / known edges

- `cuelabel` `source: speaker` and `schedule` `count` are the only
  cross-widget opts besides `tenths`; settings form renders per-type fields.
- `dayprogress` tile degrades to 0% until the day anchors (same rule as the
  dashboard day bar).
- Board `:bid`s are internal ints (only the *show* address is code-only).
- Brand: purple `#7C3AED` only (tile edge while editing, progress fill,
  eyebrow, resize handle); `ff-warning/ff-critical` only inside
  countdown/message semantics.

## 7. Test evidence

- `go test ./boards/ ./routes/ -count=1` → ok (also full `go test ./...` ok)
- `node --check public/src/board.js` → clean; `gofmt` clean on touched files
- Throwaway `:8093` (own `TIMERPI_DATA_DIR`, CWD checkout; :80 untouched,
  no systemctl): seed list → 1×Main; create → 201; rename+layout → 200;
  unknown-type → 400; overlap → 400; numeric show → 404; board view → 200
  with all 11 `b-w-*` ids + intact `grid-column` styles; `?edit=1` → toolbar
  + 11 `[data-add]`; `?view=bogus` / `?board=999` → 404; delete → 200.
  (Note: :8092 was already held by another process during verification —
  used :8093 instead.)
