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
 *     reorder slides cards to their new place. An answered question turns
 *     green, fades, and once it is half gone the cards below close up.
 *   Poll / quiz: results grow in once they are shown (bars and numbers
 *     count up), then follow the votes smoothly.
 *
 * Text goes in through textContent only.
 */
import { setText } from './ui.js';

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
  }
  setText(st.title, p.question || '');
  if (mode === 'wall') updateWall(st, p, w);
  else if (mode === 'cloud') updateCloud(st, p);
  else updateBars(st, p);
}

/** Drop the box's state (timers, observers, elements). */
export function clearAudience(box) {
  const st = box._aud;
  if (st) {
    for (const t of st.timers) clearTimeout(t);
    st.ro?.disconnect();
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

// percents rounds the vote shares so they always add up to 100 (largest
// remainder); no votes → all 0.
export function percents(counts, len) {
  const n = Array.from({ length: len }, (_, i) => counts[i] || 0);
  const total = n.reduce((a, b) => a + b, 0);
  if (!total) return n.map(() => 0);
  const raw = n.map((c) => (c * 100) / total);
  const out = raw.map(Math.floor);
  let left = 100 - out.reduce((a, b) => a + b, 0);
  const order = raw.map((v, i) => [v - out[i], i]).sort((a, b) => b[0] - a[0]);
  for (const [, i] of order) { if (left-- <= 0) break; out[i]++; }
  return out;
}

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

const WALL_HOLD_MS = 1400; // green accent before the fade
const WALL_FADE_MS = 1000; // fade out; the gap closes at half way

function updateWall(st, p, w) {
  if (!st.list) {
    st.list = mk('ol', 'list b-qa-wall');
    st.empty = mk('div', 'b-poll-meta b-aud-empty');
    st.body.append(st.empty, st.list);
    st.items = new Map(); // entry id → { el, votes, text, leaving, holding }
    st.slots = [];        // ids in screen order (incl. answered ones fading)
    st.ro = new ResizeObserver(() => layoutWall(st));
    st.ro.observe(st.list);
  }
  const kids = p.children || [];
  const spotID = p.spotlight?.id || 0;
  const limit = Number(w?.opts?.count) || 8;
  // Spotlight first, then the open entries in server order (most upvoted
  // first). Answered entries never come in; one that was on screen leaves.
  const open = kids.filter((c) => c.state === 'open' && c.id !== spotID);
  const want = (p.spotlight ? [p.spotlight, ...open] : open).slice(0, limit);
  const wantIDs = new Set(want.map((c) => c.id));
  const answered = new Set(kids.filter((c) => c.state === 'answered').map((c) => c.id));

  for (const c of want) {
    let it = st.items.get(c.id);
    if (it?.leaving) { // came back (un-answered): stop leaving
      it.leaving = false;
      it.el.classList.remove('is-answered', 'is-leaving');
    }
    if (!it) {
      it = { el: mk('li', 'list-item b-qa-item'), votes: mk('span', 'b-qa-votes mono'), text: mk('span', 'b-qa-text'), fresh: true };
      it.el.append(it.votes, it.text);
      st.list.append(it.el);
      st.items.set(c.id, it);
    }
    setText(it.votes, `▲ ${c.upvotes || 0}`);
    setText(it.text, c.question);
    it.el.classList.toggle('is-spot', c.id === spotID);
  }
  for (const [id, it] of st.items) {
    if (wantIDs.has(id) || it.leaving) continue;
    it.leaving = true;
    if (answered.has(id)) {
      // Green, then fade; the others close up once it is half gone.
      it.holding = true;
      it.el.classList.add('is-answered');
      later(st, WALL_HOLD_MS, () => {
        it.el.classList.add('is-leaving');
        later(st, WALL_FADE_MS / 2, () => { it.holding = false; layoutWall(st); });
        later(st, WALL_FADE_MS, () => { if (it.leaving) { it.el.remove(); st.items.delete(id); } });
      });
    } else {
      it.holding = false;
      it.el.classList.add('is-leaving');
      later(st, WALL_FADE_MS, () => { if (it.leaving) { it.el.remove(); st.items.delete(id); } });
    }
  }
  // Screen order: the wanted entries, with any answered one that is still
  // fading kept in its old place.
  const order = want.map((c) => c.id);
  st.slots.forEach((id, i) => {
    const it = st.items.get(id);
    if (it?.leaving && it.holding && !order.includes(id)) order.splice(Math.min(i, order.length), 0, id);
  });
  st.slots = order;
  setText(st.empty, want.length ? '' : (p.kind === 'qa' ? 'No questions yet' : 'No ideas yet'));
  st.empty.hidden = want.length > 0 || order.length > 0;
  layoutWall(st);
}

function layoutWall(st) {
  if (!st.list) return;
  const H = st.list.clientHeight;
  const gap = parseFloat(getComputedStyle(st.list).rowGap) || 10;
  // Off-screen start for new cards: the bottom edge of the screen.
  const fromBottom = Math.max(H, window.innerHeight - st.list.getBoundingClientRect().top);
  let y = 0;
  const placed = new Set();
  for (const id of st.slots) {
    const it = st.items.get(id);
    // A leaving card gives up its slot once it is half faded; the rest close up.
    if (!it || (it.leaving && !it.holding)) continue;
    placed.add(id);
    const h = it.el.offsetHeight;
    if (it.fresh) {
      it.fresh = false;
      it.el.style.transition = 'none';
      it.el.style.transform = `translateY(${fromBottom}px)`;
      it.el.style.opacity = '0';
      void it.el.offsetWidth;
      it.el.style.transition = '';
    }
    it.el.style.transform = `translateY(${y}px)`;
    // Cards that don't fit fade instead of being cut through.
    it.el.style.opacity = it.leaving ? '' : (y + h <= H + 1 ? '1' : '0');
    y += h + gap;
  }
  // Leaving cards that gave up their slot stay where they are while they fade.
  for (const [id, it] of st.items) if (!placed.has(id)) it.el.style.opacity = '';
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
const FILL = 0.42;    // share of the tile the words cover

function hash(s) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) { h ^= s.charCodeAt(i); h = Math.imul(h, 16777619); }
  return h >>> 0;
}

function updateCloud(st, p) {
  if (!st.area) {
    st.area = mk('div', 'b-cloud');
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
      it = { el: mk('span', 'b-cloud-word', text), n: 1, angle: ANGLES[h % ANGLES.length], s: 0, fresh: true, vx: 0, vy: 0, dir: (h % 628) / 100 };
      it.el.dataset.tone = String((h >>> 8) % 3);
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
    it.rel = 0.34 + 0.66 * Math.sqrt(it.n / max);
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
        const pad = Math.max(6, 12 * Math.min(a.s, b.s));
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
