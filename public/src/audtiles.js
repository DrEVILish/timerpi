/**
 * audtiles.js — the audience item on a screen (poll, quiz, Q&A / ideas
 * wall, word cloud), painted IN PLACE so nothing ever jumps (2026-10-07
 * audience notes).
 *
 * Each tile box keeps its own elements between frames. Positions are
 * computed here and applied as CSS transforms with transitions, so every
 * change — a new word, a word growing, a question moving up, the
 * spotlight, an answer leaving — animates from where it was.
 *
 *   Word cloud: a physics loop pulls every word to the centre; words
 *     push each other apart and never overlap. New words fade in at the
 *     border and drift in; sizes ease. Rotation is between -90° and +90°,
 *     fixed per word.
 *   Wall: cards are stacked by position (spotlight first, then by
 *     upvotes). New questions rise in from the bottom of the screen; a
 *     reorder slides cards to their new place. An answered question stays,
 *     an .is-answered card after the open ones; a dismissed one fades.
 *     Entries that don't fit say "+N more".
 *   Poll / quiz: results grow in once they are shown (bars and numbers
 *     count up), then follow the votes smoothly.
 *
 * Text goes in through textContent only.
 */
import { setText, percents } from './ui.js';
import { fitHeight } from './fit.js';

function mk(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}

/** Paint item p into box (the tile's .b-js-poll / -qa / -cloud). */
export function paintAudience(box, p, w) {
  const mode = p.kind === 'qa' || p.kind === 'ideas' ? 'wall' : p.kind === 'wordcloud' ? 'cloud' : 'bars';
  let st = box._aud;
  if (!st || st.id !== p.id || st.mode !== mode) {
    clearAudience(box);
    st = box._aud = { id: p.id, mode, title: mk('div', 'b-poll-q'), body: mk('div', 'b-aud-body'), timers: new Set() };
    box.dataset.audMode = mode;
    box.append(st.title, st.body);
    // One content-fit for the whole tile (review2 N1/N2): every part is
    // sized in em, and the box shrinks until nothing overflows. A new
    // tile size (absorb, rotation, resize) or a late font re-fits. A poll
    // also grows into a tall tile; a wall keeps its size and shows more
    // cards instead.
    st.fit = () => fitHeight(box, 0.4, mode === 'bars' ? 1.6 : 1);
    st.boxRO = new ResizeObserver(st.fit);
    st.boxRO.observe(box);
    document.fonts?.ready.then(st.fit);
  }
  setText(st.title, p.question || '');
  if (mode === 'wall') updateWall(st, p, w);
  else if (mode === 'cloud') updateCloud(st, p);
  else updateBars(st, p);
  st.fit();
  if (mode === 'wall') layoutWall(st);
}

/** Drop the box's state (timers, observers, elements). */
export function clearAudience(box) {
  const st = box._aud;
  if (st) {
    for (const t of st.timers) clearTimeout(t);
    st.ro?.disconnect();
    st.boxRO?.disconnect();
    cancelAnimationFrame(st.raf || 0);
    for (const r of st.rows || []) cancelAnimationFrame(r.raf || 0);
  }
  box._aud = null;
  delete box.dataset.audMode;
  box.textContent = '';
}

function later(st, ms, fn) {
  const t = setTimeout(() => { st.timers.delete(t); fn(); }, ms);
  st.timers.add(t);
}

/* ---------------------------------------------------------------- polls -- */

const ease = (t) => 1 - Math.pow(1 - t, 3);

function updateBars(st, p) {
  const opts = p.options || [];
  const sig = `${p.kind}|${opts.join('\u0001')}`;
  if (st.optSig !== sig) {
    st.optSig = sig;
    for (const r of st.rows || []) cancelAnimationFrame(r.raf || 0);
    st.body.textContent = '';
    st.meta = mk('div', 'b-poll-meta');
    st.body.append(st.meta);
    st.results = false;
    st.rows = opts.map((label) => {
      const row = mk('div', 'b-poll-opt');
      const head = mk('div', 'b-poll-opt-head');
      const name = mk('span', '', label);
      const num = mk('span', 'mono b-poll-num');
      head.append(name, num);
      const bar = mk('progress', 'progress b-poll-bar');
      bar.max = 100;
      bar.value = 0;
      bar.hidden = true;
      row.append(head, bar);
      st.body.append(row);
      return { row, name, num, bar, label, pct: 0, n: 0, raf: 0 };
    });
  }
  const results = p.state === 'results';
  const totalN = p.total || 0;
  setText(st.meta, results ? `${totalN} vote${totalN === 1 ? '' : 's'}` : `${totalN} voted so far`);
  const reveal = results && !st.results;
  st.results = results;
  const pcts = percents(p.counts || [], opts.length);
  st.rows.forEach((r, i) => {
    const correct = results && p.kind === 'quiz' && i === p.correct;
    r.row.classList.toggle('is-correct', correct);
    r.row.classList.toggle('is-results', results);
    setText(r.name, (correct ? '✔ ' : '') + r.label);
    if (!results) {
      // Before results the room sees the options only — no tallies that
      // would sway the vote.
      cancelAnimationFrame(r.raf || 0);
      r.bar.hidden = true;
      r.bar.value = 0;
      r.pct = 0; r.n = 0; r.toPct = 0; r.toN = 0;
      setText(r.num, '');
      return;
    }
    r.bar.hidden = false;
    const n = p.counts?.[i] || 0;
    const pct = pcts[i];
    if (reveal) {
      r.row.style.animationDelay = `${i * 110}ms`;
      r.row.classList.remove('b-poll-reveal');
      void r.row.offsetWidth;
      r.row.classList.add('b-poll-reveal');
    }
    if (r.toPct === pct && r.toN === n && !reveal) return;
    countTo(r, pct, n, reveal ? 300 + i * 110 : 0);
  });
}

// Re-exported for the browser tests; the maths lives in ui.js (shared with the phone).
export { percents };

// countTo tweens a row's bar and numbers from what it shows to (pct, n).
function countTo(r, pct, n, delay) {
  cancelAnimationFrame(r.raf || 0);
  r.toPct = pct; r.toN = n;
  const fromPct = r.pct, fromN = r.n;
  const dur = 900;
  let t0 = 0;
  const step = (now) => {
    if (!t0) t0 = now + delay;
    const k = now < t0 ? 0 : Math.min(1, (now - t0) / dur);
    const e = ease(k);
    r.pct = fromPct + (pct - fromPct) * e;
    r.n = fromN + (n - fromN) * e;
    r.bar.value = r.pct;
    setText(r.num, `${Math.round(r.n)} · ${Math.round(r.pct)}%`);
    r.raf = k < 1 ? requestAnimationFrame(step) : 0;
    if (!r.raf) { r.pct = pct; r.n = n; }
  };
  r.raf = requestAnimationFrame(step);
}

/* ------------------------------------------------------------ the wall -- */

const WALL_FADE_MS = 1000; // a card that leaves the wall fades out

function updateWall(st, p, w) {
  if (!st.list) {
    // The spotlight is the first entry of its own one-item ftl
    // .ranked-list, so a screen reader hears it as rank 1 (signage.md),
    // shown full size above the wall ("Now answering", PRODUCT A7). The
    // wall is ftl .ranked-list.is-positioned: we set each card's --slot-y
    // and ftl slides it there.
    st.spotList = mk('ol', 'ranked-list b-qa-spot-list');
    st.spot = mk('li', 'ranked-item is-spotlight b-qa-spot');
    st.spot.setAttribute('aria-current', 'true');
    st.spotText = mk('span', 'b-qa-spot-text');
    st.spotVotes = mk('span', 'ranked-votes');
    st.spot.append(mk('span', 'b-eyebrow', 'Now answering'), st.spotText, st.spotVotes);
    st.spotList.append(st.spot);
    st.spotList.hidden = true;
    st.list = mk('ol', 'ranked-list is-positioned b-qa-wall');
    st.list.setAttribute('aria-label', p.kind === 'qa' ? 'Questions by votes' : 'Ideas by votes');
    st.empty = mk('div', 'b-poll-meta b-aud-empty');
    // More open entries than the tile holds: say so (review2 N5).
    st.more = mk('div', 'b-poll-meta b-qa-more');
    st.more.hidden = true;
    st.body.append(st.spotList, st.empty, st.list, st.more);
    st.items = new Map(); // entry id → { el, votes, text, tag, leaving, answered }
    st.slots = [];        // ids in screen order
    st.ro = new ResizeObserver(() => layoutWall(st));
    st.ro.observe(st.list);
  }
  const kids = p.children || [];
  // A speaker reads from metres away: fewer, bigger questions (review #9).
  const limit = Number(w?.opts?.count) || (w?.opts?.target === 'presenter' ? 4 : 8);
  const spot = p.spotlight || null;
  const spotID = spot?.id || 0;
  if ((st.spotID || 0) !== spotID) {
    st.spotID = spotID;
    st.spot.classList.remove('is-new');
    void st.spot.offsetWidth;
    if (spot) st.spot.classList.add('is-new'); // ftl ranked-rise: the new spotlight rises in
  }
  st.spotList.hidden = !spot;
  st.body.classList.toggle('has-spot', !!spot);
  if (spot) {
    setText(st.spotText, spot.question);
    votes(st.spotVotes, spot.upvotes);
  }
  // Open entries in server order (most upvoted first), then the answered
  // ones, still on the wall as ftl .is-answered cards with an "Answered"
  // tag (PRODUCT A7: greyed, not gone). Dismissed entries leave.
  const open = kids.filter((c) => c.state === 'open' && c.id !== spotID);
  const done = kids.filter((c) => c.state === 'answered' && c.id !== spotID);
  const shown = open.slice(0, limit);
  const want = shown.concat(done.slice(0, Math.max(0, limit - shown.length)));
  st.extra = open.length - shown.length;
  for (const c of want) {
    let it = st.items.get(c.id);
    if (it?.leaving) { // came back: stop leaving
      it.leaving = false;
      it.el.classList.remove('is-leaving');
    }
    if (!it) {
      it = { el: mk('li', 'ranked-item b-qa-item'), votes: mk('span', 'ranked-votes'), text: mk('span', 'b-qa-text'), fresh: true };
      it.el.append(it.text, it.votes);
      st.list.append(it.el);
      st.items.set(c.id, it);
    }
    votes(it.votes, c.upvotes);
    setText(it.text, c.question);
    it.answered = c.state === 'answered';
    it.el.classList.toggle('is-answered', it.answered);
    // Not colour alone: the card says so.
    if (it.answered && !it.tag) { it.tag = mk('span', 'badge badge-solid badge-success', 'Answered'); it.text.after(it.tag); }
    if (!it.answered && it.tag) { it.tag.remove(); it.tag = null; }
  }
  const wantIDs = new Set(want.map((c) => c.id));
  for (const [id, it] of st.items) {
    if (wantIDs.has(id) || it.leaving) continue;
    it.leaving = true;
    it.el.classList.add('is-leaving');
    later(st, WALL_FADE_MS, () => { if (it.leaving) { it.el.remove(); st.items.delete(id); } });
  }
  // DOM order = rank order (screen readers hear the rank).
  for (const c of want) st.list.append(st.items.get(c.id).el);
  st.slots = want.map((c) => c.id);
  setText(st.empty, want.length ? '' : (p.kind === 'qa' ? 'No questions yet' : 'No ideas yet'));
  st.empty.hidden = want.length > 0 || !!spot;
  layoutWall(st);
}

// The bare count, as on ftl's reference wall; the name says what it is.
function votes(el, n) {
  setText(el, String(n || 0));
  el.setAttribute('aria-label', `${n || 0} vote${n === 1 ? '' : 's'}`);
}

function layoutWall(st) {
  if (!st.list) return;
  const H = st.list.clientHeight;
  const gap = 0.6 * parseFloat(getComputedStyle(st.list).fontSize) || 10; // ftl --ranked-gap
  // Off-screen start for new cards: the bottom edge of the screen.
  const fromBottom = Math.max(H, window.innerHeight - st.list.getBoundingClientRect().top);
  let y = 0, cut = 0, full = false;
  for (const id of st.slots) {
    const it = st.items.get(id);
    if (!it || it.leaving) continue;
    const h = it.el.offsetHeight;
    if (it.fresh) {
      it.fresh = false;
      it.el.style.transition = 'none';
      it.el.style.setProperty('--slot-y', `${fromBottom}px`);
      it.el.style.opacity = '0';
      void it.el.offsetWidth;
      it.el.style.transition = '';
    }
    it.el.style.setProperty('--slot-y', `${y}px`); // ftl moves the card (translate)
    // Cards that don't fit fade instead of being cut through; from the
    // first one that doesn't, the rest wait too.
    full = full || y + h > H + 1;
    it.el.style.opacity = full ? '0' : '1';
    if (full && !it.answered) cut++;
    y += h + gap;
  }
  for (const it of st.items.values()) if (it.leaving) it.el.style.opacity = '';
  const more = (st.extra || 0) + cut;
  setText(st.more, more ? `+${more} more` : '');
  st.more.hidden = !more;
}

/* ---------------------------------------------------------- word cloud -- */

// The cloud is a small physics loop: every word is pulled towards the
// centre, words push each other apart (box against box, so they never
// overlap) and stay inside the tile. Sizes ease towards their target, so
// a word that gains votes grows smoothly and the others make room. The
// loop sleeps once everything is at rest and wakes on any change.
// ponytail: O(n²) collision pass; fine for the ~100 words a room sends,
// add a grid if clouds get much bigger.

const ANGLES = [0, 0, 0, 0, 0, -90, 90, -90, 90, -30, 30, -15, 15];
const PULL = 0.012;   // spring towards the centre, per frame
const DAMP = 0.82;    // velocity kept per frame
const GROW = 0.08;    // share of the size gap closed per frame
const FILL = 0.5;     // share of the tile the words cover

function hash(s) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); }
  return h >>> 0;
}

function updateCloud(st, p) {
  if (!st.area) {
    st.area = mk('ul', 'word-cloud b-cloud');
    st.area.setAttribute('aria-label', 'Audience words');
    st.empty = mk('div', 'b-poll-meta b-aud-empty');
    st.body.append(st.area, st.empty);
    st.words = new Map(); // lower-case word → word state
    st.ro = new ResizeObserver(() => { sizeCloud(st); wake(st); });
    st.ro.observe(st.area);
    document.fonts?.ready.then(() => { if (st.area.isConnected) { sizeCloud(st); wake(st); } });
  }
  const seen = new Set();
  for (const c of p.children || []) {
    if (c.state && c.state !== 'open') continue;
    const text = String(c.question || '').trim();
    const key = text.toLowerCase();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    let it = st.words.get(key);
    if (it?.gone) { it.gone = false; it.el.style.opacity = '1'; }
    if (!it) {
      const h = hash(key);
      it = { el: mk('li', 'word-cloud-word b-cloud-word', text), n: 1, angle: ANGLES[h % ANGLES.length], s: 0, fresh: true, vx: 0, vy: 0, dir: (h % 628) / 100 };
      it.el.dataset.tone = String(((h >>> 8) % 6) + 1); // ftl's six chart tones; .is-top glows (review #14)
      st.area.append(it.el);
      st.words.set(key, it);
    }
    it.n = Math.max(1, c.upvotes || 1);
  }
  for (const [key, it] of st.words) {
    if (seen.has(key) || it.gone) continue;
    it.gone = true;
    it.el.style.opacity = '0';
    later(st, 900, () => { if (it.gone) { it.el.remove(); st.words.delete(key); } });
  }
  const sig = [...seen].sort().join('\u0001');
  if (st.sig !== sig) { st.sig = sig; st.squeeze = 1; } // new words: fresh start at full size
  setText(st.empty, seen.size ? '' : 'Waiting for the first word…');
  st.empty.hidden = seen.size > 0;
  sizeCloud(st);
  wake(st);
}

// sizeCloud sets every word's target scale so the words cover about FILL
// of the tile, biggest = most sent. Words are styled at 100px; their box
// at scale s is (w, h) * s, swapped by the rotation.
function sizeCloud(st) {
  st.W = st.area.clientWidth;
  st.H = st.area.clientHeight;
  const list = [...st.words.values()].filter((it) => !it.gone);
  if (!st.W || !st.H || !list.length) return;
  const max = Math.max(...list.map((it) => it.n));
  let area = 0;
  for (const it of list) {
    // Measured every time: a theme font that loads late changes the box.
    const w = it.el.offsetWidth, h = it.el.offsetHeight;
    const r = (it.angle * Math.PI) / 180, c = Math.abs(Math.cos(r)), sn = Math.abs(Math.sin(r));
    it.w0 = w * c + h * sn; it.h0 = w * sn + h * c;
    it.rel = 0.3 + 0.7 * (it.n / max); // linear, like ftl --weight: the most sent word stands out
    area += it.w0 * it.h0 * it.rel * it.rel;
  }
  // No word may be wider or taller than the tile, measured on its own
  // rotated box (a long word turned 90° must fit the height, E2E #22);
  // squeeze shrinks the lot while words still can't find room.
  let k = Math.min(Math.sqrt((FILL * st.W * st.H) / area), (st.H * 0.3) / 100);
  for (const it of list) k = Math.min(k, (0.92 * st.W) / (it.rel * it.w0), (0.92 * st.H) / (it.rel * it.h0));
  k *= st.squeeze || 1;
  for (const it of list) it.target = it.rel * k;
}

function wake(st) {
  if (!st.raf && st.words.size) st.raf = requestAnimationFrame(() => stepCloud(st));
}

function stepCloud(st) {
  st.raf = 0;
  if (st.area && !st.area.isConnected) return;
  const { W, H } = st;
  const list = [...st.words.values()].filter((it) => !it.gone && it.target);
  if (!W || !H || !list.length) return;
  let moving = false;
  for (const it of list) {
    if (it.fresh) {
      // Fade in at the border, on the word's own heading.
      it.fresh = false;
      it.s = it.target;
      it.x = Math.cos(it.dir) * (W / 2 - (it.w0 * it.s) / 2);
      it.y = Math.sin(it.dir) * (H / 2 - (it.h0 * it.s) / 2);
      requestAnimationFrame(() => { it.el.style.opacity = '1'; });
    }
    const ds = (it.target - it.s) * GROW;
    it.s = Math.abs(ds) < 1e-4 ? it.target : it.s + ds;
    if (ds) moving = true;
    // Pull to the centre, stronger vertically on a wide tile so the
    // cloud spreads out to the tile's shape.
    it.vx = (it.vx - it.x * PULL * (H / W)) * DAMP;
    it.vy = (it.vy - it.y * PULL) * DAMP;
    it.x += it.vx;
    it.y += it.vy;
    it.w = it.w0 * it.s; it.h = it.h0 * it.s;
  }
  // Push overlapping boxes apart along the shallower axis; the bigger word
  // moves less. A few passes settle chains of contacts. The gap between
  // words grows with their size, so they never touch.
  // Passes repeat until a pass finds no overlap (long chains of words
  // need several), capped per frame.
  for (let pass = 0, hit = true; pass < 12 && hit; pass++) {
    hit = false;
    for (let i = 0; i < list.length; i++) {
      const a = list[i];
      for (let j = i + 1; j < list.length; j++) {
        const b = list[j];
        const pad = Math.max(6, 25 * Math.max(a.s, b.s)); // ~0.25em of the bigger word (review2 N10: "oktired")
        const ox = (a.w + b.w) / 2 + pad - Math.abs(a.x - b.x);
        const oy = (a.h + b.h) / 2 + pad - Math.abs(a.y - b.y);
        if (ox <= 0 || oy <= 0) continue;
        hit = true;
        const wa = b.w * b.h / (a.w * a.h + b.w * b.h), wb = 1 - wa;
        // Shallower axis relative to the tile's shape, so a wide tile
        // spreads words sideways instead of stacking them (E2E #22).
        if (ox / W < oy / H) {
          const d = (a.x < b.x || (a.x === b.x && i < j) ? -1 : 1) * ox;
          a.x += d * wa; b.x -= d * wb;
          a.vx = 0; b.vx = 0;
        } else {
          const d = (a.y < b.y || (a.y === b.y && i < j) ? -1 : 1) * oy;
          a.y += d * wa; b.y -= d * wb;
          a.vy = 0; b.vy = 0;
        }
      }
    }
    for (const it of list) {
      it.x = Math.max(-W / 2 + it.w / 2, Math.min(W / 2 - it.w / 2, it.x));
      it.y = Math.max(-H / 2 + it.h / 2, Math.min(H / 2 - it.h / 2, it.y));
    }
  }
  // Still touching after the passes (a crowded tile): count the frames,
  // and after a short while make every word a little smaller.
  let touching = false;
  for (let i = 0; i < list.length && !touching; i++) {
    for (let j = i + 1; j < list.length; j++) {
      const a = list[i], b = list[j];
      if (Math.abs(a.x - b.x) < (a.w + b.w) / 2 + 2 && Math.abs(a.y - b.y) < (a.h + b.h) / 2 + 2) { touching = true; break; }
    }
  }
  st.crowded = touching ? (st.crowded || 0) + 1 : 0;
  if (st.crowded > 40) {
    st.crowded = 0;
    st.squeeze = (st.squeeze || 1) * 0.94;
    sizeCloud(st);
    moving = true;
  }
  const top = list.reduce((m, it) => (it.n > m.n ? it : m), list[0]);
  for (const it of list) {
    const px = it.lx === undefined ? Infinity : Math.abs(it.x - it.lx) + Math.abs(it.y - it.ly);
    if (px > 0.15) moving = true; // under 0.15 px a frame is rest: the loop sleeps
    it.lx = it.x; it.ly = it.y;
    it.el.classList.toggle('is-top', it === top && list.filter((o) => o.n === top.n).length === 1);
    it.el.style.transform = `translate(${it.x.toFixed(1)}px, ${it.y.toFixed(1)}px) rotate(${it.angle}deg) scale(${it.s.toFixed(4)}) translate(-50%, -50%)`;
  }
  if (moving) wake(st);
}
