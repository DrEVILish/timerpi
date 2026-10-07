# ftl-themes — request: a "card list" variant of `.list`

**Filed as:** [ftl-themes issue #67](https://github.com/DrEVILish/ftl-themes/issues/67), owner direction, 2026-10-07, from TimerPi's audience screens.
**Type:** feature request.

## Why

TimerPi's Q&A wall is a list of separate question cards: each item has its
own border, the list itself has none, and one item (the spotlight) gets a
thicker accent border; an answered item takes the success colour. ftl's
`.list` draws one bordered box with flat rows, so TimerPi turns that off
with tokens and draws the cards itself (`public/css/timerpi.css`, `.b-qa-*`):

```css
.b-qa-wall { --list-border-width: 0; --list-bg: transparent; row-gap: .6rem; }
.b-qa-item { border: 2px solid var(--border); border-radius: var(--radius);
  background: var(--surface-2, var(--surface)); padding: .5em .8em; }
.b-qa-item.is-spot { border-width: 6px; border-color: var(--accent); }
.b-qa-item.is-answered { border-color: var(--success);
  background: color-mix(in srgb, var(--success) 18%, var(--surface-2, var(--surface))); }
```

Every theme then shows the same generic card instead of its own (LCARS,
Windows 95 and Pip-Boy each draw cards differently).

## Proposal

- `.list.list-cards`: no outer border/background; each `.list-item` is a
  card (theme border, radius, surface), separated by `--list-gap`.
- `.list-item.is-featured`: thick accent border (`--list-featured-border-width`).
- `.list-item.is-success` (and the other status tones): status border + tint.

## Not for upstream

The motion (cards sliding by transform, the word-cloud physics) is app
JavaScript in `public/src/audtiles.js` and stays in TimerPi.
