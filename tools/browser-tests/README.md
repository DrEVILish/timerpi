# Browser tests

End-to-end checks that need a real browser (layout editor drags, keyboard
shortcuts vs typing, phone voting…). The Go tests can't see these.

```sh
cd tools/browser-tests
npm install          # once; uses Playwright (no browser download needed if
                     # PLAYWRIGHT_BROWSERS_PATH or CHROMIUM_PATH is set)
node run.mjs         # all tests
node run.mjs keys    # tests whose file name contains "keys"
```

`run.mjs` builds TimerPi, starts it on a free port with a throwaway data
dir, runs every `tests/*.mjs`, and exits non-zero on any failure.

A test file exports `name` and `async function run(t)`; `t` gives
`browser`, `base`, `newEvent()` (an event + room, signed in as its
SuperOperator) and `check(label, cond)`.
