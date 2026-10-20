# ftl-themes: `.otp` clips its first digit; modal title misaligned with body

Found by the TimerPi blue-future UI review (2026-10-20); both reproduce on
ftl's own reference pages (`auth.html`, `components-surfaces.html`,
`?theme=blue-future`, ftl-themes `76e27d2`, unchanged on `origin/main` d909ac9).

## 1. `.otp`: the first digit scrolls out of view after the sixth is typed

Repro: `auth.html?theme=blue-future`, click the verification code, type
`123456`. The `.otp` container scrolls 24 px (`scrollLeft 24`,
`scrollWidth 364` > `clientWidth 304`): the first box shows only half of "1".

Cause: `core/components/prose.css:570-582`. `.otp-input` is wider than the
boxes (`(n + 1)` cells, line ~600) so the caret can sit after the last digit;
`.otp { overflow: hidden }` still lets the browser scroll the container to
keep the caret in view.

Fix: `.otp { overflow: clip; }` (clip cannot be scrolled programmatically).
TimerPi carries this as a local workaround in `public/css/app.css` until
upstream ships it.

## 2. Modal title sits 8 px left of the modal's content

Repro: `components-surfaces.html?theme=blue-future`, "Uploads" modal: title
text starts at x=36, body text at x=44.

Cause: `core/core.css:636-645`. `.modal-header` is pulled to the modal edges
with `margin: calc(-1 * var(--surface-pad))` and then padded
`var(--space-xs) var(--space-m)` (16 px) while the body keeps
`--surface-pad` (`--space-l`, 24 px). Right for themes with a filled title
bar (they set `--modal-header-pad`), wrong for transparent headers.

Fix: default the inline padding to the surface padding:
`padding: var(--modal-header-pad, var(--space-xs) var(--surface-pad, var(--space-m)));`
Themes with a title bar keep their own `--modal-header-pad`.

Filed upstream: https://github.com/DrEVILish/ftl-themes/issues/72
