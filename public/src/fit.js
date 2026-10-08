/**
 * fit.js — big text on the plain screen views sizes by height in CSS and
 * by width here: a line that would overflow its box shrinks until it fits
 * (E2E #21: "36" on a phone, the clock's last digit cut on a 1080p TV).
 * The CSS font-size multiplies by var(--tp-fit, 1). Elements swapped in
 * later (oob re-render of the stage) are picked up too.
 */
export function fitWidth(sel) {
  const seen = new WeakMap(); // element → text length it was fitted for
  const fit = (el) => {
    el.style.removeProperty('--tp-fit');
    const box = el.parentElement?.clientWidth || 0;
    const w = el.scrollWidth;
    if (box && w > box) el.style.setProperty('--tp-fit', ((box / w) * 0.98).toFixed(3));
  };
  const all = () => document.querySelectorAll(sel).forEach(fit);
  // Re-measure when the text length changes, not on every tick.
  const check = () => {
    for (const el of document.querySelectorAll(sel)) {
      const n = el.textContent.length;
      if (seen.get(el) !== n) { seen.set(el, n); fit(el); }
    }
  };
  new MutationObserver((ms) => {
    if (ms.some((m) => m.type === 'attributes')) all(); else check();
  }).observe(document.documentElement, { childList: true, characterData: true, subtree: true, attributes: true, attributeFilter: ['data-rotate', 'data-theme'] });
  addEventListener('resize', all);
  document.fonts?.ready.then(all);
  check();
}
