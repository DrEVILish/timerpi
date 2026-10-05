> **Shelved draft (2026-10-05).** `blue-future` is the TimerPi default theme. A custom TimerPi theme will be designed later, and this spec is input for that work, not a current requirement.

# TimerPi Theme Spec

> **Owner requirements (non-negotiable — read first).**
> 1. Theme name is **"TimerPi"** (proposed slug `timerpi`, so `html[data-theme="timerpi"]`).
> 2. **Ultra professional, no gimmicks, every feature 100% functional.** No glow-for-glow's-sake, no textures that cost legibility, no decorative animation that carries state alone.
> 3. **Primary Purple `#7C3AED`** is the only UI brand accent: links, active states, `.btn-primary`, `.btn-go`, progress/meter fill, selected-row marker.
> 4. **Secondary Green `#22C55E` is LOGO ONLY** — it appears inside `public/img/timerpi.svg` (minute hand) and **nowhere else**: never UI chrome, never text, never a state fill, never a message swatch. The theme must not set any token to `#22C55E`.
> 5. **Orange is banned entirely** from the UI. No orange accent, hover, border, or decoration anywhere.
> 6. **SPEC-mandated timer state colors `#ffaa00` (warning/alert1) and `#ff4444` (critical/alert2) are state colors, not brand.** The theme renders them **exactly** (no lightening/darkening the fills) wherever timer state needs a warning/danger tone: clock digits, state chip, stage edge, lamps, message swatches. They must not leak into default buttons, links, or decoration.
> 7. **Dark-first broadcast console**: operator-room desk + TV/stage confidence monitors. Dark backgrounds, high-contrast text, tabular numerals on every time readout, visible focus rings everywhere, `prefers-reduced-motion` respected (paired `reduce` block for every animation).

## Theme story

One paragraph: this theme belongs in a **dark vision-mixing / showcaller desk and on the stage confidence monitors hanging off it**. The operator sits in a blacked-out room glancing between stage and screen under show stress; the audience and presenters read the same state from a 1080p TV across the room. So the console is a near-black instrument panel with hairline structure, one loud control (GO, in brand purple), giant tabular clocks that never reflow, and eight timer states that are each distinct at a glance — while the TV stage is the same palette blown up to a no-scroll fullscreen with safe-area padding, cursor-hide compatibility, and OVERTIME plus the wrap-up message impossible to miss. If it ever reads as a marketing site, a neon arcade, or a light theme wearing sunglasses, it is wrong.

## References and brand source

- `third_party/ftl-themes/CONTRACT.md` — token + component contract this spec builds against.
- `third_party/ftl-themes/docs/authoring-a-theme.md` — how the ftl developer builds it (one `theme.css`, root-scope `--*` overrides only, `color-scheme: dark`).
- Format reference: `themes/xbmc/README.md` + `themes/cue-lab/README.md` (story + core values + tell-tales checklist).
- TimerPi usage: `docs/archive/CONTRACT-UI.md` (`.app` shell, `.readout` / `.meter` / `.transport` / `.btn-go` / `.lamp`, tables, modals; element ids in §6; `data-state` values `idle armed running paused held overtime alert1 alert2` in §6; brand decision §7; Fix-3 amendments §9).
- Logo: `public/img/timerpi.svg` — dark neutrals `#14161a`-family, ink `#E8E6E3`, purple ring + purple "Pi" (`#7C3AED`), green minute hand (`#22C55E`, logo-only).

## Core values

1. **One loud thing: GO.** Purple, filled, biggest hit-target in the transport. Nothing else glows, pulses, or fills purple.
2. **Flat, hairline chrome everywhere else.** 1px borders, small radius, no gradients on surfaces, no card shadows on the cue list. Depth cues cost scanning time.
3. **State is color + shape + text, never color alone.** Every timer state pairs its color with a distinct lamp treatment, chip label, and clock treatment so color-vision deficiency or a washed-out projector never collapses two states.
4. **Amber/red are alarms, not paint.** `#ffaa00` / `#ff4444` appear only where the engine says warning/critical (or a message explicitly marked caution / "WRAP UP!"). Any amber/red pixel elsewhere is a bug.
5. **Numbers are tabular and huge where it matters.** `font-variant-numeric: tabular-nums` on every readout, table time column, day-bar scale, and rate echo; clock digits never shift width as they tick.
6. **Dark console, TV-legible.** Body text ≥7:1, muted/secondary ≥4.5:1 (see token table); focus ring always visible; reduced-motion always honored.

## Token table

All values are exact hexes the developer puts under `html[data-theme="timerpi"]` (plus `color-scheme: dark`). Contrast ratios below are WCAG 2.x (relative-luminance) pairs computed against the surface the text actually sits on; all were computed, not estimated.

| Token | Value | Sits on / role | Contrast | Notes |
|---|---|---|---|---|
| `--bg` | `#0B0D12` | Page background (deepest layer) | — | Blue-tinted near-black; vignette optional, still |
| `--surface` | `#14161A` | Cards, panels, modals (body text substrate) | — | Matches logo clock face `#1E2126`-family |
| `--surface-2` | `#1E2126` | Inputs, nested panels, table stripes, transport | — | Logo clock-face fill, reused as recessed surface |
| `--text` | `#EDEBE6` | Primary text | **15.2:1** on `--surface`, **13.6:1** on `--surface-2`, **16.3:1** on `--bg` | Warm-neutral ink near logo `#E8E6E3`, lifted for ≥7:1 everywhere |
| `--muted` | `#A7B0BF` | Labels, captions, table headers, hints | **8.3:1** on `--surface`, **7.4:1** on `--surface-2`, **8.9:1** on `--bg` | Carries essential text: clears 4.5:1 on both surfaces with margin |
| `--accent` | `#7C3AED` | Primary interactive FILL: `.btn-primary`/`.btn-go` fill, active states, progress fill. Small accent text uses `--accent-text`, never raw accent | 3.4:1 on `--bg` (never body text) | Brand purple, exact. Never used as small text on dark — fails 4.5:1 by design |
| `--accent-2` | `#A78BFA` | Hover, second series, complementary highlight; large/bold accent text | **7.1:1** on `--bg` | Light purple, safe as text on dark |
| `--accent-text` | `#B79CFF` | Accent-colored small text on `--surface` | **8.0:1** on `--surface` | Same hue family as accent, lightened to pass 4.5:1 |
| `--on-accent` | `#FFFFFF` | Text on accent fills | **5.7:1** on `--accent` | White (not `--bg`): reads on any theme luminance |
| `--warning` (fill) | `#ffaa00` | SPEC state fill: alert1, caution lamp/chip/stage | **10.2:1** on `--bg` as text-on-dark | Exact SPEC hex, unmodified |
| `--on-warning` | `#1A1200` | Text on warning fills | **9.7:1** on `#ffaa00` | Near-black warm; white-on-amber would fail |
| `--warning-text` | `#FFC44D` | Warning-hue small text on `--surface` | **11.4:1** on `--surface` | Same hue as fill, lightened past 4.5:1 (per contract § fills-vs-text) |
| `--danger` (fill) | `#ff4444` | SPEC state fill: alert2/overtime, destructive buttons | **5.7:1** on `--bg` as text-on-dark | Exact SPEC hex, unmodified |
| `--on-danger` | `#140607` | Text on danger fills | **5.8:1** on `#ff4444` | White on `#ff4444` is only 3.4:1 — must be near-black, never white |
| `--danger-text` | `#FF8A8A` | Danger-hue small text on `--surface` | **8.0:1** on `--surface` | Lightened red for `.status`, badges, field errors |
| `--success` (fill, state-only) | `#34D399` | Success fills: running lamp, confirm buttons. **Not `#22C55E`.** | **10.1:1** on `--bg` | Emerald chosen to be unmistakably *not* the logo green; success appears only as live state/confirm, never chrome |
| `--on-success` | `#06281C` | Text on success fills | **8.2:1** on `#34D399` | Near-black green |
| `--success-text` | `#5FE3AC` | Success-hue small text on `--surface` | **11.3:1** on `--surface` | Lightened emerald for `.status`/trends |
| `--border` | `#2E3542` | Default visible borders | — | Visible on `--surface` without shouting |
| `--hairline` | `#1D222C` | Quiet dividers, table row rules | — | Below `--border`; `data-contrast="high"` pulls to `--border` per core |
| `--focus` | `#A78BFA` | Focus ring color | **7.1:1** on `--bg` | Light purple so the ring reads on dark AND on purple fills |
| `--focus-ring` | `0 0 0 2px var(--bg), 0 0 0 4px var(--focus)` | Double ring (dark keyline + light edge) | — | Visible on any surface including accent fills |
| `--radius` | `0.3rem` | Base corner radius | — | Hardware-console small radius; pills only where core demands (badges, lamps stay square-ish per component) |
| `--font` | `-apple-system, BlinkMacSystemFont, "Segoe UI", "Helvetica Neue", Arial, sans-serif` | UI stack | — | Platform face: most legible at small dense sizes; nothing vendored |
| `--font-mono` | `"SFMono-Regular", "JetBrains Mono", Consolas, "Roboto Mono", monospace` | Time/numeric stack — **always with `font-variant-numeric: tabular-nums`** | — | Monospace + tabular figures; no webfont dependency (offline-safe) |
| `--flare` | `#7C3AED` | The single decorative extra, used sparingly | — | Same as accent: the theme gets no second brand color |
| `--overlay-bg` | `rgba(4, 6, 10, 0.78)` | Modal/drawer scrim | — | Dark enough that modal text keeps contrast |
| `--selection-bg` / `--selection-fg` | `#7C3AED` / `#FFFFFF` | Text selection | 5.7:1 | Brand selection, legible |
| `--input-placeholder` | `#7C8595` (≈5:1 on `--surface-2`) | Placeholder text | ≥4.5:1 on `--surface-2` (verify in build) | Muted but still readable; never below 4.5:1 |
| `--input-caret` | `#A78BFA` | Caret | — | Visible in dark fields |
| `--density` | `0.9` | Console density | — | Dense but looser than cue-lab's 0.85: more TV-handoff legibility, still fits the list |
| `--app-*` | See "App shell" | Layout tokens | — | `shellAware: true` (L1 layout, L0-safe degrade) |

Rules the developer must not break: no token set to `#22C55E`; no token in the orange hue band except the two SPEC state fills (`#ffaa00`, `#ff4444`) and their `-text` companions; `--on-*` never points at `--bg`; `color-scheme: dark` declared once.

## Per-component look

All overrides via `--<component>-*` at root scope (contract § "How a theme overrides a component"). Never `background`/`color` on a base component selector.

### App shell (`.app`: bar / rail / main / status)

- **L1 layout, L0-safe.** Sets `--app-*` so the shell visibly arranges: slim top bar, **no rail** (collapse to nothing: `--app-rail-display: none`), content-first main, quiet status strip. Without shell markup (L0) it degrades to a correct recolor, never a broken gutter.
- **Bar:** `--app-bar-bg: var(--surface)`, `--app-bar-fg: var(--text)`, 1px `--border` rule under it (no amber/purple rule — the bar is neutral; brand lives in `.tp-brand` wordmark + hero + primary triggers only). Height `3rem`, padding `0.4rem 0.9rem`. Brand mark: logo SVG as-is (green hand intact inside the artwork) + `TimerPi` wordmark with purple "Pi" via `.tp-brand` (app-owned class, already in markup). Nav active item: `--nav-item-fg-active: #FFFFFF` with a 2px `--accent` underline (inherited from xbmc language, recolored purple) — underline, never a filled pill.
- **Main:** `--app-main-bg: transparent` over `--bg` (optional very subtle radial vignette, still, ≤1.2:1 — home-theatre dim-room read, never a visible gradient band). Padding `0.9rem`; dashboard grid owns its own columns app-side.
- **Status:** `--app-status-bg: var(--surface)`, `--app-status-fg: var(--muted)`, 1px top rule. Session code / peers in mono tabular; join credential (`Session 0000-0000`) must clear 3:1 on `--bg` — it uses `--text` at Fix-3 weight, not dimmed muted.
- **Tiers:** phone/tablet use core one-column shell as-is; touch hit areas obey `--tap-min` (core handles). XL: centered cap `--app-max: 1800px`; gutter art optional stage-plot hairline grid within ~1.3:1 of `--bg`, still, motion only under `no-preference`.

### Buttons (incl. `.btn-go` — the loudest control)

- **Base `.btn`:** `--btn-bg: var(--surface-2)`, `--btn-fg: var(--text)`, `--btn-border: var(--border)`, flat, `--btn-radius: var(--radius)`, 600 weight on primary/GO only. Hover: 1px `--accent-2` edge + faint purple wash — no lift, no shadow.
- **`.btn-primary`:** solid `--accent` fill, `--on-accent` white text (5.7:1). Hover: lighten toward `--accent-2` edge; active: inset 2px bottom keyline. Focus: `--focus-ring` double ring.
- **`.btn-go` (transport + row-level):** the single loudest element on the page. Solid `--accent` fill, white bold caps `GO`, `font-size: clamp(1.4rem, 3vh, 2.2rem)` in `.transport` (per CONTRACT-UI §9a glue — theme must not fight the sizing, only the paint), `min-inline-size: 7rem`. It is the **only** element allowed a glow: `--go-shadow-hover: 0 0 0 3px rgba(124,58,237,.35), 0 0 22px 4px rgba(124,58,237,.5)` on hover only, off under `prefers-reduced-motion`. Row-level `.btn-go` (cue list `btn-sm btn-go`) is the same fill at small size, no glow. `:disabled` (no next cue): desaturate to `--surface-2` fill with `--muted` text, no glow — a disabled GO must never look armed.
- **`.btn-danger`:** `--danger` fill with `--on-danger` near-black text (5.8:1) — never white-on-red. `.btn-ghost`/`.btn-icon`: transparent, `--text` glyphs, hover hairline frame.
- **Sizes:** no `.btn-lg` exists in core — hero/GO sizing stays app-side (`.tp-btn-lg`, `.transport .btn-go`); theme only paints.

### Readouts (giant tabular clock)

- `.readout` / `.tp-clock` / `#tp-clock` / `#d-clock`: `--readout-fg: var(--text)` at rest (neutral white digits — color is reserved for state), weight 700, `font-family: var(--font-mono)`, `font-variant-numeric: tabular-nums`, `letter-spacing: 0.02em`. Unit (`.readout-unit`) in `--muted` at 0.55em.
- State recolor happens **only** through `[data-state]` (JS-set, CONTRACT-UI §6): idle/armed/running stay `--text` except where the state table below says otherwise; `paused`/`held` go `--warning-text`; `alert1` goes `#ffaa00`/`--warning-text` (digits may use the raw fill at TV size — 10.2:1 on `--bg` — but small badges use `--warning-text`); `overtime`/`alert2` go `#ff4444`/`--danger-text` (same split). Digits never use purple or green.
- Overtime digits get a leading `+` (JS `clockView` already computes sign — theme adds `+` styling/weight, not the math) and the pulse below; pulse is enhancement only, state is carried by color + `+` + chip label.

### Meters / progress

- `.meter` / `#tp-meter` / `.tp-progress-meter` / `.tp-daybar-seg`: track `--meter-track: #08090C` (darker than `--bg` so the bar reads as recessed), `--meter-border: var(--hairline)`, `--meter-radius: 0.2rem`, fill `--meter-low: var(--accent)` — progress fill is **brand purple**, not green. No green/amber/red banding on the elapsed-progress meter (those hues are reserved for timer state). Peak tick `--meter-peak: var(--accent-2)`.
- Day-bar segments (`.tp-daybar-seg`): neutral `--surface-2` blocks with `--hairline` dividers; `.is-break` hatched (repeating-linear-gradient 45°, `--hairline` on transparent — pattern, not color); `.is-done` dimmed to 45% opacity; `.is-active` gets the 2px `--accent` top edge + `--accent` left marker. Needle (`#tp-daybar-needle`): 2px `--text` line with purple cap — visible on every segment tone.

### Lamps + state chips (`idle|armed|running|paused|held|overtime|alert1|alert2` — each DISTINCT at a glance)

Color + lamp shape + chip text always travel together. Lamp base: square (`--lamp-radius: 2px`), unlit `#2b2b2b`, no bloom except where noted.

| `data-state` | Lamp | `.status` chip | Clock | Distinguisher (why it can't be confused) |
|---|---|---|---|---|
| `idle` | off (dark) | `--muted`, text `READY` | `--text` | Only state with a dark lamp + muted chip |
| `armed` | `--accent` solid + soft purple glow (2nd and last glow allowed; GO hover is the other) | `--accent-text`, text `ARMED` | `--text` | Only purple lamp |
| `running` | `--success` solid (`#34D399`, never `#22C55E`), no glow | `--success-text`, text `RUNNING` | `--text` | Only green lamp; running digits stay white — green is the lamp/chip, not the clock |
| `paused` | `--warning` hollow (outline ring, dark center) | `--warning-text`, text `PAUSED` | `--warning-text` digits | Hollow vs held's solid; static (no blink) |
| `held` | `--warning` solid + inset bottom keyline | `--warning-text` bold, text `HELD` | `--warning-text` digits + stage inset `box-shadow` (Fix-3 §9c) | Solid + bold + inset edge vs paused's hollow; the only state with the inset frame |
| `overtime` | `--danger` solid + slow pulse (see Motion) | `--danger` fill chip, `--on-danger` text `OVERTIME −m:ss` (counting up with `+`) | `#ffaa00`-no — `#ff4444` digits with `+` prefix | Only pulsing red; only state with `+`/count-up digits + filled red chip |
| `alert1` | `--warning` solid diamond (rotated square via `clip-path` — shape override allowed) | `--warning` fill chip, `--on-warning` text `WRAP UP`/`⚠1` | `#ffaa00` digits | Only diamond lamp; amber digits without `+` (vs overtime's red `+`) |
| `alert2` | `--danger` solid round lamp | `--danger` fill chip, `--on-danger` text `⚠2` / cue label | `#ff4444` digits, no `+` unless also overtime | Only round red lamp; red digits without count-up (vs overtime) |

If overtime and alert2 coincide, overtime wins the chip (it carries the count) and both reds agree on the digits — document the precedence in code comments.

### Cue table rows (selected / running)

- `.table`: tabular numerals throughout (`font-variant-numeric: tabular-nums`); header `--table-head-fg: var(--muted)` uppercase 0.06em tracking (muted clears 4.5:1, so headers stay legible — unlike themes that dim headers into decoration); `--table-head-rule: var(--border)`; sticky header keeps `--surface` opaque (no translucency under scrolled rows).
- Selected row: `--row-selected-bg: rgba(124,58,237,0.16)` + inset `0.25rem 0 0 var(--accent)` left marker. Running/active row: `--row-active-bg: rgba(124,58,237,0.22)` + same marker in `--accent-2` + bold cue label. The two are adjacent intensities of one hue — selected is "armed", running is "live". Alert badges (`.tp-alert-badge`, `⚠1/⚠2`) use the exact SPEC fills with `--on-warning`/`--on-danger` text; per-cue `alertColor1/2` inline styles (JS-copied to `--clock-alert1/2`) are honored verbatim, never remapped.
- Row action buttons keep the button language at `btn-sm`; delete stays `--danger` fill + near-black glyph (icon buttons need the same `--on-danger` treatment, not white).

### Modals, tabs, fields/inputs

- **Modal/drawer:** `--modal-bg: var(--surface)`, 1px `--border` frame, `--modal-shadow: 0 24px 64px rgba(0,0,0,.6)` (the one shadow allowed off-list — scrims need separation), header `--modal-header-bg: var(--surface-2)` with tabular title. Close/min/max grouped at header end per v5 close-button tokens.
- **Tabs (`.tabs/.tab`):** text tabs with 2px `--accent` underline for active (`--tab-fg-active: #FFFFFF`), `--muted` for the rest; no pill fills. Closable tabs and overflowing tabbars scroll sideways on touch tiers.
- **Fields:** `.label` uppercase 0.06em `--muted` (legible at 8.3:1); `.input/.select/.textarea` on `--input-bg: #0B0D12` (darker than `--surface-2` so fields read recessed) with `--input-border: var(--border)`, focus border `--accent-2` + `--focus-ring`. `.is-invalid`/`aria-invalid`: `--danger` border + `--danger-text` message + recoloured ring (core handles with `--input-error-*` tokens — theme supplies the hues, not the mechanics). Sizes via `.input-sm/.input-lg` alongside the control (no `.select-sm` exists). Sliders (rate): purple thumb on dark groove, 44px touch target via core `--tap-min`.
- **Switches/checkboxes:** checked = `--accent` track/thumb with white knob; focus ring always on.

### Toasts

- `.toast-region` (top-layer `popover="manual"`): `--toast-bg: var(--surface-2)`, 1px `--border` + 3px left state edge (neutral `--accent` for info, `#ffaa00` caution, `#ff4444` danger), `--text` body, tabular numbers, wraps long URLs instead of spilling. Toasts sit above modals (top layer), line up with the content edge on XL.

### Day-bar

Covered under Meters: neutral segments, hatched breaks, dimmed done, purple active edge, white needle with purple cap, mono tabular scale (`DayStartFmt … TotalFmt total … DayEndFmt`) in `--muted` at ≥4.5:1. The day-bar is 100% functional chrome — no decorative gradient over it.

### Message overlay (`#d-message` / `.tp-stage-message`, `#tp-live-msg`)

- Hidden by default (`hidden` attr); shown message: full-width stage band, `--surface-2` at 96% over dimmed stage, 4px left edge in the message's own color (default `--accent`; author may pick `#ffaa00` caution or `#ff4444` "WRAP UP!" — the only sanctioned non-state use of the SPEC hues, per CONTRACT-UI §9b; green/`#22C55E` is absent from the picker).
- Text: `--text` at stage scale (see TV rules), never the edge color as body text (amber/red small text would fail — the edge is the color carrier, the text stays white). `Theme` (`value=""`) opt-out inherits theme text for maximum legibility.

## TV / stage rules (`/d/` display, `#d-stage`)

The display page is L0 (tokens only, no `.app` shell) — the theme must carry it on tokens + element rules alone.

- **1080p legibility minimums (viewport-height units so it scales to any panel):** main clock `#d-clock` ≥ `26vh` (overtime/alert2 ≥ `30vh` — alarm states get bigger, never smaller); cue label ≥ `5vh` semibold; NEXT block ≥ `2.6vh`; status line (session code/hostname) ≥ `2.2vh` at `--text` (Fix-3: join credential ≥3:1 — white on `--bg` is 16:1, compliant with margin).
- **Safe-area padding:** `4vh 4vw` inset on `#d-stage` (overscan on consumer TVs eats the outer ~3.5%); nothing informative within the outer `3.5vh/vw`.
- **No-scroll:** stage is `100dvh` flex column, `overflow: hidden`; center clock grows, NEXT/speaker/status never push the clock off-screen; long labels truncate with ellipsis (one line) — the operator shortens the label, the theme never scrolls.
- **Cursor-hide compatibility:** no hover-only affordance on the stage; the `cursor: none` kiosk mode loses nothing (all state is painted, none revealed on hover).
- **OVERTIME + wrap-up prominence:** `[data-state="overtime"]` stage gets a 6px `#ff4444` top edge + red clock + filled `OVERTIME` chip; a shown `#d-message` with a red/amber edge takes over the lower third at ≥ `6vh` bold with its own scrim so the clock and the message never compete in the same band. Reduced-motion: pulse becomes a static double edge (see Motion).
- **LINK DOWN overlay (`.tp-offline`):** hairline panel on the scrim, `LINK DOWN` in `--danger-text` + plain-language line in `--text`; must read at 3m.

## Variants

Per contract § "Palette variants" (`html[data-theme="timerpi"][data-variant="…"]`, specificity `(0,2,1)` wins over the base). List in the theme README; picker offers each under the theme.

1. **`standard` (base, no `data-variant`):** the palette in the token table. Default for every room.
2. **`high-contrast` (`data-variant="high-contrast"`):** for washed-out projectors, bright rooms, and low-vision operators. Deltas only: `--text: #FFFFFF` (16.3:1→21:1 class), `--muted: #C7CEDA` (≥10:1 on `--surface`), `--border: #4A5468`, `--hairline: var(--border)`, `--focus: #C4B5FD` with 3px ring, `--row-selected-bg: rgba(124,58,237,0.30)`, `--row-active-bg: rgba(124,58,237,0.38)`, day-bar `.is-done` dim floor raised to 60% opacity, lamp unlit raised to `#3a3a3a` (still clearly "off" vs any lit state). Accent/state fills unchanged (they already pass; changing SPEC fills per variant is forbidden).

A future `deuteranopia-safe` variant (shape/pattern emphasis, blue/white alarm alternative) is explicitly out of scope for v1 — the v1 answer to color-vision deficiency is the shape+text redundancy in the state table, not a second palette.

## Icon guidance

- **Generic sprite is fine.** No TimerPi-specific icon set is required; the xbmc sprite (`icon-player-*`, `icon-plus`, `icon-trash`, `icon-chevron-*`, `icon-microphone`) reskins via currentColor.
- **Redraws needed for TV-size legibility** (stroke ≥2px at 24px grid, no 1px detail that vanishes at 3m): `icon-player-pause` (bars too thin — widen to 5px with 3px gap), `icon-player-stop` (small square — enlarge to 70% of grid), `icon-player-skip-back/forward` (chevron+bar reads as blur — separate the bar by 2px), `icon-microphone` (grille dots vanish — replace with 3 slots), alert triangles used in `⚠1/⚠2` badges (need heavier 2.5px stroke + tabular numeral, not the stock thin triangle).
- Icons are `currentColor` glyphs on flat buttons — never purple-filled, never green, never orange.

## Tell-tales of an inauthentic result

Concrete, checkable — any single failure rejects the theme:

1. GO button is not the single loudest element (something else glows, pulses, or uses a heavier fill).
2. Any orange pixel outside the `#ffaa00`/`#ff4444` state contexts (search the bundle: orange-hue hexes other than the two SPEC fills = fail).
3. Any `#22C55E` pixel anywhere in the theme CSS (logo-only green; `grep 22C55E` must return nothing).
4. Clock digits not tabular (`font-variant-numeric: tabular-nums` missing on `.readout`, `.table`, `.mono` time columns) or digits shift width while ticking.
5. Two timer states indistinguishable with color removed (screenshot → grayscale: idle/armed/running/paused/held/overtime/alert1/alert2 must still read via lamp shape + chip label + clock treatment).
6. `held` renders identical to `paused` (same lamp fill without the solid-vs-hollow + bold + inset distinction) or identical to `idle` (muted).
7. Disabled GO looks pressable (keeps purple fill or glow).
8. White text on `#ff4444` or `#7C3AED` anywhere (both fail 4.5:1 — must be the specified near-black/white `--on-*`).
9. Muted text under 4.5:1 on either surface (measure `--muted` on `--surface` and `--surface-2`).
10. Missing or 1px-only focus ring on any control (Tab through transport → table actions → import form → theme picker).
11. Animation without a paired `prefers-reduced-motion: reduce` off-switch, or state carried by animation alone.
12. Flat cue list inside a shadowed/glossy card; rounded pill active-nav instead of the underline; gradient button fills.
13. TV stage scrolls at 1080p, content inside the outer 3.5% safe area, or status/session line under ~2.2vh.
14. Light-theme leak: `color-scheme` anything but `dark`, or any surface lighter than `#1E2126`.

## Open questions for the ftl developer

1. **Slug `timerpi`?** Contract guarantees `dataTheme === slug`. I propose slug `timerpi` (`html[data-theme="timerpi"]`). *Recommendation: accept; keep `xbmc` as the loader fallback in `base.html` until the theme ships, then repoint the default link.*
2. **Success emerald `#34D399` instead of logo green — acceptable?** The owner bans `#22C55E` from UI but a running/confirm state needs a green. *Recommendation: accept `#34D399` + `#5FE3AC` text variant; never `#22C55E`; note the distinction in the theme README so nobody "harmonizes" them later.*
3. **Held vs paused both amber — is solid-vs-hollow + bold + inset enough?** Fix-3 already paints held amber; splitting hues further (e.g. held → purple) would contradict the engine's warning semantics. *Recommendation: keep both amber, carry the distinction in shape/label/edge per the state table; verify in grayscale.*
4. **Overtime pulse: 1.2s ease-in-out opacity/edge pulse, disabled under reduced motion — OK?** *Recommendation: accept; pulse is enhancement-only (color + `+` + chip already carry the state), with a static double-edge fallback in the `reduce` block.*
5. **No vendored font — system stacks only?** Broadcast appliances are offline; a webfont is a 404 risk for zero legibility gain at console sizes. *Recommendation: ship system stacks + tabular-nums, no `@font-face`; document the choice in the header comment like `tron` does.*
6. **No rail (`--app-rail-display: none`) — or a slim decorative rail for shellAware identity?** TimerPi's dashboard already columns app-side; a painted rail would steal list pixels. *Recommendation: no rail; let the bar rule + GO + clocks be the identity.*
7. **No `tint`, no accent swatches in v1?** A user-picked tint could smuggle orange/green past the brand ban. *Recommendation: decline both for v1; the high-contrast variant is the only supported deviation.*
8. **Email bundle (`dist/email/timerpi/`)?** The build auto-generates transactional emails per theme. TimerPi sends no emails. *Recommendation: let the build emit them (zero cost), ignore the output.*

## Build / verify checklist (for the developer)

- File: `themes/timerpi/theme.css` (+ `README.md` mirroring this spec's story/values/tell-tales) — nothing else in the repo changes.
- `scripts/build.sh` then `scripts/check.sh`: no missing token, no base-selector `background`/`color`, `color-scheme: dark` exactly once, all contrast floors pass, `dist/` fresh, manifest entry `{slug: timerpi, shellAware: true, scheme: dark, variants: [high-contrast]}`.
- `scripts/theme-ready.sh timerpi`: rendered checks + axe + size budgets.
- Open `components.html`, pick TimerPi + high-contrast variant: every component distinct, Tab-visible focus everywhere, semantic buttons read as primary/danger/success, grayscale screenshot keeps all eight states distinct.
- TimerPi pages: `/` home, `/c/:code` dashboard (transport, cue table, day-bar, messages, share, import), `/d/:code` display at 1080p (no-scroll, safe area, OVERTIME/message prominence), `?tint=` N/A (no tint), `data-contrast="high"` and `data-motion="reduced"` spot-checked.
