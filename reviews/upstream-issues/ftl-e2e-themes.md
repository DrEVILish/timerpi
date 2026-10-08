# ftl-themes: theme bugs found in the TimerPi end-to-end test (2026-10-08)

Found on the E2E test server (48 screens, one per theme). Each is theme
artwork or theme CSS we cannot fix from `timerpi.css` without overriding the
theme.

**Filed as:** [ftl-themes issue #71](https://github.com/DrEVILish/ftl-themes/issues/71), owner direction, 2026-10-09.

## Display artwork over content (screens)

- **steampunk — `.readout` clock artwork crosses the digits.** The wall-clock
  readout draws a decorative diagonal needle line straight across the
  digits ("21:0…") on the Clock / walk-in layouts (E2E S32, TV and phones).
  Repro: `/d/<room>?view=board&tpl=clockroom` with `data-theme="steampunk"`.
  Proposed fix: draw the needle behind the text (`z-index: -1` on the
  pseudo-element inside an isolated stacking context) or keep it to the
  readout's edge, outside the glyph box.
- **vaporwave — the sun decoration overlaps the join QR.** The background sun
  sits over the bottom-right of the page, where the Audience main layout puts
  the join QR tile; the stripes cut through the code (E2E S38). Proposed fix:
  keep body artwork under `.widget`/panel backgrounds (`z-index` below
  content) or let apps opt out per element (`--theme-art: none`).
- **teletext — edge block artwork covers tile edges.** The coloured edge
  blocks along the screen border paint over the outermost tiles' borders and
  first characters (E2E S34, presenter layouts). Proposed fix: inset the
  artwork by the safe-area padding, or expose a token for its size so apps
  can pad the grid.

## Operator shell and components (operator pages)

Worked around in `public/css/app.css` / `timerpi.css` (scoped by theme name,
marked "E2E"); the workarounds can go once these are fixed upstream.

- **liquid-glass inherits ios-skeuomorphic / ios-flat chrome it doesn't
  draw.** The built bundle keeps the parents' rules scoped to
  `html[data-theme="liquid-glass"]`: the 1.15rem black status-bar bezel
  (`.app-bar { border-top }` + `::before/::after` dots and battery) on the
  capsule bar; `.app-bar .nav-brand { order: 99; flex: 1 0 100% }` (brand on
  its own row); `.app-bar .nav-item { margin-inline: -0.375rem }` (items
  overlap); the dark-blue alert tokens on `.modal` (`--text/--label-fg:
  #fff`, embossed white labels on the light glass modal, a "Done" pill as
  `.btn-close`); the dark panel-header inks (`--muted: #eef2f7`,
  `--danger-text: #ffd0d2`, so `.status` and "danger" headings vanish); and
  `.list-item.is-active { --list-item-fg: #fff }`. Repro: any app shell page
  with `data-theme="liquid-glass"`, open any `.modal`. Proposed fix: reset
  these in the liquid-glass layer (or stop inheriting parent chrome rules
  and inherit tokens only).
- **liquid-glass shell assumes a one-line, 3rem bar.** `.app-main` spans all
  shell rows with `padding-top: 4.6rem` and the bar is sticky over it, so
  any bar that wraps (or is taller) covers the page top and takes its
  clicks; the decorative 12rem sidebar costs a dashboard ~220px at 1440.
  Proposed fix: let `.app-main` start in its own row (or size the top
  padding from `anchor(--app-bar-anchor bottom)`), and a token to drop the
  sidebar (`--app-rail-display: none` is overridden by
  `--app-rail-empty-display: block`).
- **ios-flat** shows the same leaks: white embossed breadcrumbs on its white
  bar, steel-gradient (skeuomorphic) `.panel-header` behind grey text.
- **ios-skeuomorphic** sets its dark tab-bar inks (`--tab-fg-active: #fff`)
  on every `.tabs`, so the active tab on the light page is white on light
  grey. Proposed fix: scope the tab-bar inks to `.app-status .tabs`.
- **blue-future, bloomberg: `.status-idle` is painted as "ready" (green /
  accent-2).** Apps that use `status-idle` for "offline / not running" read
  as live. Proposed: keep `status-idle` neutral (core: `--muted`) and add a
  `.status-ready` for the recorder meaning. (TimerPi now uses
  `.status.text-muted`.)
- **imac-g3: default `.btn` is teal text on translucent teal** (room-card
  "Screens"/"GO", room-table actions) and `.input` placeholders are white on
  white. Proposed fix: a darker `--btn-fg` / `--input-placeholder` in the
  theme.
- **Dark themes whose `.btn-primary` is a tint with `--btn-fg: var(--accent)`**
  (alienware, matrix, pipboy, tokie, tron, winamp-classic; also hot-wheels'
  outlined GO): any app that brands the accent (TimerPi's purple #7C3AED)
  gets 1–3.5:1 text. Proposed: derive the label from `--accent-text` (a
  contrast-checked text variant) instead of `--accent`.
- **hot-wheels: the checkered `.app-status` artwork sits behind the status
  text**, which is unreadable. Proposed: a solid strip under the text.
- **core / tokens have no icon sprite** (`/ftl/dist/icons/core.svg` 404) —
  TimerPi falls back to `generic.svg`; a theme-less default sprite name would
  help.
