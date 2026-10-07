# TimerPi: component gaps and feature requests

**Filed as:** [ftl-themes issue #68](https://github.com/DrEVILish/ftl-themes/issues/68), owner direction, 2026-10-07. Merged from the Run, admin and display notes written during the ftl component swap.

TimerPi (an event timer: operator web app, TV/signage displays and an audience phone page) has moved its UI onto ftl-themes components wherever one exists. Its AGENTS.md rule is now "pull components from ftl-themes; don't build fresh ones", and dialogs are always ftl modals.

This issue collects everything found during that move, checked against `dist/blue-future.css` and `dist/material.css`:

- **A items** are gaps, bugs or awkward spots hit while adopting existing ftl components (repro and proposed fix included).
- **B items** are TimerPi components we had to keep because ftl has no equivalent. Each is a feature request: what it is and why apps need it, TimerPi's current implementation, proposed ftl markup/classes/tokens/states, accessibility and motion behaviour, and why the nearest ftl component falls short.

IDs are prefixed by area: **R** = Run page and app shell, **M** = admin/setup pages, **D** = displays, boards and phone page. Some overlap across areas (e.g. `.connection` states, editable cells, the dashboard grid); they are kept separate where the use case differs.

Display context that drives many D items: a TimerPi display is read from 3–10 m, has no operator chrome, sets the root size to `1.5vh`, and **always animates** regardless of `prefers-reduced-motion` (signage); only the phone page honours reduced motion.

## Checklist

- [ ] **R-A1** Breadcrumb separator is invisible in some app bars
- [ ] **R-A2** Editing-cell controls can't be made compact without an ID selector
- [ ] **R-A3** Only *direct-child* controls get the editing treatment
- [ ] **R-A4** One popover shared by many triggers: document `showPopover({ source })`
- [ ] **R-A5** `.dropdown[popover]` holding a form field is undocumented
- [ ] **R-A6** `.connection` has no state for the very first connect
- [ ] **R-A7** Toast region ignores the app bar
- [ ] **R-B1** Countdown / timer readout `.countdown`
- [ ] **R-B2** Day bar `.timeline-bar`
- [ ] **R-B3** Table row drag-to-reorder
- [ ] **R-B4** Row states: past, next, per-row colour/category
- [ ] **R-B5** Two-tap "armed" destructive button
- [ ] **R-B6** Server-confirmed toggle on a solid `.btn`
- [ ] **R-B7** Inline small colour dot
- [ ] **R-B8** App-bar-aware toast region
- [ ] **M-A1** `.grid` is auto-fit only: a lone card stretches to the full row
- [ ] **M-A2** `.grid-sm` / `.grid-lg` change the column minimum, not the gap
- [ ] **M-A3** `.otp` has no intrinsic width: it collapses in a flex row
- [ ] **M-A4** Editable-cell states only exist under `.table td`
- [ ] **M-A5** `.toggle-group` can't hold two-line labels or wrap
- [ ] **M-A6** `.list-item-title` as a direct child forces a column layout
- [ ] **M-A7** `.accordion-panel` text is `--muted`
- [ ] **M-A8** No `.text-danger` (or any text-tone utility)
- [ ] **M-A9** `.status-rec` exists in one theme only
- [ ] **M-B1** Large button `.btn-lg`
- [ ] **M-B2** Segmented alphanumeric code input with a separator (XXXX-XXXX)
- [ ] **M-B3** Scaled live layout preview tile
- [ ] **M-B4** Thumbnail image
- [ ] **M-B5** Key/glyph legend
- [ ] **M-B6** Sticky tabs
- [ ] **M-B7** Corner close/remove button on a card
- [ ] **M-B8** Page width containers
- [ ] **D-A1** No full-screen overlay wrapper for `.empty-state`
- [ ] **D-A2** `.splash` is always themed; no "dark stage" variant
- [ ] **D-A3** `.splash-art` for a logo needs four token overrides
- [ ] **D-A4** `.connection` lamp size does not follow `--connection-size`
- [ ] **D-A5** `.connection` and `.empty-state.is-loading` motion cannot be forced on
- [ ] **D-A6** No filled `.badge` variant
- [ ] **D-A7** `.meter` has no single-colour mode
- [ ] **D-A8** `.gauge-linear` sizes are rem-only
- [ ] **D-A9** `.schedule-item` rows are tap targets even when not interactive
- [ ] **D-A10** Dashboard edit mode vs fixed placement (edit-mode classes over a fixed-placement grid)
- [ ] **D-B1** Word cloud with a physics layout
- [ ] **D-B2** Ranked live Q&A wall (animated reorder, spotlight, answered/leaving)
- [ ] **D-B3** Poll results count-up and staggered reveal
- [ ] **D-B4** Display / kiosk motion mode + per-element enter/exit choice
- [ ] **D-B5** Blackout/standby, stage alert edge glow, flash/blink attention
- [ ] **D-B6** Full-screen message band with operator colour
- [ ] **D-B7** QR join card
- [ ] **D-B8** Daysheet / print schedule table with a NOW needle row
- [ ] **D-B9** Signage safe area, fit padding and viewport-scaled root type
- [ ] **D-B10** Dashboard grid with fixed x/y placement (no reflow)

---

## R: Run page (operator room, app shell)

### A. Gaps and awkwardness hit during the swaps

#### R-A1. Breadcrumb separator is invisible in some app bars
- Repro: `<header class="app-bar"><nav class="nav"><span class="breadcrumbs"><a class="breadcrumb-item">Test 1</a><a class="breadcrumb-item is-current">Room A</a></span></nav></header>` in `blue-future`. The "/" between items is painted with `--breadcrumb-separator, var(--hairline)`; on the dark app bar the hairline is near-black, so it reads "Test 1  Room A". `material` is fine.
- Cause: `.app-bar .breadcrumbs` re-points `--muted` and `--breadcrumb-current-fg` at `--app-bar-text` (dist line ~3000) but not the separator.
- Fix: in that rule add `--breadcrumb-separator: color-mix(in srgb, var(--app-bar-text) 45%, transparent);`, or default the separator to `var(--muted)` instead of `--hairline` (a separator is text, not a rule line).

#### R-A2. Editing-cell controls can't be made compact without an ID selector
- Repro: a whole-row editor `<tr class="is-editing">` in a 13-column table that must fit 1440 px. ftl's `.table :is(td.is-editing, tr.is-editing > td) > :is(.input,.select,.textarea)` (specificity 0,3,2) sets `font: inherit`, padding from `--cell-pad-x/-y`, and `.select` gets `padding-inline-end: calc(var(--cell-pad-x) + 1.5em)`. Our table overflowed by 43 px (1075 > 1032) until we added `#cuelist tfoot td > .select { padding-inline-end: .95rem; font-size: .74rem }` etc. (`public/css/timerpi.css`, "Compact add-row controls"). A class selector can't win.
- Fix: tokens read by that rule: `--cell-editor-font-size` (default `inherit`), `--cell-editor-pad-x` (default `--cell-pad-x`), `--cell-editor-arrow-space` (default `1.5em`). Or wrap the rule in `:where()` so apps can override at class specificity.

#### R-A3. Only *direct-child* controls get the editing treatment
- Repro: an alert cell holding a colour dot plus an input: `<td><span class="tp-alert-field"><button class="tp-alert-dot"></button><input class="input input-sm"></span></td>` inside `tr.is-editing`. The input keeps its normal bordered look and the cell keeps its padding, so that cell looks different from the rest of the row.
- Fix: also match `> .input-group > :is(.input,.select)` (and document `.input-group` as the way to put an addon next to an editing control).

#### R-A4. One popover shared by many triggers: document `showPopover({ source })`
- CONTRACT "Floating surfaces" says a `popovertarget` button is the implicit anchor. One colour popover is reused by ~30 dots in a table (they are spans, created per render), so `popovertarget` doesn't fit. `pop.showPopover({ source: dot })` (Chromium 133+) sets the implicit anchor and ftl's `position-area` then works: our hand placement with `getBoundingClientRect` (12 lines) was deleted. Older browsers ignore the argument and the popover opens centred, which is the documented fallback.
- Fix: a sentence plus an example in CONTRACT "Floating surfaces" and docs for `.popover`.

#### R-A5. `.dropdown[popover]` holding a form field is undocumented
- We put a label + `<select>` (the theme picker) in `<div class="dropdown" popover>`. It works (padding, surface, anchoring), but docs only show `.dropdown-item` buttons. Please document "a dropdown may hold a `.field`" or name a `.popover` as the right surface for small forms.

#### R-A6. `.connection` has no state for the very first connect
- States are live / reconnecting / offline / degraded / initializing… We map the first connect to `reconnecting` (dotted lamp). `initializing` exists but uses the static warning lamp, which reads as a fault on every page load. Suggest documenting `initializing` as "first connect, neutral" with a muted (not warning) lamp, or adding `connecting`.

#### R-A7. Toast region ignores the app bar
- `.toast-region[popover]` sits at `inset: 1rem 1rem auto auto`, so in an `.app` shell toasts cover the app bar's right-hand buttons (Change Theme, Leave). TimerPi carries `body.app .toast-region { inset-block-start: calc(4.2rem + env(safe-area-inset-top, 0px)); }` (`public/css/timerpi.css` ~973) with a magic bar height.
- Fix: themes already know the bar height; publish `--app-bar-height` on `.app` and default the region to `inset-block-start: calc(var(--app-bar-height, 0px) + 1rem)` when inside `.app` (`.app .toast-region`). Add `--toast-offset-top` for apps with extra sticky chrome.

### B. Feature requests: TimerPi-custom pieces kept because ftl lacks them

#### R-B1. Countdown / timer readout `.countdown`
- What and why: a big tabular timer that shows m:ss (h:mm:ss over an hour), optional tenths in the last 10 s, and a leading "+" in overtime. Any show-control, sports, exam or cooking app needs the same states: armed (cued, not running), running, paused, held at 0, alert 1 (warning), alert 2 (critical), overtime. Each item carries its *own* alert colours (an operator sets amber/red per cue).
- TimerPi today: `public/css/timerpi.css` ~79-116 (plus display copies ~312, ~771, ~807):
  ```css
  .tp-clock[data-state="paused"] { color: var(--warning-text, var(--warning)); }
  .tp-clock[data-state="overtime"], .tp-clock[data-state="alert2"] { color: var(--clock-alert2, var(--danger-text, var(--danger))); }
  .tp-clock[data-state="alert1"] { color: var(--clock-alert1, var(--warning-text, var(--warning))); }
  @keyframes tp-overtime-pulse { 0%,100% { opacity: 1 } 50% { opacity: .72 } }
  .tp-clock[data-state="overtime"], .tp-clock[data-state="alert2"] { animation: tp-overtime-pulse 1s steps(2, jump-none) infinite; }
  ```
  JS copies `cue.alertColor1/2` onto `--clock-alert1/2` (`public/src/timerpi.js` ~168). The text is painted per frame by JS.
- Proposed ftl: `<time class="readout countdown" data-state="running|armed|paused|held|alert1|alert2|overtime" aria-live="off">09:59</time>`; modifiers `.countdown-xl`; child `<span class="countdown-sign">+</span>` and `<span class="countdown-tenths">.4</span>` (smaller, muted). Tokens: `--countdown-armed-fg` (muted), `--countdown-paused-fg` (warning), `--countdown-held-fg` (muted), `--countdown-alert1` (warning), `--countdown-alert2` (danger), `--countdown-overtime` (= alert2), `--countdown-pulse-opacity` (.72), `--countdown-pulse-period` (1s). Per-item colours are just inline `--countdown-alert1/2`. Paused also gets a non-colour cue (blinking colon off, or a ⏸ prefix via `::before`). Tabular numerals and `white-space: nowrap` built in; width in `ch` so digits never shift.
- Accessibility: the element must not be a live region while running (it changes every frame). Provide a sibling `.visually-hidden[role=status]` pattern the app updates on state changes only ("Overtime", "2 minutes left"). States must not rely on colour alone: overtime has the "+" sign, paused the glyph, alerts the pulse.
- Reduced motion: pulse off under `prefers-reduced-motion` and `[data-motion=reduced]`, replaced by a static outline or heavier weight. (TimerPi exception: display screens always animate — an app opt-out like `.countdown.is-always-animate` is needed.)
- Nearest ftl: `.readout` (only the look and size, no states), `.meter`/`.gauge` (bar, not digits), `.timer`? none. `.status` colours exist but not for digits, and there is no overtime notion.

#### R-B2. Day bar `.timeline-bar`
- What and why: one row showing the whole day as proportional segments (sessions, striped breaks), a "now" needle and start/end times below. Event, broadcast, rail and shift apps need a glanceable "where are we in the day".
- TimerPi today: `public/css/timerpi.css` ~201-238 (`.tp-daybar`, `-seg`, `-needle`, `-scale`), markup `templates/fragments/daybar.html`:
  ```css
  .tp-daybar { position: relative; height: 2.6rem; background: var(--surface-2); border: 1px solid var(--border); overflow: hidden; }
  .tp-daybar-seg { position: absolute; top: 0; bottom: 0; border-inline-start: 1px solid var(--hairline); }
  .tp-daybar-seg.is-break { background: repeating-linear-gradient(45deg, color-mix(in srgb, var(--border) 55%, transparent) 0 .5rem, transparent .5rem 1rem); }
  .tp-daybar-seg.is-done { filter: saturate(.4) brightness(.75); }
  .tp-daybar-needle { position: absolute; top: 0; bottom: 0; width: 2px; background: var(--accent); }
  ```
- Proposed ftl: `<div class="timeline-bar" role="img" aria-label="Day: 09:00–17:30, now 11:06, in Coffee">` with `<span class="timeline-seg" style="--start: 12%; --size: 8%" data-kind="break">Coffee</span>`, states `.is-done`, `.is-active`, `[data-kind=break]` (pattern, not colour only), optional per-segment `--seg-color`; `<span class="timeline-now" style="--at: 23%">`; `<div class="timeline-scale"><time>09:00</time><time>17:30</time></div>`. Tokens `--timeline-height`, `--timeline-break-pattern`, `--timeline-now-color`, `--timeline-done-filter`. Labels hide under a container width (`@container`) rather than a viewport query.
- Accessibility: whole bar is one `role="img"` with a text summary the app keeps current (segments are decorative); segments that are links/buttons must be focusable with visible focus.
- Reduced motion: the needle moves by position updates only; no transition. Fine as is.
- Nearest ftl: `.progress`/`.meter` (one value, no segments), `planner` docs (multi-row Gantt with lanes, too heavy and vertical space hungry), `.steps` (equal-width, no time scale).

#### R-B3. Table row drag-to-reorder
- What and why: drag a row by its handle to reorder (running orders, playlists, priorities), with a drop line showing where it will land.
- TimerPi today: `public/css/timerpi.css` ~911-933, JS `initDragReorder` (`public/src/timerpi.js` ~1700-1770, pointer events, works with touch):
  ```css
  .tp-cue-pos { touch-action: none; cursor: grab; }
  .tp-dragging { opacity: .45; outline: 2px dashed var(--accent); outline-offset: -2px; }
  .tp-drag-line { position: absolute; height: 3px; background: var(--accent); border-radius: 2px; pointer-events: none; z-index: 30; }
  ```
- Proposed ftl: `td.drag-handle` (grip glyph via `--drag-handle-glyph`, `cursor: grab`, `touch-action: none`, tap-sized), `tr.is-dragging` (lifted look: dashed outline + reduced opacity), `tr.is-drop-before` / `tr.is-drop-after` drawing the drop line with a `box-shadow` on the row (no extra element, so no absolute positioning inside a scroller), `body.is-dragging` / `.table.is-dragging` for global `cursor: grabbing` and `user-select: none`. Tokens `--drop-line-color`, `--drop-line-size`.
- Accessibility: document a keyboard alternative (handle is a button; Alt+↑/↓ or a "Move to…" menu item) and an `aria-live` announcement ("Moved Coffee to position 3"). Never drag-only.
- Reduced motion: no animated row travel; rows jump on drop.
- Nearest ftl: `.dashboard` widget drag (grid tiles, not table rows); `.sortable` list? not present for tables.

#### R-B4. Row states: past, next, per-row colour/category
- What and why: ordered tables (agendas, playlists, queues) need "done" rows dimmed and struck, the "next" row marked, and a row category colour (breaks vs sessions; a user-picked row colour).
- TimerPi today: `public/css/timerpi.css` ~191-193 and ~1110-1112:
  ```css
  tr.tp-row-past { opacity: .55; }
  tr.tp-row-past .tp-cue-label { text-decoration: line-through solid color-mix(in srgb, var(--muted) 55%, transparent) 1px; }
  tr.tp-row-next td { border-block-start-style: solid; }
  .tp-cuetable tr.tp-row-break { --tp-row-accent: var(--accent-2, var(--warning)); background: color-mix(in srgb, var(--tp-row-accent) 7%, transparent); }
  .tp-cuetable tr.tp-row-break > td:first-child { box-shadow: inset 3px 0 0 var(--tp-row-accent); }
  ```
- Proposed ftl: `tr.is-past` (muted text + strike on `.row-title` or first text cell, never just opacity — opacity fails contrast), `tr.is-next` (leading marker in `--row-next-marker`, dashed, distinct from `.is-active`'s solid), `tr[style="--row-color: #…"]` / `tr.has-row-color` painting a 3px leading bar and a 6–8 % tint that composes with hover/selection/active the way `--_cell-paint` already does for cell states. Tokens `--row-past-fg`, `--row-past-strike`, `--row-next-marker`, `--row-color-tint` (7%).
- Accessibility: `aria-current="step"` on the active row is documented by the app; past/next need text equivalents (visually-hidden "done", "next") since strike-through isn't announced.
- Reduced motion: static.
- Nearest ftl: `tr.is-active` / `.is-selected` / striping. No past/next, and a custom row colour fights the theme's row marker `box-shadow` (we had to restate it).

#### R-B5. Two-tap "armed" destructive button
- What and why: a delete that needs a second tap within ~2.5 s instead of a modal; standard in live-operation UIs where modals cost time.
- TimerPi today: `public/css/timerpi.css` ~868-873, JS `confirmDelete` (`public/src/timerpi.js` ~767-790):
  ```css
  .tp-del-armed { outline: 2px solid var(--danger-text, var(--danger)); outline-offset: 2px;
    background: color-mix(in srgb, var(--danger-text, var(--danger)) 18%, transparent) !important; color: var(--text) !important; }
  ```
  (`!important` is needed to beat `.btn-danger` / `.btn-icon` paint.)
- Proposed ftl: `.btn.is-armed` (works on `.btn-danger`, `.btn-icon`, `.btn-ghost`): double ring + danger tint, optional label swap via `<span class="btn-armed-label">Tap again</span>` shown only when armed, and a countdown sweep `::after` driven by `--armed-ms`. Token `--btn-armed-ring`, `--btn-armed-bg`.
- Accessibility: app sets `aria-describedby` to a live "Tap again to delete" text when armed; armed state must change the accessible name or description, not just colour.
- Reduced motion: no sweep animation, static ring only.
- Nearest ftl: `.btn-danger` (one state), modal confirm (`dialog.modal`), `.toggle-btn` (persistent toggle, wrong semantics).

#### R-B6. Server-confirmed toggle on a solid `.btn`
- What and why: BLANK (blackout all screens) is a toggle whose pressed state comes from the server snapshot. It's a `.btn-danger` with `aria-pressed`. ftl paints `[aria-pressed="true"]` only for `.btn-clear` / `.btn-icon.is-clear` (dist ~5296) and `.key`; a solid button shows no pressed look.
- TimerPi today: `public/css/timerpi.css` ~347: `#tp-blank[aria-pressed="true"] { outline: 3px solid var(--danger, #ff4444); outline-offset: 3px; }`.
- Proposed ftl: `.btn[aria-pressed="true"]` for every variant: inset/sunken look (`box-shadow: inset 0 2px 0 rgb(0 0 0 / .35)`), ring in the variant colour, plus a non-colour marker (a leading dot/LED via `--btn-pressed-glyph`). `.btn[aria-pressed][aria-busy="true"]` for "sent, waiting for the server" (spinner) so a server-confirmed toggle can show pending.
- Accessibility: `aria-pressed` is the state; the busy variant uses `aria-busy`.
- Reduced motion: spinner only spins with motion allowed (as `.is-saving` does).
- Nearest ftl: `.toggle-btn` (checkbox-backed, client state; we need server truth), `.btn-clear[aria-pressed]` (only the clear variant).

#### R-B7. Inline small colour dot
- What and why: a tiny colour indicator inside a dense table cell (alert colours), clickable to open a swatch popover. `.swatch` is tap-sized by design (`min-inline-size: max(var(--tap-min), var(--swatch-size))`), so in a 48 px row it doubles the column width.
- TimerPi today: `public/css/timerpi.css` ~1102-1108:
  ```css
  .tp-alert-dot { display: inline-block; inline-size: .85rem; block-size: .85rem; border-radius: 50%;
    background: var(--swatch, var(--accent)); border: 1px solid rgb(128 128 128 / .5); padding: 0; cursor: pointer; }
  button.tp-alert-dot { inline-size: 1.1rem; block-size: 1.1rem; margin: 0; }
  ```
- Proposed ftl: `.color-dot` (non-interactive, `--swatch`, `--color-dot-size: .85em`, border token shared with `.swatch`) and `button.color-dot` whose *visible* size stays small but whose hit area grows to `--tap-min` with a transparent `::before` (the same technique forms use for rating stars), so dense tables keep their width and touch still works.
- Accessibility: button form needs an accessible name ("Alert 1 colour: amber"); the colour name, not just the hue.
- Reduced motion: static.
- Nearest ftl: `.swatch` (tap-sized radio), `.status` dot (fixed semantic colours, not arbitrary), `.avatar` (too big).

#### R-B8. App-bar-aware toast region
See A7 — the request is the `--app-bar-height` token and `.app .toast-region` default offset, so apps drop magic numbers.

---

## M: Admin and setup pages
dashboard, Screens page, box settings and the moderator Audience panel
(2026-10-07). Checked against `third_party/ftl-themes` `dist/core.css` and

### A. Gaps and bugs hit while swapping

#### M-A1. `.grid` is auto-fit only: a lone card stretches to the full row
- Repro: `<div class="grid" style="--grid-min:19rem"><article class="panel">…</article></div>`
  inside a 72rem page. The one panel is 72rem wide (TimerPi's screen card
  then draws a page-wide 16:9 preview). `core.css` `.grid` uses
  `repeat(auto-fit, minmax(min(100%, var(--grid-min)), 1fr))`.
- Workaround: `public/css/app.css:29`
  `:is(#room-grid, #live-grid, #tp-gal, #tp-waiting-list) > * { max-inline-size: 28rem; }`
- Proposed fix: add `.grid.is-fill` (or a `--grid-repeat: auto-fill` token)
  switching to `auto-fill`, so columns keep their width and empty tracks stay
  empty. Galleries of cards want fill; dashboards of tiles want fit.

#### M-A2. `.grid-sm` / `.grid-lg` change the column minimum, not the gap
- CONTRACT.md "Layout, levels…" says "`-sm`/`-lg` tighten or loosen the gap
  (`.stack-sm`, `.cluster-lg`, `.grid-sm`)", but `core.css` sets
  `.grid-sm { --grid-min: 11rem }` / `.grid-lg { --grid-min: 22rem }`.
- Proposed fix: correct the doc (or add `--grid-gap` modifiers and keep
  the min-width ones under a distinct name).

#### M-A3. `.otp` has no intrinsic width: it collapses in a flex row
- Repro: `<form class="row is-items-end"><div class="field"><label …/>
  <div class="otp"><input class="otp-input" …><span></span>×6</div></div> …</form>`.
  `.otp` is `container-type: inline-size`, so its shrink-to-fit width is 0
  and `--_otp-cell` (`min(cell-size, 100cqi …)`) resolves to ~0: six
  hairline boxes.
- Workaround: `public/css/app.css:30` `#pair-form .otp { inline-size: 18rem; max-inline-size: 100%; }`.
- Proposed fix: give `.otp` a default
  `inline-size: calc(var(--_otp-n) * var(--otp-cell-size, 2.75rem) + (var(--_otp-n) - 1) * var(--_otp-gap))`
  with `max-inline-size: 100%`, so it is its natural size anywhere and still
  shrinks on narrow screens.
- Also worth documenting: with `maxlength="6"` a pasted "123 456" is cut to
  "123 45" by the browser before any script sees it. TimerPi drops
  `maxlength` and strips non-digits on `input` (`public/src/event.js`, pair
  form). Suggest the docs say so, or show `maxlength="7"` + sanitising.

#### M-A4. Editable-cell states only exist under `.table td`
- All of `.is-editable/.is-editing/.is-saving/.is-saved/[aria-invalid]` are
  scoped `.table td…` / `.table tr > td…` (`core.css` ~12216–12353).
  An inline-editable title outside a table (TimerPi's screen-card name,
  `public/src/screens.js` `card()`, `.tp-inline-edit` at `app.css:21-23`)
  can't use them, so TimerPi keeps its own dashed-underline look there.
- Proposed fix: an element-agnostic `.editable` (+ the same state classes)
  for headings/labels, with the table selectors as the specialised case.

#### M-A5. `.toggle-group` can't hold two-line labels or wrap
- Repro: three `label.toggle-btn` radios with a title and a one-line
  description each (`templates/screens.html` "What is this screen?"). The
  group is `inline-flex`, never wraps, and `.toggle-btn` centres one line
  (`align-items/justify-content: center`), so a 32rem modal overflows.
- Workaround: `public/css/app.css:200-202` (`.tp-kind-group`: full-width flex,
  column-direction buttons, stacked on phones, reset of the joined negative
  margin when stacked).
- Proposed fix: `.toggle-group.is-block` (full width, equal items,
  start-aligned multi-line content) and `.toggle-group.is-stacked` (vertical
  join: block-start margins/radii instead of inline ones), or a
  `.toggle-btn-title` / `.toggle-btn-hint` pair styled like
  `.list-item-title/-meta` (hint muted, but readable on the checked fill).

#### M-A6. `.list-item-title` as a direct child forces a column layout
- `.list-item:has(> .list-item-title) { flex-direction: column }`: a row with
  a title plus trailing buttons (preset rows, recent events) turns into a
  vertical stack. TimerPi wraps title+meta in a `div` (`event.js`
  `initHome` recent list) or drops `.list-item-title` (presets).
- Proposed fix: document the wrapper pattern, or add `.list-item-body`
  (flex: 1; column) so title/meta stack while actions stay on the row.

#### M-A7. `.accordion-panel` text is `--muted`
- Moderation queues (`moderate.js` `queue()`) put entries in
  `.accordion-panel`; plain text there is muted. A `.list` inside restores
  `--text`, but a bare paragraph does not. Suggest `--accordion-panel-fg`
  defaulting to `--text` (muted is a choice for FAQ copy, not for content).

#### M-A8. No `.text-danger` (or any text-tone utility)
- Only `.text-muted` / `.text-truncate` exist. Error text inside a table cell
  (mesh radio error) became `.status.status-error`, which works but adds a
  dot. Suggest `.text-danger/-warning/-success` reading the `*-text` tokens.

#### M-A9. `.status-rec` exists in one theme only
- `blue-future.css` styles `.status-rec`; core doesn't define it. TimerPi used
  it for "Live" on a poll; now `.status.status-error`. Either promote
  `status-rec` into core (recording semantics, pulsing dot) or drop it from
  the theme.

### B. TimerPi-custom pieces kept because ftl lacks them (feature requests)

#### M-B1. Large button `.btn-lg`
- What/why: one big primary action per door/room card (touch at a lectern,
  "Continue", "Open room", "Moderate this room").
- Current: `public/css/app.css:18`
  `.tp-btn-lg { min-height: 2.9rem; padding-inline: 1.25rem; font-size: 1.05rem; justify-content: center; }`
  used in `templates/home.html`, `templates/event.html`.
- Proposed: `.btn-lg` (and `.btn-block` for full width) reading
  `--btn-padding-lg`, `--btn-font-size-lg`, `--btn-min-height-lg`
  (default `max(var(--tap-min), 2.9rem)`), mirroring `.input-lg`.
- A11y: keeps `--tap-min`; focus ring unchanged.
- Nearest ftl: `.input-lg` exists for inputs only; `.btn-go` is a colour
  variant, not a size.

#### M-B2. Segmented alphanumeric code input with a separator (XXXX-XXXX)
- What/why: event codes are 8 characters from a Crockford-like alphabet,
  shown as `K7QP-M3XB`; typing should auto-insert the dash and upper-case.
- Current: `templates/home.html` `#join-code` (`.input.input-lg.mono.tp-code-input`),
  `public/css/app.css:17`
  `.tp-code-input { text-transform: uppercase; letter-spacing: .12em; font-size: 1.4rem; }`,
  formatting in `public/src/event.js` `initHome()`.
- Proposed: extend `.otp` with `data-groups="4-4"` (or `--otp-groups`) that
  draws a separator cell, and `.otp.is-alnum` (`inputmode="text"`,
  `autocapitalize="characters"`, `text-transform: uppercase`). Same single
  real input, so paste/autofill keep working.
- A11y: one labelled input; the separator is decorative (no text node).
- Nearest ftl: `.otp` assumes digit cells with no grouping and numeric
  input mode in the docs.

#### M-B3. Scaled live layout preview tile
- What/why: a mini map of where widgets sit on a 12-column board, at the
  screen's aspect (16:9 or 9:16), with absolutely positioned tiles.
- Current: `public/css/app.css:188-190` `.tp-scr-prev` (relative,
  `aspect-ratio: 16/9`, `.is-portrait` 9/16 with fixed height),
  `.tp-scr-tile` (absolute, accent-tinted, tiny label); built in
  `public/src/screens.js` `preview()`.
- Proposed: `.ratio.is-canvas` (children not forced to fill/cover) plus
  `.ratio-item` positioned by `--x/--y/--w/--h` percentages, and a
  `--ratio-item-bg/-border/-fg` token set. Portrait: `.ratio-9x16` with a
  `--ratio-max-block` cap.
- A11y: decorative preview (`aria-hidden`), with the layout name as text
  elsewhere on the card.
- Nearest ftl: `.ratio` makes every child fill and `object-fit: cover`,
  which is right for media, wrong for a positioned canvas.

#### M-B4. Thumbnail image
- What/why: small framed preview of an uploaded venue map in a settings row.
- Current: `public/css/app.css:31`
  `.tp-map-thumb { max-height: 4rem; max-width: 8rem; object-fit: contain; border: 1px solid var(--border); border-radius: var(--radius, 4px); }`
- Proposed: `.thumb` (+ `-sm/-lg`) with `--thumb-size`, `--thumb-border`,
  `--thumb-radius`, `--thumb-bg`, `object-fit: contain` by default and
  `.thumb.is-cover`.
- A11y: needs `alt`; no change.
- Nearest ftl: `.avatar` is round/square and cover-cropped (wrong for maps);
  `.ratio` fills.

#### M-B5. Key/glyph legend
- What/why: the layout editor header lists its affordances ("⠿ drag to
  move · ◢ resize · ⚙ settings · arrow keys nudge").
- Current: `templates/screens.html` `.tp-edit-legend`,
  `public/css/app.css:209-210` (`display:flex; flex-wrap:wrap; gap`; glyph
  in `--accent`).
- Proposed: `.legend` / `.legend-item` (wrapping row, muted text,
  `<kbd>` or `.legend-glyph` styled with `--kbd-*`), usable for keyboard
  shortcut strips too.
- A11y: glyphs `aria-hidden`, words carry meaning.
- Nearest ftl: `<kbd>` is styled but there is no inline legend layout.

#### M-B6. Sticky tabs
- What/why: Run / Audience / Screens tabs stay on top while the room page
  scrolls.
- Current: `public/css/app.css:214`
  `.tp-room-tabs { position: sticky; top: 0; z-index: 3; background: var(--bg); }`
- Proposed: `.tabs.is-sticky` reading `--sticky-top` and
  `--tabs-sticky-bg` (opaque), the same z-index token as `.table.is-sticky`.
- A11y: none beyond tabs' own.
- Nearest ftl: only `.table.is-sticky` exists.

#### M-B7. Corner close/remove button on a card
- What/why: "Forget screen" is a danger-tinted × in the card's top-end
  corner.
- Current: `public/css/app.css:182`
  `.tp-scr-forget { position:absolute; top:.4rem; right:.4rem; z-index:1; --btn-close-fg: var(--danger-text, var(--danger)); --btn-close-fg-hover: var(--on-danger,#fff); --btn-close-bg-hover: var(--danger); }`
  plus `.tp-scr { position: relative }` and header padding to clear it.
- Proposed: `.card-dismiss` / `.panel-dismiss` (logical `inset-inline-end`,
  RTL-safe) and `.btn-close.is-danger`; the card reserves
  `padding-inline-end` for it.
- A11y: `aria-label` names the item ("Forget screen Main screen").
- Nearest ftl: `.btn-close` exists but only placed inside `.modal-header`
  /`.toast`; no danger tint, no card placement.

#### M-B8. Page width containers
- What/why: the main column of operator pages (72rem) and narrow forms
  (32rem), with page padding.
- Current: `public/css/app.css:6-7` `.tp-page`, `.tp-narrow`.
- Proposed: `.container` already exists (`--container-max`, 64rem) but has
  no block padding and only one width; add `.container-narrow`
  (`--container-narrow-max`) and a `--container-pad` token, then TimerPi can
  drop `.tp-page/.tp-narrow`.
- Nearest ftl: `.container` (one width, `width: min(100% - 2rem, …)`, no
  vertical rhythm).

---

## D: Display screens, boards and the audience phone page

### A. Gaps and awkwardness hit while swapping

#### D-A1. No full-screen overlay wrapper for `.empty-state`

- **Repro:** show "LINK DOWN" over a running display. `.empty-state.is-offline`
  is an inline block; there is no class to lay it over the viewport with a scrim.
- **What we did:** our own shell `.tp-offline` (`public/css/timerpi.css`,
  "Offline overlay shell"): `position: fixed; inset: 0; z-index: 40; display:
  none; place-items: center; background: color-mix(in srgb, var(--bg) 88%,
  transparent)` + `.is-visible { display: grid }`; content is
  `.empty-state.is-offline` (`templates/fragments/d-chrome.html`, `frag-offline`).
- **Proposed fix:** `.empty-state.is-overlay` (or an `.overlay` wrapper):
  fixed, viewport-covering, centred, `--overlay-scrim` (default
  `color-mix(in srgb, var(--bg) 88%, transparent)`), `--overlay-z`, toggled
  by `[hidden]` like `.splash`, with `.is-contained` for a panel. Same motion
  rules as `.splash[hidden]`.

#### D-A2. `.splash` is always themed; no "dark stage" variant

- **Repro:** `<div class="splash">` on `html[data-theme="material"]` paints
  `--splash-bg: var(--surface)` (white). A signage waiting/pairing screen must
  be black on every theme (a white TV in a dark room is the bug), and then the
  theme's `--text` / `--muted` / `--accent-text` (dark on light themes) vanish
  on black. A `.readout` inside the splash also takes `--accent-text`
  (material: #6200ee on black, ~2.4:1).
- **What we did:** `.tp-display .splash { --splash-bg: #000; --splash-fg:
  #f2f2f2; --splash-message-fg: #a0a0ac; --splash-title-fg: #a0a0ac;
  --readout-fg: #f2f2f2; … }` (timerpi.css "Display splashes").
- **Proposed fix:** `.splash.is-stage` (or `data-surface="stage"`) that sets
  a dark background and contrast-checked light ink tokens for every theme
  (theme may override `--splash-stage-bg/-fg/-muted`), and re-points
  `--readout-fg`, `--accent-text` inside it.

#### D-A3. `.splash-art` for a logo needs four token overrides

- `--splash-art: url(logo) center / contain no-repeat; --splash-art-mask: none;
  --splash-art-radius: 0; --splash-art-motion: none` — otherwise the logo is
  cut into a ring and spun. Proposal: `.splash-art.is-image` (or `<img
  class="splash-art">` support) that drops mask/radius/motion.

#### D-A4. `.connection` lamp size does not follow `--connection-size`

- **Repro:** set `--connection-size: 2.2vh` on a TV chip: text grows, lamp
  stays `--lamp-size, 0.7rem`.
- **What we did:** `:is(.b-link, .tp-dv-link) { --connection-size: clamp(.9rem,
  2.2vh, 1.6rem); --lamp-size: .8em; }`.
- **Proposed fix:** default the lamp to `var(--lamp-size, 0.8em)` inside
  `.connection` so it scales with its own text.

#### D-A5. `.connection` and `.empty-state.is-loading` motion cannot be forced on

- **Repro:** a TV whose OS reports reduced motion: the live lamp pulse, the
  reconnecting spin and the loading hourglass flip are all inside
  `@media (prefers-reduced-motion: no-preference)` and keyed off
  `:root:not([data-motion="reduced"])`. There is no `data-motion="always"`.
- **Proposed fix:** see B4 (`html[data-motion="always"]`).

#### D-A6. No filled `.badge` variant

- **Repro:** the stage urgency chip (OVERTIME +0:12 / WRAP UP / ALERT 2) must
  be a solid danger/warning fill to read at distance. `.badge-danger` /
  `.badge-warning` only tint the text on `--surface-2`.
- **What we did:** keep `class="badge tp-state-chip"`; timerpi.js sets
  `data-state`, our CSS maps it to `--badge-bg / --badge-fg / --badge-border`
  (timerpi.css "U2-1"). Example:
  `.tp-state-chip[data-state="alert1"] { --badge-bg: var(--clock-alert1, var(--warning)); --badge-fg: var(--on-warning, #1A1200); }`
- **Proposed fix:** `.badge-solid` modifier (`.badge-danger.badge-solid` →
  `--badge-bg: var(--danger); --badge-fg: var(--on-danger)`), plus an
  outline variant (`.badge-outline`) for HELD/PAUSED.

#### D-A7. `.meter` has no single-colour mode

- **Repro:** a day-progress bar on `.gauge-linear > .meter` paints the
  green/amber/red level bands, so the afternoon reads as "danger".
- **What we did:** `.b-w-dayprogress { --meter-low: var(--accent);
  --meter-mid: var(--accent); --meter-high: var(--accent); }`.
- **Proposed fix:** `.meter.is-solid` (fill `var(--meter-fill, var(--accent))`,
  no bands) for progress-like meters.

#### D-A8. `.gauge-linear` sizes are rem-only

- `--gauge-linear-label-room` (1.35rem) and the tick offsets assume a fixed
  label size; scaling the gauge with a container (cq units) means setting
  `--meter-thickness`, `--gauge-linear-label-size` and
  `--gauge-linear-label-room` together (app.css `body.b-board
  .b-w-dayprogress`). Proposal: derive `label-room` from `label-size`
  (`calc(1.4 * var(--gauge-linear-label-size))`) so one token scales it.
- A `.gauge-mark.is-target` with text overlaps the 0%/100% labels near the
  ends; we use an empty target mark as a "now" needle. A
  `.gauge-mark.is-needle` (line + head, no label) would say that directly.

#### D-A9. `.schedule-item` rows are tap targets even when not interactive

- `min-block-size: max(var(--tap-min), …)` applies to read-only signage rows;
  a 5-row schedule in a short TV tile overflows. We set `--tap-min: 0px` on
  `.b-board .schedule-list`. Proposal: apply `--tap-min` only when the row has
  a `.stretched-link` / button (`:has(a, button)`).
- Any non-whitespace text node between `<time class="schedule">` and
  `.schedule-body` (we had an en space for copy/paste readability) becomes an
  anonymous grid item and pushes the body into the third column. Worth a doc
  line.

#### D-A10. Dashboard edit mode vs fixed placement (edit-mode classes over a fixed-placement grid)

- **Trial:** `.dashboard.is-editing` on `#b-grid`, `.widget` on each
  `.b-widget`, `.widget.is-dragging`, `.widget-placeholder` as the drop ghost,
  keeping our `grid-template-columns: repeat(12, …)`,
  `grid-template-rows: repeat(var(--b-rows), minmax(0, 1fr))` and inline
  `grid-column: x / span w; grid-row: y / span h`.
- **Result:** placement did not leak (inline grid lines beat `.widget
  { grid-column: span … }` and the ≤480px `1 / -1`; our template beats
  `.dashboard`'s). Screenshots at 1280×900, 820×1000 and 1080×1920 keep the
  fixed 12-column layout. Kept.
- **Workarounds needed** (the gap):
  1. The tier media rules set `--dashboard-cols: 2` (481–900px) and `1`
     (≤480px); the edit-mode column guides read `--dashboard-cols`, so on a
     narrow screen they draw 2 columns over our 12. We pin
     `body[data-editing] .b-grid { --dashboard-cols: 12 }`.
  2. `.widget` forces `display: flex; flex-direction: column;
     container-type: inline-size` — that changes how our tile bodies lay out
     and drops our `container-type: size` (we size type in `cqh`). So the
     classes are toggled **only while editing** (board.js `setEditing`), never
     on a live display.
  3. `.widget-placeholder` has `min-block-size: var(--dashboard-row-min,
     9rem)`, taller than a 1-row tile; we set `--dashboard-row-min: 0px`.
  4. `.dashboard-dirty` only shows inside the `.dashboard`; our save state
     lives in a toolbar above the grid, so it is not used.
- **Proposed fix:** `.dashboard.is-fixed`: no tier collapse, no
  `auto-flow: dense`, guides from `--dashboard-cols` only, `.widget` keeps the
  app's display/containment (move `display:flex` to `.widget:not(.is-fixed
  *)` or a `.widget-stack` helper), and documented `--x/--y/--cols/--rows`
  placement (`grid-column: calc(var(--x) + 1) / span var(--cols)`). Also allow
  `.dashboard-dirty` to sit in a `.dashboard-bar` outside the grid via
  `[data-dashboard-for]` or a plain `.is-editing` ancestor.

---

### B. Feature requests: TimerPi pieces kept because ftl lacks them

#### D-B1. Word cloud with a physics layout

- **What/why:** audience word cloud on a TV: words grow with votes, drift to
  the centre and settle without overlapping; new words fade in at the edge on
  their own heading. Must look alive at distance.
- **Now:** `public/src/audtiles.js:258-405` — `updateCloud`, `sizeCloud`,
  `stepCloud` (spring per frame):
  `const PULL = 0.012; const DAMP = 0.82; const GROW = 0.08; const FILL = 0.42;`
  and rotations `ANGLES = [0,0,0,0,0,-90,90,-90,90,-30,30,-15,15]`.
  CSS `timerpi.css` `.b-cloud-word { position: absolute; left: 50%; top: 50%;
  font-size: 100px; transform-origin: 0 0; opacity: 0; transition: opacity
  .9s ease-out, color .6s; }` with `[data-tone="1|2"]` and `.is-top`.
- **Proposed ftl:** `<div class="word-cloud" role="list" aria-label>` with
  `<span class="word-cloud-word" role="listitem" style="--weight:.7;
  --x:…; --y:…; --rotate:-90deg" data-tone="1">` — ftl paints (size from
  `--weight` between `--word-cloud-min/-max`, tones from the chart palette,
  `.is-top`, `.is-new` enter), the app positions. Optional
  `assets/js/word-cloud.js` for the spring layout.
- **A11y/motion:** list semantics with each word's count in an accessible
  name; reduced motion = static spiral placement (unless display mode, B4).
- **Why nearest falls short:** `.tag-cloud`/`.badge` lists flow inline; no
  weighted sizing, no absolute layout, no rotation.

#### D-B2. Ranked live Q&A wall (animated reorder, spotlight, answered/leaving)

- **What/why:** questions sorted by votes; when the order changes cards slide
  to their new slot; the moderator's spotlight card gets a heavy border; an
  answered card turns green, holds 1.4 s, fades 1 s and the gap closes half
  way through. New cards rise from the bottom; cards that don't fit fade.
- **Now:** `audtiles.js:158-250` (`updateWall`, `layoutWall`:
  `it.el.style.transform = \`translateY(${y}px)\``, `WALL_HOLD_MS = 1400`,
  `WALL_FADE_MS = 1000`); CSS `timerpi.css` `.b-qa-item { position: absolute;
  … transition: transform .8s cubic-bezier(.2,.8,.2,1), opacity 1s ease,
  border-color .4s, background-color .4s; } .b-qa-item.is-spot {
  border-width: 6px; border-color: var(--accent); } .b-qa-item.is-answered
  {…success…} .b-qa-item.is-leaving { opacity: 0 !important; }`.
- **Proposed ftl:** `.ranked-list` (`<ol>`), items `.ranked-item` placed by
  `--slot-y` (app sets) with a token transition `--ranked-move`, states
  `.is-spotlight`, `.is-answered`, `.is-leaving`, `.is-new`, a vote slot
  `.ranked-votes`; optional FLIP helper in JS.
- **A11y/motion:** `<ol>` order = rank order in the DOM (screen readers get the
  rank, transforms are visual only); `aria-current` for the spotlight; answered
  announced by text, not colour. Reduced motion = instant reorder, opacity
  only.
- **Why `.feed` falls short:** `.feed` is an append-only scrolling log with
  `.is-new` slide-in; it has no ranks, no reorder motion, no spotlight, no
  leave choreography, and it scrolls (a TV can't scroll).

#### D-B3. Poll results count-up and staggered reveal

- **What/why:** when results show, bars and numbers count up together over
  900 ms with an ease-out, each option staggered; this is the "reveal moment"
  in a room.
- **Now:** `audtiles.js:72-150` (`updateBars`, `countTo`: rAF loop,
  `const ease = (t) => 1 - Math.pow(1 - t, 3); const dur = 900;` writes
  `r.bar.value` and `"${n} · ${pct}%"`); CSS `.b-poll-reveal { animation:
  b-poll-reveal .6s ease-out both; }`.
- **Proposed ftl:** `progress.progress.is-counting` / `.meter` with
  `--value-from` → `--value` transition via `@property --value` (registered
  `<number>`), plus `.readout[data-count]` using a CSS counter on the same
  registered property so the number and bar animate from CSS alone;
  `--stagger-index` → `animation-delay`.
- **A11y/motion:** final values set immediately on the element (`value`,
  text) so AT reads the result; motion is decoration. Reduced motion = jump.
- **Why nearest falls short:** `.progress` / `.meter` animate width only via
  a fixed 0.06 s transition; no number count-up, no stagger.

#### D-B4. Display / kiosk motion mode + per-element enter/exit choice

- **What/why:** signage must animate even when the playback device (a TV
  stick, a Pi with a default OS profile) reports `prefers-reduced-motion`;
  operators pick a tile's enter/exit (fade / slide / pop) and its duration.
- **Now:** `timerpi.css` "Tile entrance/exit":
  `.b-anim-in-fade { animation: b-anim-fade var(--b-anim-ms) ease-out; }`
  … `.b-anim-out-pop { animation: b-anim-pop-out var(--b-anim-ms) ease-in forwards; }`
  with five `@keyframes`; board.js `animOf(w)` reads `opts.anim/animMS`.
  Every ftl motion we use (`.connection` pulse/spin, `.splash-art`,
  `.empty-state.is-loading`, `--motion-enter`) is off under reduced motion
  with no override.
- **Proposed ftl:** `html[data-motion="always"]` (signage): all
  `@media (prefers-reduced-motion: no-preference)` blocks also match
  `:root[data-motion="always"]`. Plus enter/exit vocabulary:
  `[data-enter="fade|slide|pop"] [data-exit=…]` with `--motion-enter-duration`
  and `.is-entering/.is-exiting` classes (or `--motion-enter-fade/-slide/-pop`
  tokens next to `--motion-enter`).
- **A11y:** `always` is for unattended screens only; doc should say never use
  it on interactive pages.
- **Why nearest falls short:** `--motion-enter/--motion-exit` are one shorthand
  each, and gated by the media query.

#### D-B5. Blackout/standby, stage alert edge glow, flash/blink attention

- **What/why:** (a) operator blackout: a TV goes pure black with a dim logo and
  STANDBY; (b) alert phases paint the whole stage edge so the state is seen in
  peripheral vision; (c) "Flash" blinks presenter timers to grab attention.
- **Now:** (a) `timerpi.css:337-344` `.tp-blanked { position: fixed; inset: 0;
  z-index: 60; background: #000; … } body[data-blanked="true"] .tp-blanked {
  display: grid; }`; (b) `timerpi.css` `.tp-display-stage[data-state="alert1"]
  { box-shadow: inset 0 0 0 .45vmin var(--clock-alert1, var(--warning)); }`
  (alert2/overtime `.7vmin` danger); (c) `timerpi.css:1119-1120`
  `.b-js-clock.is-flash { animation: b-blink 1s steps(1) infinite; }
  @keyframes b-blink { 50% { opacity: .12; } }` and `tp-overtime-pulse`.
- **Proposed ftl:** (a) `.splash.is-standby` (black, dim art, muted label, no
  motion); (b) `.stage-edge[data-level="warn|danger"]` / a `--stage-alert`
  token rendering an inset ring scaled in `vmin`; (c) `.is-attention`
  (steps blink) and `.is-urgent` (pulse) states for `.readout`, honouring B4.
- **A11y/motion:** blink ≤ 3 Hz (WCAG 2.3.1); state also carried by text
  (chip label). Reduced motion = static outline unless `data-motion="always"`.
- **Why nearest falls short:** `.splash` is a loading screen (themed, A2);
  `.alert` is a block message, not a viewport edge; no blink state.

#### D-B6. Full-screen message band with operator colour

- **What/why:** the operator sends "WRAP UP" / "Mic 2 off" to the stage; a
  band over the lower third, huge type, operator-chosen colour on its leading
  edge, readable over the clock.
- **Now:** `timerpi.css:246-260` `.tp-stage-message { position: absolute;
  inset-inline: 0; bottom: max(12vh, env(safe-area-inset-bottom, 0px)); … }`
  and `.tp-stage-message > div { font-size: clamp(1.7rem, 6vh, 6rem); …
  border-inline-start: 4px solid var(--accent); }` (U2-4 override), JS sets
  `borderInlineStartColor`; board tiles `.b-msg`.
- **Proposed ftl:** `.banner.is-overlay` / `.lower-third` with `--banner-edge`
  (app colour), `--banner-size`, placement tokens (`bottom|center`), enter/exit
  per B4.
- **A11y:** `role="status"`; colour is the edge only, text carries meaning;
  ftl checks text contrast on the band surface.
- **Why nearest falls short:** `.toast` is small/transient/corner; `.alert` is
  in-flow and themed at body size.

#### D-B7. QR join card

- **What/why:** a corner card on screens/editors: mini QR, `<host>.local`, URL,
  "Control room: …"; legible at distance, pixel-crisp QR.
- **Now:** `timerpi.css:532-545` `.b-join-card { position: fixed;
  inset-block-end: max(1rem, env(safe-area-inset-bottom)); … }` and
  `.b-join-card img { width: 7.4vh; image-rendering: pixelated; }`; board tile
  `frag-b-joinqr` (`templates/fragments/b-audience.html:49-57`) with
  `.b-js-joinqr { background: #fff; aspect-ratio: 1 }`.
- **Proposed ftl:** `.qr-card` (`.qr-card-code` img/svg with a forced light
  quiet zone `--qr-bg: #fff`, `--qr-size`, `.qr-card-label`, `.qr-card-url`
  mono, `.is-corner` fixed placement with safe-area insets).
- **A11y:** the URL is always printed next to the code (no QR-only path);
  `alt` names the destination.
- **Why nearest falls short:** `.card` has no quiet-zone/pixelated image rules;
  themes that tint images or set dark surfaces break scanning.

#### D-B8. Daysheet / print schedule table with a NOW needle row

- **What/why:** a whole-day running order on a TV and on paper; a thin "NOW"
  row moves between the rows at the current time; past rows struck through.
- **Now:** `templates/display_variants.html:140-160`
  (`.tp-daysheet tr#d-daysheet-needle td { color: var(--tp-accent,
  var(--accent)); font-weight: 800; letter-spacing: .2em; font-size: .6em; }`,
  `.tp-row-past` line-through), print rules `:230-260`, JS inserts/moves the
  needle row (`:383-386`).
- **Proposed ftl:** `.table` row states `.is-past`, `.is-current` and a
  `tr.table-now` divider row (accent rule + label), plus print styles for
  `.table.is-daysheet` (black on white, `print-color-adjust: exact` for the
  current row). Or a `.schedule-list.is-table` variant with `.schedule-now`.
- **A11y:** the needle row is `aria-hidden` (the current row carries
  `aria-current="time"`).
- **Why nearest falls short:** `.schedule-list` has `is-past`/`aria-current`
  but is card rows, not a dense table, and has no "now" divider; `.table` has
  no time states.

#### D-B9. Signage safe area, fit padding and viewport-scaled root type

- **What/why:** TVs overscan; content must stay inside a 4vh/4vw safe area,
  with operator "fit" choices (cover/contain/shadow); all rem sizes must scale
  with the output (720p/1080p/4K identical).
- **Now:** `timerpi.css` `html:has(body.tp-display) { font-size: 1.5vh; }`;
  `display_variants.html:50-64` `.tp-dv-screen { position: fixed; inset: 0;
  padding: max(4vh, env(safe-area-inset-top, 0px)) max(4vw, …) …; }` and
  `body[data-fit="cover|shadow"]` variants; `.tp-display-stage` padding (C5).
- **Proposed ftl:** `html[data-surface="signage"]`: root size from
  `--signage-root` (default `1.5vh`, `1.5vw` when rotated), `.safe-area`
  utility with `--safe-inset-block/-inline` (default 4vh/4vw, max with
  `env(safe-area-inset-*)`), `[data-fit]` presets, and no scrollbars.
- **A11y:** signage is read-only; operator pages keep the OS root size.
- **Why nearest falls short:** ftl's text-size options are fixed multipliers
  of the browser default; no viewport-relative root, no overscan inset.

#### D-B10. Dashboard grid with fixed x/y placement (no reflow)

- See A10: we keep our own 12 × `--b-rows` canvas
  (`app.css` `body.b-board .b-grid { grid-template-rows: repeat(var(--b-rows,
  8), minmax(0, 1fr)); }`, board.js `applyGeometry`: `tile.style.gridColumn =
  \`${w.x + 1} / span ${w.w}\``) because `.dashboard` reflows by tier and
  back-fills (`dense`). Request `.dashboard.is-fixed` with `--x/--y` placement,
  a stretched row template (`--dashboard-rows`), no tier collapse, and a
  size-container `.widget` (TV type is sized in `cqh`).
