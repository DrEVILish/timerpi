# TimerPi — UI Contract

> Which page uses which template, script and CSS, and the hooks that tie
> them together. Change this file with the page. Wire formats are in
> [../PROTOCOL.md](../PROTOCOL.md); behaviour is in [ARCHITECTURE.md](ARCHITECTURE.md).

## 1. Pages → templates → scripts

| Page | Template (`templates/`) | Script (`public/src/`) | Notes |
|---|---|---|---|
| `/` home | `home.html` → `home` | `event.js` | Join by code, create event, open a screen, recent events |
| `/e/:code` lobby | `event.html` → `event` | `event.js` | Room sign-in, SuperOperator sign-in |
| `/e/:code/admin` | `event.html` → `event-admin` | `event.js` | Tabs Live · Setup (hash `#setup`). Live: Blackout all / Restore all, one `.table.is-stacked` row per room (state, now, time left, next, screens, Open + ⋯ with GO, Pause, Blackout and the room admin), add room. Setup: pair a box, event settings (save on change; the password keeps Save), event file, delete |
| access denied | `event.html` → `denied` | — | Rendered by `routes/access.go` |
| `/c/:room` room | `base.html` → `page-dashboard` (`dashboard.html`) | `timerpi.js` + `moderate.js` + `swatches.js` | One workspace (2026-11-12): the now band (`#tp-now`: readout, title, meter, adjust, rate) and the transport (Prev · GO · Pause · Next · FLASH · WRAP UP · BLANK) sit above the tabs on every tab; phones stick the transport to the bottom. Tabs: Run · Audience (hash `#audience`) · Screens link (SuperOperator only). Under them the tab's panel (Run: running order with the day bar; Audience: the item library) beside the **On air now** column (`.tp-side`: `#tp-aud-onair` cards, then Messages), which both tabs share. Import and the join QR are ftl drawers (`#tp-import-drawer`, `#tp-share-drawer`). Running order: cells edit inline, footer add row tied to `#tp-add-form`; rows are Go · edit · ⋯ (touch: Go · ⋯); the row menu `#tp-row-menu` (⋯, right-click, long press) holds Cue up, All details, Duplicate, Move, Delete. Columns drop with the table's own width (container `tp-run`); the cue editor has them all. With several rooms the room name in the bar is a switcher (`#tp-room-pop`). `cuelist.html` and `timerpi.js cueRow()` render the same row: change both |
| `/screens/:room` | `base.html` → `page-screens` (`screens.html`) | `timerpi.js` → `screens.js` | Screens cards, set-up dialog, layout editor dialog; full page width, like the room page |
| `/d/` ready card | `dready.html` | `waiting.js` | Waits to be captured |
| `/d/:room` timer | `display.html` | `timerpi.js` | Plain stage timer |
| `/d/:room?view=next\|daysheet\|clock` | `display_variants.html` | inline module + `engine.js` | |
| `/d/:room?view=board` | `display_board.html` + `fragments/b-*.html` | `board.js` (+ `timerpi.js`) | Layout canvas; `?edit=1&preview=1` = editor (moderators only) |
| `/a/:room` phone | `audience.html` | `audience.js` | Audience lane WS + REST fallback |
| `/settings` | `settings.html` | inline | Box settings (box password) |
| `/box` | `event.html` → `box` | `event.js` | Box password: setup, sign-in, change, sign out |

All templates share `head` (`base.html`): theme bootstrap, `timerpi.css`, `app.css`, favicon.
JS/CSS URLs always go through `{{asset "/src/x.js"}}` (`views/assets.go`).

## 2. Shared client modules

| Module | Exports |
|---|---|
| `ui.js` | `api()`, `toast()`, `el()`, `inlineEdit()`, `normalizeCode()`, `fmtCode()`, `fmtRemaining()`, recent-events helpers |
| `dialog.js` | `tpConfirm()`, `tpPrompt()` (fields may set `type`) — never use browser `confirm`/`prompt` |
| `theme.js` | `applyTheme()`, `applyIconTheme()`, `initClientLog()` |
| `mesh.js` | `Mesh` (WS + P2P), `screenName()` |
| `engine.js` | Client-parity countdown/schedule math |
| `layout-edit.js` | `editTile()`, canvas geometry helpers |
| `layout-document.js` | `LayoutDocument` (selected version, replacement, save, rollback) |
| `display-session.js` | `createDisplaySession()` (screen identity and shared display frames) |

## 3. CSS

- **ftl-themes** provides every component (`.panel`, `.btn`, `.tabs`, `.status`, `.badge`, `.lamp`, `.segmented`, `.field`, …) and every colour token. Never hard-code colours except the brand purple `#7C3AED`.
- `public/css/app.css` holds layout for the newer surfaces: `.tp-page`, home doors, event lobby/admin (`.tp-live-*`, `.tp-rooms-table`), the phone page (`.tp-aud-*`), the moderator panel (`.tp-item`, `.tp-entry`, `.tp-onair`), the Screens page (`.tp-scr-*`, `.tp-kind`), room tabs, and the **screen canvas** (`body.b-board`, `--b-rows`, `html[data-rotate]`, container-query typography, `.b-rooms`, `.b-evsched`, `.b-qa-*`).
- `public/css/timerpi.css` holds the older dashboard, stage and board styles.

## 4. Hooks that must not drift

- **Screen pages:** `html[data-theme]`, `html[data-rotate]` (0/90/180/270), `body[data-role="display"]`, `body[data-blanked]`, and on boards `body[data-orientation]`, `body[data-kind]`, `style="--b-rows:N"`.
- **Board tiles:** `<section class="b-widget" data-widget="<type>" data-wid="<id>">` with a `.b-w-body`. Edit chrome (`.b-w-chrome`, `.b-w-resize`) renders **only** when `P.Editable`.
- **Timer ids** (JS-owned text, never server-ticked): `#tp-clock`, `#tp-state-chip`, `#tp-meter`, `#d-clock`, `#d-stage[data-state]`; `data-state` ∈ `idle armed running paused held overtime alert1 alert2`.
- **oob targets:** `#cuelist`, `#tp-daybar`, `#messages-panel`, `#tp-now`, `#share-panel`, `#d-stage`. Each fragment's root element carries its target id.
- **Room page:** `[data-tab]` buttons and `[data-panel]` sections (`run`, `audience`); `.tp-room`, `.tp-work`, `.tp-side`, `.tp-transport-bar`; `#tp-aud-panel`, `#tp-aud-items`, `#tp-aud-onair`, `#tp-aud-count`, `#tp-aud-tabcount`, `#tp-aud-editor`, `#tp-aud-menu`, `#tp-row-menu`, `#tp-import-drawer`, `#tp-share-drawer`, `#tp-room-pop`.
- **Screens page:** `#tp-screens-page[data-room]`, `#tp-gal`, `#tp-waiting-list`, `#tp-capture`, `#tp-screen-edit`.
- **Text safety:** all user text reaches the DOM through `textContent` (or `el()`), never `innerHTML`.

## 5. Motion

- Screens always animate. Each layout has a default (`anim` fade|slide|pop|none, `animMS`). Each tile may override it (`opts.anim`, `opts.animMS`).
- Content tiles animate when their text changes; audience tiles animate when the item or its phase changes.
- Only the phone page honours `prefers-reduced-motion`.
