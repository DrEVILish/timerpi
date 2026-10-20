# ftl-themes: sticky modal footer, and an "any colour" swatch

Not filed. Found in the TimerPi Run-tab review, round 2 (STATUS U50).

## 1. `.modal-footer` scrolls away in a tall modal

**Repro:** a `<dialog class="modal">` whose form is taller than the viewport (TimerPi's cue editor at 1024×768 or 390×844). `.modal` scrolls as a whole (`core.css` `.modal`), so `.modal-footer` with Cancel / Save is below the fold until you scroll.

**Local workaround** (`public/css/timerpi.css`, `.tp-inspector .modal-footer`): `position: sticky; inset-block-end: calc(-1 * var(--surface-pad))`, a negative bottom margin and padding of `--surface-pad`, and `background-color: var(--surface)`. It can't use `--modal-bg`, because blue-future paints its corner brackets into that token, and they would show again on the footer.

**Proposal:** add `.modal.has-sticky-footer` (or make it the default) to core. Core knows the modal's padding and base fill, so it can pin the footer to the bottom edge. A `--modal-footer-bg` token would let themes with art in `--modal-bg` give the footer a plain fill.

## 2. `.swatches` has no "any colour" swatch

**Repro:** a `.swatches` fieldset next to a native `<input type="color">` shows a grey, square browser plate beside the round ftl swatches.

**Local workaround** (`public/src/swatches.js`): the last swatch is a `label.swatch` that holds the colour input. `.swatch > input` hides it, and a click on the label opens the picker. A radio in the same label carries the ring and ✓ while a custom colour is chosen. `--swatch` is a conic colour wheel until a custom colour is picked.

**Proposal:** document this pattern in `forms.md`, or ship `.swatch.is-custom` with a theme-tunable `--swatch-custom-art`. The ring and ✓ should follow `input[type=color]` when it carries a data flag, or the pattern should document the radio.
