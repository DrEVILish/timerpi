# ftl-themes: a maximized native `<dialog class="modal">` collapses to its content

**Type:** bug (core). Filed upstream as item 3 of https://github.com/DrEVILish/ftl-themes/issues/72.

## Repro

On `/ftl/components.html?theme=blue-future` (any theme), at 1440x900:

```js
const d = document.createElement('dialog');
d.className = 'modal is-maximized';
d.innerHTML = '<div class="modal-header"><h2>Max</h2></div><p>body</p>';
document.body.appendChild(d); d.showModal();
d.getBoundingClientRect(); // width 1440, height 96 (expected 900)
```

## Cause

`core/core.css`: the top-layer bridge `dialog.modal:modal { height: fit-content }`
(specificity 0,2,1) outranks `.modal.is-maximized { height: 100% }` (0,2,0),
so the maximized state only applies its width.

## Proposed fix

Key the maximized rule on the open state as well, e.g. add
`dialog.modal.is-maximized:modal, .modal.is-maximized[popover]:popover-open { height: 100%; }`.

## TimerPi workaround

`public/css/app.css` (layout editor): `dialog.modal.is-maximized:modal { height: 100%; }`.
Remove it when the bundle has the fix.
