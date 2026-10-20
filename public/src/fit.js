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

/**
 * fitHeight(el, floor, ceil): one content-fit pass for a box whose parts
 * are all sized in em (the audience tiles: poll, quiz, wall, cloud). The
 * box's CSS font-size multiplies by var(--tp-fit, 1); this finds the
 * largest factor in [floor, ceil] at which nothing overflows the box, so
 * a long question or ten options shrink together instead of overprinting,
 * and a short poll grows into a tall portrait tile (review2 N1/N2, UI #9).
 * Seven layouts per call (binary search).
 */
export function fitHeight(el, floor = 0.4, ceil = 1) {
  if (!el?.isConnected) return;
  const set = (k) => el.style.setProperty('--tp-fit', k.toFixed(3));
  const fits = () => el.scrollHeight <= el.clientHeight + 1 && el.scrollWidth <= el.clientWidth + 1;
  set(ceil);
  if (!el.clientHeight || fits()) return;
  let lo = floor, hi = ceil;
  for (let i = 0; i < 7; i++) {
    const mid = (lo + hi) / 2;
    set(mid);
    if (fits()) lo = mid; else hi = mid;
  }
  set(lo);
}
