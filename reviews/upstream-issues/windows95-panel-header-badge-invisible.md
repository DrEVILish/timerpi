# windows95: `.badge` inside `.panel-header` renders white-on-white (invisible)

**Bundle:** `themes/windows95/theme.css` (dist `win…/windows95.css`) — contract v4
**Severity:** medium (component text becomes unreadable on the theme's own surface)
**Found by:** TimerPi 13-theme browser audit, 2026-10-05

## Reproduce

1. Serve any page using the windows95 bundle, with an app that puts `.badge`
   elements inside a `.panel-header` (very common: count/total/status chips).
2. The badge text is invisible: foreground equals background
   (`#dfdfdf` on `#dfdfdf`).

## Diagnosis

- Root scope sets `--badge-bg: var(--surface-2)` (= `#dfdfdf`).
- `.panel-header` sets `--muted: #dfdfdf` (theme.css ≈ line 249) to brighten
  header captions against the navy title bar.
- Core paints badges with `color: var(--badge-fg, var(--muted))` — badges have
  no `--badge-fg`, so they inherit the header's muted → identical to the badge
  fill.
- The theme already maintains a caption-exception reset that re-points
  `.input`, `.select`, `.btn` inside `.panel-header` (≈ line 258) — `.badge`
  is simply missing from its `:is(...)` list.

## Proposed fix (one selector)

```css
/* in the line-~258 exception reset, add .badge to the group */
.panel-header :is(.input, .select, .btn, .badge) {
  /* existing resets … plus: */
  --badge-fg: #000; /* navy/gray-80 caption ink, matches the group */
}
```

## Impact if unfixed

Any integrator using header badges loses them on windows95 (counters, status
chips). TimerPi has worked around the display consequences; the bundle fix is
upstream.

— filed by TimerPi (DrEVILish/ftl-themes). Patch welcome either way; the
exception-list omission looks like an oversight, not a design choice.
