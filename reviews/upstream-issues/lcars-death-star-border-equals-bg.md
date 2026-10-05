# lcars & death-star: `--border` equals the background → "visible border" contract broken

> **STATUS: RESOLVED.** FIXED upstream — Option 1: --border now #333333 (lcars) / #1a1a1a (death-star), distinct from --bg. Verified in rebuilt lcars.css. Upstream commit 19c1e02, 2026-10-05.

**Bundles:** `themes/lcars/theme.css` (`--border: #000000`, ≈ line 35),
`themes/death-star/theme.css` (same pattern on true-black)
**Severity:** medium (any app surface that draws a non-panel frame from
`var(--border)` renders frameless)
**Found by:** TimerPi 13-theme browser audit, 2026-10-05

## Reproduce

1. With either bundle active, render an element styled
   `border: 1px solid var(--border)` that is **not** a `.panel`
   (e.g. app-owned tiles, inline previews, custom cards).
2. The border disappears — it is exactly the page background.

## Diagnosis

CONTRACT.md defines `--border` as the "Default **visible** border color".
These themes map it to their backdrop so the big `.panel` chrome reads as
seamless frames (their design intent — panels carry their own decorations),
but the token contract doesn't scope that choice to panels: every other
consumer of `--border` in an integrator's markup goes invisible with it.

## Proposed fix (either)

1. Set `--border` to a minimally-distinguishable value (lcars: e.g.
   `#1a1408`-tinted gray or a 10% candy blend); keep the seamless-panel look
   via `.panel { border-color: transparent }`-style component overrides,
   which is what the "theme sets component defaults at root scope" rule is
   for; or
2. Document the exception explicitly in CONTRACT.md ("themes may set
   `--border` equal to `--bg`; integrators must not rely on it for
   non-panel frames") — then this ticket is a doc change.

TimerPi hardened locally meanwhile: app tiles blend the border toward
`--accent` (`color-mix(in srgb, var(--border) 55%, var(--accent) 45%)`), so
frames stay visible whichever way upstream goes.

— filed by TimerPi (DrEVILish/ftl-themes).
