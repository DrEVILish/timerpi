/**
 * TimerPi display board client — /d/:code?view=board.
 *
 * This module is THIS page's own glue: it imports client-parity math from
 * shared engine.js READ-ONLY and joins the SHARED display mesh (Mesh from
 * mesh.js, display role — the same init timerpi.js runs for its display
 * role, mirrored here) for live data. Two halves:
 *
 *  1. RENDER — adopt PROTOCOL snapshots through the mesh (updatedAt
 *     dominance inside Mesh; Fix-2 {"t":"schedule"} frame rows preferred,
 *     degrading to client computeSchedule). Digits are never server-ticked;
 *     the local clock re-anchors on serverTime (joined/state/pong). While
 *     the server is down the board stays live peer-to-peer: the mesh master
 *     keeps executing and broadcasting, and #tp-offline / #b-link only report
 *     LINK DOWN when no server AND no mesh path carry the show.
 *  2. COMPOSE (operator only, ?edit=1) — Edit-layout/Done toggle, pointer
 *     drag to move + corner resize (touch-capable), add-widget palette,
 *     role presets (Stage/Lobby one-click layouts), per-widget settings,
 *     debounced PUT autosave, reset-to-factory.
 *
 * EDIT-MODE SAFETY: ?edit=1 only RENDERS the toolbar — the grid stays locked
 * until the operator presses "Edit layout" (body[data-editing]). On editable
 * boots the join-card QR is rewritten to the LOCKED board URL (Go bakes
 * ?edit=1 into Join.QR/Self) so a scanned TV never hands out compose access.
 */

import {
  activeCue, cueAfter, elapsedMS, remainingMS, isOvertime, alertState,
  clockView, computeSchedule, fmtDuration, fmtRemaining, fmtTimeOfDay,
} from './engine.js';
import { Mesh, screenName } from './mesh.js';
import { applyTheme, loadThemeVersion, initClientLog } from './theme.js';
import { api, setText } from './ui.js';
import { applyWaiting } from './waiting.js';
import { tpConfirm, tpPrompt } from './dialog.js';
import { paintAudience, clearAudience } from './audtiles.js';

loadThemeVersion();

const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
// unchanged(el, key): true when el already shows what key describes; else
// remembers key so the caller rebuilds once. List tiles call it first, so
// an update that changes nothing they show never rebuilds (flickers) them
// (STATUS U7).
const unchanged = (el, key) => {
  if (!el) return true;
  if (el.dataset.k === key) return true;
  el.dataset.k = key;
  return false;
};
// Tiles that show audience interactions; a screen without any ignores
// poll updates entirely (STATUS U7).
const AUDIENCE_TILES = new Set(['poll', 'qa', 'wordcloud']);
const hasAudienceTile = () => !!grid && $$('.b-widget', grid).some((t) => AUDIENCE_TILES.has(t.dataset.widget));

const body = document.body;
const code = body.dataset.show || '';
const boardId = body.dataset.board || '';
const editable = body.dataset.editable === '1';
// Gallery-modal compose: the iframe edits the board but must NOT
// self-register a phantom screen — presence stays honest.
const previewMode = new URLSearchParams(location.search).has('preview');
const grid = $('#b-grid');

/* State (snap/sched/editing) + serverNow live in the mesh block below —
   the shared Mesh owns the snapshot, the clock offset and the link. */

/* ---------------------------------------------------------------- layout -- */

// Live layout document: parsed from the server-embedded #b-layout JSON
// (the same doc PUT autosaves). Geometry edits mutate this + tile styles.
let layout = { v: 1, widgets: [] };
try {
  const raw = $('#b-layout');
  if (raw) layout = JSON.parse(raw.textContent || '{"v":1,"widgets":[]}');
} catch { /* corrupt embed: render-only, editor stays inert */ }
// lastSaved is the layout the server last accepted: a structural change
// that fails to save rolls back to it instead of reloading (BUGLOG RW43).
let lastSaved = structuredClone(layout);

// Palette defaults for newly added tiles (mirror of the Go registry).
const TILE_DEFAULTS = {
  countdown: { w: 8, h: 3, opts: { tenths: '1' } },
  cuelabel: { w: 5, h: 1, opts: { source: 'label' } },
  speaker: { w: 3, h: 1, opts: {} },
  nextup: { w: 4, h: 2, opts: {} },
  wallclock: { w: 4, h: 1, opts: { tenths: '0' } },
  progress: { w: 8, h: 1, opts: {} },
  dayprogress: { w: 8, h: 1, opts: {} },
  messages: { w: 4, h: 3, opts: {} },
  showtitle: { w: 6, h: 1, opts: {} },
  rate: { w: 2, h: 1, opts: {} },
  schedule: { w: 4, h: 4, opts: { count: '5' } },
  notice: { w: 6, h: 2, opts: { text: 'Welcome' } },
  // Every palette button needs an entry, or clicking it silently does
  // nothing (BUGLOG RW34).
  nownext: { w: 6, h: 4, opts: {} },
  poll: { w: 6, h: 4, opts: { target: 'audience' } },
  qa: { w: 6, h: 4, opts: { target: 'audience' } },
  wordcloud: { w: 6, h: 4, opts: { target: 'audience' } },
  map: { w: 6, h: 4, opts: {} },
  joinqr: { w: 3, h: 4, opts: {} },
  rooms: { w: 8, h: 4, opts: {} },
  eventschedule: { w: 8, h: 4, opts: {} },
};

const widgetOf = (wid) => (layout.widgets || []).find((w) => w.id === wid) || null;
const tileOf = (wid) => (grid ? grid.querySelector(`[data-wid="${CSS.escape(wid)}"]`) : null);

/* ------------------------------------------------- mesh (shared P2P) -- */

// The shared mesh is the board's ONLY data path (mirrors timerpi.js's
// display-role initMesh: same join identity, same frame handling). Mesh._adopt
// enforces updatedAt dominance, so onMeshSnapshot just re-points at the
// mesh's working copy; the mesh master keeps boards alive peer-to-peer while
// the server is down (NOTES-board §5.7 offline honesty — fixed here).
let mesh = null;

let snap = null;   // latest mesh snapshot (mesh.snap working copy)
let sched = null;  // last {"t":"schedule"} frame rows (else null → compute)
let editing = false;

const serverNow = () => (mesh ? mesh.now() : Date.now());

function onMeshSnapshot() {
  if (!mesh || !mesh.snap) return;
  const s = mesh.snap;
  if (snap && s.updatedAt < snap.updatedAt) return; // stale (paranoia; mesh guards too)
  snap = s;
  // E3 blackout: same body-flag paint as timerpi.js (board owns the only
  // mesh session here, so this is the single repaint site).
  if (document.body) document.body.dataset.blanked = s.show?.blanked ? 'true' : 'false';
  if (sched && sched.dayStartTS !== (s.runtime.dayStartTS || 0)) sched = null;
  renderStatic();
  updateLink();
}

let flashTimer = 0;

function initMesh() {
  if (!code) return;
  mesh = new Mesh({
    showId: code,
    role: 'display',
    // F1: boards self-register a stable screen name (operator assigns
    // theme/board from the dashboard's Screens panel); the gallery's
    // preview iframe edits without registering.
    screen: previewMode ? '' : screenName(),
    onSnapshot: onMeshSnapshot,
    onStatusChange: updateLink,
    onFrame: (m) => {
      switch (m.t) {
        case 'schedule':
          if (m.rows) {
            sched = { rows: m.rows, totalMS: m.totalMS || 0, dayStartTS: m.dayStartTS || 0 };
            renderStatic();
          }
          break;
        case 'screen-look':
          if (!editable) {
            // A phone/tablet follows its own orientation (U1): Mounted is ignored.
            if (m.rotation && !document.documentElement.dataset.deviceOrient) document.documentElement.dataset.rotate = String(m.rotation);
            else delete document.documentElement.dataset.rotate;
            if (m.kind) body.dataset.kind = m.kind;
          }
          break;
        case 'flash':
          // The operator's Flash: presenter screens blink the timer.
          if (body.dataset.kind === 'presenter') {
            body.classList.add('b-flash');
            clearTimeout(flashTimer);
            flashTimer = setTimeout(() => body.classList.remove('b-flash'), m.ms || 5000);
          }
          break;
        case 'poll':
          // PLAN §11.5: poll-only deltas ride the lane — boards stay live
          // on votes without a full snapshot fanout.
          if (mesh.snap) {
            mesh.snap.poll = m.poll || null;
            if ('presenter' in m) mesh.snap.presenter = m.presenter || null;
            if ((snap === mesh.snap || !snap) && hasAudienceTile()) { snap = mesh.snap; renderStatic(); }
          }
          break;
        case 'message':
          if (m.message && mesh.snap) {
            const list = mesh.snap.messages.filter((x) => x.id !== m.message.id);
            if (m.message.shownAt || m.message.text) list.push(m.message);
            mesh.snap.messages = list;
            mesh.snap.updatedAt = Math.max(mesh.snap.updatedAt, m.updatedAt || 0) + 1;
            if (snap === mesh.snap || !snap) { snap = mesh.snap; renderStatic(); }
          }
          break;
        case 'timer':
        case 'cue':
          if (m.runtime && mesh.snap && (m.updatedAt ?? 0) >= mesh.snap.updatedAt) {
            Object.assign(mesh.snap.runtime, m.runtime);
            mesh.snap.updatedAt = m.updatedAt ?? mesh.snap.updatedAt;
            if (m.serverTime) mesh.clockOffset = m.serverTime - Date.now();
            snap = mesh.snap;
            if (sched && sched.dayStartTS !== (mesh.snap.runtime.dayStartTS || 0)) sched = null;
            renderStatic();
          }
          break;
        default:
          // C2 (2026-10-04): the board page's mesh is THIS module's — the
          // operator-pushed theme swap lands here (bundle + attr + icons via
          // theme.js; policy REVIEW-2 R6). Everything else (oob/targets for
          // dashboard surfaces, peers/signal) is mesh-internal or belongs to
          // pages we are not on.
          if (m.t === 'display' && m.theme && m.theme !== document.documentElement.getAttribute('data-theme')) {
            applyTheme(m.theme);
          }
          // F1: the operator re-assigned this screen's board — navigate
          // (locked screens only; an open editor must not lose its draft).
          // boardId > 0 = an event layout; else template = a built-in
          // shown directly (U10); neither = "plain timer" (RW35).
          if (m.t === 'screen-board' && !editable) {
            const u = new URL(location.href);
            const want = Number(m.boardId) || 0;
            const tpl = want > 0 ? '' : (m.template || '');
            if (want > 0) { u.searchParams.set('view', 'board'); u.searchParams.set('board', String(want)); u.searchParams.delete('tpl'); }
            else if (tpl) { u.searchParams.set('view', 'board'); u.searchParams.set('tpl', tpl); u.searchParams.delete('board'); }
            else { u.searchParams.delete('view'); u.searchParams.delete('board'); u.searchParams.delete('tpl'); }
            if (u.toString() !== location.href) location.replace(u.toString());
          }
          break;
      }
    },
    onLog: () => { /* boards stay quiet; the link chip carries state */ },
  });
  mesh.start();
  updateLink();
}

// Honest link state (ftl .connection, same as the .tp-dv-link chip on the variant
// boards): LINK LIVE while the server OR any mesh path carries the show —
// that includes being the mesh master ourselves. The strip only covers the
// truly-dark case (no server, no master, no open peer). Before the first
// snapshot the server-rendered initials are the content, so the strip waits
// for snap (same pre-snapshot-hole rule as the variant module).
function updateLink() {
  if (mesh) applyWaiting(mesh.wsStatus); // orphaned board tab raises the waiting overlay
  const strip = $('#tp-offline');
  const chip = $('#b-link');
  const live = mesh ? (mesh.serverOnline() || mesh.isMaster() || mesh.openPeerIds().length > 0) : false;
  if (chip) {
    chip.dataset.state = live ? 'live' : 'offline';
    setText(chip, live ? 'LINK LIVE' : 'LINK DOWN');
  }
  if (strip) strip.classList.toggle('is-visible', !live && !!snap);
}

/* --------------------------------------------------------------- render -- */

// fmtRemaining always prints tenths below 10 s; tenths:"0" strips them.
function fmtClock(ms, tenths) {
  const t = fmtRemaining(ms);
  return tenths === '0' ? t.replace(/\.\d$/, '') : t;
}

function nextPlan() {
  if (!snap) return { row: null, cue: null };
  const r = snap.runtime;
  const rows = sched?.rows || computeSchedule(snap).rows;
  const nextPos = r.nextPos || (snap.cues.find((c) => c.pos > (r.activePos || 0)) || snap.cues[0] || {}).pos;
  return {
    row: rows.find((x) => x.pos === nextPos) || null,
    cue: snap.cues.find((c) => c.pos === nextPos) || null,
  };
}

function planStart(row) {
  if (!snap || !row) return null;
  return (snap.runtime.dayStartTS || serverNow()) + row.startMS;
}

// Audience interaction tiles (PRODUCT §4.4). Each tile follows ONE push
// target (opts.target: audience | presenter — the DSM template's tile is
// "presenter") and shows whatever item is on it:
//   poll      → any kind (the "Audience item" tile)
//   qa        → Q&A / ideas only (wall + spotlight)
//   wordcloud → word clouds only
// Content appears/disappears with the tile's animation (opts.anim +
// animMS). Screens always animate; only phones honour reduced motion.
function renderAudience(tile, type, w) {
  const box = $('.b-js-poll', tile) || $('.b-js-qa', tile) || $('.b-js-cloud', tile);
  if (!box) return;
  const target = w?.opts?.target === 'presenter' ? 'presenter' : 'audience';
  const p = target === 'presenter' ? snap.presenter : snap.poll;
  const vis = audienceVisible(type, p);
  const was = tile.dataset.bVis === '1';
  // The tile animates in when another item comes on; changes within the
  // item (results, votes, words, questions) animate inside it.
  const sig = vis ? `${p.id}:${p.kind}` : '';
  const changed = tile.dataset.bItem !== sig;
  const { mode, ms } = animOf(w);
  tile.dataset.bVis = vis ? '1' : '0';
  tile.dataset.bItem = sig;
  if (vis) {
    paintAudience(box, p, w);
    if ((!was || changed) && mode !== 'none') playAnim(box, mode, ms, 'in');
    return;
  }
  if (was && mode !== 'none' && box.childElementCount) {
    playAnim(box, mode, ms, 'out', () => { if (tile.dataset.bVis !== '1') clearAudience(box); });
    return;
  }
  if (box._aud || box.childElementCount) clearAudience(box);
}

function audienceVisible(type, p) {
  if (!p || (p.state !== 'open' && p.state !== 'results')) return false;
  if (type === 'qa') return p.kind === 'qa' || p.kind === 'ideas';
  if (type === 'wordcloud') return p.kind === 'wordcloud';
  return true; // 'poll' = the Audience item tile: any kind
}

function mk(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}

// ------------------------------------------------- event walk-in tiles --
// "All rooms now", "Event schedule" and the event map read the walk-in
// feed (every room of the event, from the live timers). Polled: sessions
// change on GO, not per second; remaining time ticks locally.
const walkin = { data: null, at: 0, timer: 0 };
function needsWalkin() {
  return (layout.widgets || []).some((w) => w.type === 'rooms' || w.type === 'eventschedule' || (w.type === 'map' && !w.opts?.assetId));
}
async function pullWalkin() {
  if (!code) return;
  try {
    const j = await api('GET', `/api/shows/${encodeURIComponent(code)}/walkin`);
    if (j.ok) { walkin.data = j; walkin.at = Date.now(); renderStatic(); }
  } catch { /* keep the last data; the next pull retries */ }
}
function startWalkin() {
  if (!needsWalkin() || walkin.timer) return;
  pullWalkin();
  walkin.timer = setInterval(pullWalkin, 5000);
}
function hhmm(ts) {
  return ts ? fmtTimeOfDay(ts).slice(0, 5) : '';
}
// "Current & next" walk-in tile (STATUS U4): for each session its title,
// then Start Time (planned, 24 h), Duration and Speaker; empty facts are
// left out. Current = the running/paused cue, else "—".
function nnDur(ms) {
  const m = Math.round((ms || 0) / 60000);
  if (m < 60) return `${m} min`;
  return m % 60 ? `${Math.floor(m / 60)} h ${m % 60} min` : `${m / 60} h`;
}
function renderNowNext(tile, cue, plan) {
  const r = snap.runtime;
  const rows = sched?.rows || computeSchedule(snap).rows;
  const fill = (box, c) => {
    if (!box) return;
    setText($('.b-js-nn-title', box), c ? c.label : '—');
    const facts = $('.b-js-nn-facts', box);
    if (!facts) return;
    const row = c && rows.find((x) => x.pos === c.pos);
    const start = row && r.dayStartTS ? hhmm(r.dayStartTS + row.startMS) : '';
    if (unchanged(facts, JSON.stringify(c ? [start, c.durationMS, c.speaker, c.location, c.kind] : null))) return;
    facts.textContent = '';
    if (!c) return;
    const who = c.kind === 'break' ? ['Location', c.location || ''] : ['Speaker', c.speaker || ''];
    // ftl dl.props; the spaces keep the text readable as one line.
    for (const [k, v] of [['Start Time', start], ['Duration', nnDur(c.durationMS)], who]) {
      if (v) facts.append(mk('dt', '', k), ' ', mk('dd', '', v), ' ');
    }
  };
  const live = (r.running || r.paused) ? cue : null;
  fill($('.b-js-nn-now', tile), live);
  fill($('.b-js-nn-next', tile), plan.cue && plan.cue !== live ? plan.cue : null);
}
// A schedule line's text (STATUS U3/U14): "Title - Speaker" for a
// session, "Coffee — Great Hall" for a break with a location. Works for
// cues and walk-in feed rows alike.
function schedLabel(c) {
  const isBreak = c.kind === 'break' || c.break === true;
  if (isBreak) return c.location ? `${c.label} — ${c.location}` : c.label;
  return c.speaker ? `${c.label} - ${c.speaker}` : c.label;
}
function renderRooms(tile) {
  const box = $('.b-js-rooms', tile);
  const rooms = walkin.data?.rooms;
  if (!box || !rooms) return;
  if (unchanged(box, JSON.stringify(rooms.map((r) => [r.label || r.name, r.here, r.now && schedLabel(r.now), r.now?.speaker, r.next && schedLabel(r.next), r.next?.startTS])))) return;
  box.textContent = '';
  for (const r of rooms) {
    const card = mk('div', 'b-room' + (r.now ? ' is-live' : '') + (r.here ? ' is-here' : ''));
    card.append(mk('div', 'b-room-name', r.label || r.name));
    card.append(mk('div', 'b-room-now', r.now ? schedLabel(r.now) : (r.next ? 'Next session soon' : 'No more sessions today')));
    if (r.now?.speaker) card.append(mk('div', 'b-room-meta', r.now.speaker));
    if (r.next) card.append(mk('div', 'b-room-meta', `Next${r.next.startTS ? ' ' + hhmm(r.next.startTS) : ''}: ${schedLabel(r.next)}`));
    box.appendChild(card);
  }
}
function renderEventSchedule(tile) {
  const box = $('.b-js-evsched', tile);
  const rooms = walkin.data?.rooms;
  if (!box || !rooms) return;
  if (unchanged(box, JSON.stringify(rooms.map((r) => [r.label || r.name, r.schedule.map((x) => [schedLabel(x), x.startTS, x.state])])))) return;
  box.textContent = '';
  // One ftl .schedule-list per room, side by side.
  for (const r of rooms) {
    const col = mk('div', '');
    const list = mk('ol', 'schedule-list');
    col.append(mk('h3', 'schedule-heading', r.label || r.name), list);
    // Done sessions drop off the top so the column shows what is left.
    const rows = r.schedule.filter((x) => x.state !== 'done');
    for (const s of (rows.length ? rows : r.schedule)) list.appendChild(schedItem(hhmm(s.startTS), schedLabel(s), s.state));
    box.appendChild(col);
  }
}

// schedItem builds one ftl .schedule-item: state 'done' is past, 'now' is
// aria-current="time". The plain space keeps "hh:mm Title" readable as text
// (an en space would become an anonymous grid item).
function schedItem(time, label, state) {
  const li = mk('li', 'schedule-item' + (state === 'done' ? ' is-past' : ''));
  if (state === 'now') li.setAttribute('aria-current', 'time');
  const body = mk('div', 'schedule-body');
  body.append(mk('span', 'schedule-title', label));
  li.append(mk('time', 'schedule', time), ' ', body);
  return li;
}

// Animation for a tile: its own opts.anim / animMS, else the layout's
// default (PRODUCT L2/L3). Screens always animate.
function animOf(w) {
  const mode = w?.opts?.anim || layout.anim || 'fade';
  const ms = Number(w?.opts?.animMS) || Number(layout.animMS) || 400;
  return { mode, ms };
}

// Every content tile animates when what it says changes (new session,
// new message, next-up moves on…). Ticking tiles (clocks, bars) don't.
const TICKING = new Set(['countdown', 'wallclock', 'progress', 'dayprogress', 'rate', 'poll', 'qa', 'wordcloud']);
function animateChanges() {
  if (!grid || editing) return;
  for (const tile of $$('.b-widget', grid)) {
    const type = tile.dataset.widget;
    if (TICKING.has(type)) continue;
    const body = $('.b-w-body', tile);
    if (!body) continue;
    const sig = body.textContent.replace(/\s+/g, ' ').trim();
    const before = tile.dataset.bSig;
    tile.dataset.bSig = sig;
    if (before === undefined || before === sig) continue;
    const { mode, ms } = animOf(widgetOf(tile.dataset.wid));
    if (mode !== 'none') playAnim(body, mode, ms, 'in');
  }
}

function playAnim(box, mode, ms, dir, done) {
  box.style.setProperty('--b-anim-ms', `${ms}ms`);
  const cls = `b-anim-${dir}-${mode}`;
  box.classList.add(cls);
  const cleanup = () => {
    box.classList.remove(cls);
    if (done) done();
  };
  box.addEventListener('animationend', cleanup, { once: true });
  setTimeout(cleanup, ms + 120); // animationend can be swallowed mid-repaint
}

// Structural repaint (adopt/schedule/opts change): labels, lists, messages.
function renderStatic() {
  renderStaticBody();
  animateChanges();
}

function renderStaticBody() {
  if (!snap || !grid) return;
  const cue = activeCue(snap);
  const plan = nextPlan();
  for (const tile of $$('.b-widget', grid)) {
    const type = tile.dataset.widget;
    const w = widgetOf(tile.dataset.wid);
    switch (type) {
      case 'countdown':
        setText($('.b-js-cdlabel', tile), cue?.label || '');
        break;
      case 'cuelabel': {
        const src = w?.opts?.source || 'label';
        setText($('.b-js-cuelabel', tile), src === 'speaker' ? (cue?.speaker || '') : (cue?.label || ''));
        break;
      }
      case 'speaker':
        setText($('.b-js-speaker', tile), cue?.speaker ? `🎙 ${cue.speaker}` : '');
        break;
      case 'nownext':
        renderNowNext(tile, cue, plan);
        break;
      case 'nextup':
        setText($('.b-js-nextlabel', tile), plan.cue?.label || '');
        setText($('.b-js-nextdur', tile), plan.cue ? fmtDuration(plan.cue.durationMS) : '');
        setText($('.b-js-nextstart', tile), plan.row && planStart(plan.row) ? fmtTimeOfDay(planStart(plan.row)) : '--:--:--');
        break;
      case 'messages': {
        const box = $('.b-js-msgs', tile);
        if (box && !unchanged(box, JSON.stringify(snap.messages.map((m) => [m.id, m.text, m.color])))) {
          box.textContent = '';
          if (!snap.messages.length) {
            const d = document.createElement('div');
            d.className = 'b-msg-empty';
            d.textContent = 'No messages on stage';
            box.appendChild(d);
          }
          for (const m of snap.messages) {
            const d = document.createElement('div');
            d.className = 'b-msg';
            if (m.color) d.style.borderColor = m.color;
            const inner = document.createElement('div');
            inner.textContent = m.text;
            d.appendChild(inner);
            box.appendChild(d);
          }
        }
        break;
      }
      case 'showtitle':
        setText($('.b-js-showtitle', tile), snap.show?.title ? (document.body.dataset.roomPrefix || '') + snap.show.title : '');
        break;
      case 'notice':
        setText($('.b-js-notice', tile), w?.opts?.text || '');
        break;
      case 'rate':
        setText($('.b-js-rate', tile), `×${Number(snap.runtime.rate || 1).toFixed(2)}`);
        break;
      case 'poll':
      case 'qa':
      case 'wordcloud':
        renderAudience(tile, type, w);
        break;
      case 'map': {
        const img = $('.b-js-map', tile);
        const empty = $('.b-map-empty', tile);
        const aid = String(w?.opts?.assetId || '').replace(/[^0-9]/g, '');
        const evMap = walkin.data?.event?.map || '';
        if (aid || evMap) {
          const url = aid ? `/assets/${aid}` : evMap;
          if (!img.getAttribute('src')?.endsWith(url)) img.src = url;
          img.hidden = false;
          if (empty) empty.hidden = true;
        } else {
          img.removeAttribute('src');
          img.hidden = true;
          if (empty) empty.hidden = false;
        }
        break;
      }
      case 'rooms':
        renderRooms(tile);
        break;
      case 'eventschedule':
        renderEventSchedule(tile);
        break;
      case 'joinqr': {
        const lbl = $('.b-js-joinlabel', tile);
        if (lbl) lbl.textContent = w?.opts?.label || 'Scan to take part';
        break;
      }
      case 'progress':
        setText($('.b-js-progresslabel', tile), cue?.label || '');
        break;
      case 'schedule': {
        // Walk-in style (STATUS U3): "hh:mm  Title - Speaker", 24 h, no
        // duration; no time until the day has a start.
        const ul = $('.b-js-sched', tile);
        const rows = sched?.rows || computeSchedule(snap).rows;
        const byPos = new Map(rows.map((r) => [r.pos, r]));
        const base = snap.runtime.dayStartTS || serverNow();
        const want = w?.opts?.count || '5';
        const list = want === 'all' ? snap.cues : snap.cues.slice(0, Number(want) || 5);
        const key = JSON.stringify([snap.runtime.dayStartTS, snap.runtime.activePos,
          list.map((c) => [c.pos, schedLabel(c), byPos.get(c.pos)?.startMS])]);
        if (ul && !unchanged(ul, key)) {
          ul.textContent = '';
          if (!list.length) {
            const li = mk('li', 'schedule-item is-free');
            li.append(mk('div', 'schedule-body'));
            li.firstChild.append(mk('span', 'schedule-title', 'No cues yet — build the running order in the control room.'));
            ul.appendChild(li);
          }
          const act = snap.runtime.activePos;
          for (const c of list) {
            const planRow = byPos.get(c.pos);
            const li = schedItem(planRow && snap.runtime.dayStartTS ? hhmm(base + planRow.startMS) : '',
              schedLabel(c), act && c.pos === act ? 'now' : act && c.pos < act ? 'done' : '');
            li.dataset.pos = String(c.pos);
            ul.appendChild(li);
          }
        }
        break;
      }
      default: break;
    }
  }
  tick(); // structural change implies fresh digits too
}

// Fast repaint (~10 Hz): clocks, bars, countdown, time-until.
function tick() {
  if (!snap || !grid) return;
  const now = serverNow();
  const cue = activeCue(snap);
  const plan = nextPlan();
  for (const tile of $$('.b-widget', grid)) {
    const type = tile.dataset.widget;
    const w = widgetOf(tile.dataset.wid);
    switch (type) {
      case 'countdown': {
        const view = clockView(snap, now);
        tile.dataset.state = view.state;
        const el = $('.b-js-clock', tile);
        if (el) {
          el.dataset.state = view.state;
          el.classList.toggle('is-flash', !!((view.alert === 1 && cue?.alertFlash1) || (view.alert === 2 && cue?.alertFlash2)));
          if (view.state === 'blank') setText(el, '—');
          else if (view.state === 'held') setText(el, '0:00');
          else if (view.state === 'idle' || view.state === 'armed') {
            setText(el, cue ? fmtDuration(cue.durationMS) : '--:--');
          } else if (view.remaining != null) {
            setText(el, fmtClock(view.remaining, w?.opts?.tenths));
          }
        }
        break;
      }
      case 'wallclock': {
        const el = $('.b-js-wall', tile);
        if (el) {
          let t = fmtTimeOfDay(now);
          if (w?.opts?.tenths === '1') t += `.${Math.floor((now % 1000) / 100)}`;
          setText(el, t);
        }
        break;
      }
      case 'progress': {
        const fill = $('.b-js-progress', tile);
        if (fill && cue && cue.durationMS > 0) {
          const e = Math.min(Math.max(elapsedMS(snap, now), 0), cue.durationMS);
          // UX2: 2-decimal fractions — subpixel antialiasing was smearing the
          // fill edge ("blurry bar"); keep the numbers short and the edge crisp.
          fill.style.width = `${((e / cue.durationMS) * 100).toFixed(2)}%`;
        } else if (fill) {
          fill.style.width = '0%';
        }
        break;
      }
      case 'dayprogress': {
        // ftl .gauge-linear: the meter level and the "now" mark move together.
        const meter = $('.b-js-dayprogress', tile);
        if (meter) {
          const tot = sched ? sched.totalMS : computeSchedule(snap).totalMS;
          const base = snap.runtime.dayStartTS;
          const pct = base && tot > 0 ? (Math.min(Math.max(now - base, 0), tot) / tot) * 100 : 0;
          meter.style.setProperty('--meter-level', `${pct.toFixed(2)}%`);
          $('.b-js-daynow', tile)?.style.setProperty('--at', `${pct.toFixed(2)}%`);
          const v = String(Math.round(pct));
          if (meter.getAttribute('aria-valuenow') !== v) meter.setAttribute('aria-valuenow', v);
        }
        break;
      }
      case 'nextup': {
        const at = planStart(plan.row);
        if (at) {
          const left = at - now;
          setText($('.b-js-nextin', tile), (left >= 0 ? 'in ' : '+') + fmtDuration(left));
        }
        break;
      }
      default: break;
    }
  }
}

/* --------------------------------------------------------------- compose -- */

const saveStateEl = () => $('#b-save-state');
function saveState(t) { const el = saveStateEl(); if (el) setText(el, t); }

let saveTimer = 0;
function scheduleSave() {
  if (!editable) return;
  saveState('Saving…');
  clearTimeout(saveTimer);
  saveTimer = setTimeout(saveNow, 600);
}

// saveNow PUTs the layout and reports whether the server accepted it.
async function saveNow() {
  if (!editable || !code || !boardId) return false;
  clearTimeout(saveTimer);
  saveState('Saving…');
  try {
    await api('PUT', `/api/shows/${encodeURIComponent(code)}/boards/${encodeURIComponent(boardId)}`,
      { layout: { v: 1, rows: canvasRows(), orientation: layout.orientation || 'landscape', anim: layout.anim || 'fade', animMS: Number(layout.animMS) || 400, widgets: layout.widgets } });
    saveState(`Saved ${fmtTimeOfDay(Date.now())}`);
    lastSaved = structuredClone(layout);
    return true;
  } catch (e) {
    saveState(`Save failed: ${e.message}`);
    return false;
  }
}

// Client-side mirror of the server clamp (server re-validates on PUT).
// The canvas: 12 columns × rows (layout.rows; never fewer than the tiles
// need). Tiles stay inside it.
function canvasRows() {
  const ext = (layout.widgets || []).reduce((n, w) => Math.max(n, (w.y | 0) + (w.h | 0)), 0);
  return Math.max(1, Math.min(48, Math.max(layout.rows | 0 || (layout.orientation === 'portrait' ? 16 : 8), ext)));
}

function clampTile(t) {
  const rows = Math.max(1, layout.rows | 0 || canvasRows());
  t.x = Math.max(0, Math.min(11, t.x | 0));
  t.w = Math.max(1, Math.min(12, t.w | 0));
  if (t.x + t.w > 12) t.w = 12 - t.x;
  t.h = Math.max(1, Math.min(rows, t.h | 0));
  t.y = Math.max(0, Math.min(rows - t.h, t.y | 0));
  return t;
}

function overlaps(a, b) {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

function applyGeometry(wid) {
  const w = widgetOf(wid);
  const tile = tileOf(wid);
  if (!w || !tile) return;
  tile.style.gridColumn = `${w.x + 1} / span ${w.w}`;
  tile.style.gridRow = `${w.y + 1} / span ${w.h}`;
  // Mobile list editor listens per tile to keep its digits true.
  window.dispatchEvent(new CustomEvent('tp-geom-' + wid));
}

// Editing handles follow common editor practice (STATUS U8): every handle
// has a visible glyph, a tooltip and an accessible name — ⠿ grip = move,
// ◢ corner = resize, ⚙ = settings, bin = remove.
function labelHandles(tile) {
  const chrome = $('.b-w-chrome', tile);
  if (chrome && !chrome.dataset.labelled) {
    chrome.dataset.labelled = '1';
    const title = $('.b-w-title', chrome)?.textContent || 'tile';
    chrome.title = `Drag to move ${title}`;
    const grip = document.createElement('span');
    grip.className = 'b-w-grip';
    grip.setAttribute('aria-hidden', 'true');
    grip.textContent = '⠿';
    chrome.prepend(grip);
    const gear = $('.b-w-gear', chrome);
    if (gear) gear.title = `${title} settings`;
    const del = $('.b-w-del', chrome);
    if (del) del.title = `Remove ${title}`;
  }
  const rs = $('.b-w-resize', tile);
  if (rs) rs.title = 'Drag to resize';
}

function addAlignButtons(tile) {
  labelHandles(tile);
  const chrome = $('.b-w-chrome', tile);
  if (!chrome || chrome.querySelector('[data-align]')) return;
  for (const side of ['left', 'right']) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'btn btn-icon btn-sm is-clear';
    b.dataset.align = side;
    b.textContent = side === 'left' ? '⯇' : '⯈';
    b.title = `Align ${side} edge`;
    b.setAttribute('aria-label', `Align tile ${side}`);
    chrome.appendChild(b);
  }
}

// Cell pitch of the stretched canvas (column/row size + gap), measured.
function gridMetrics() {
  const cs = getComputedStyle(grid);
  const padX = parseFloat(cs.paddingLeft) + parseFloat(cs.paddingRight);
  const padY = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom);
  const gapX = parseFloat(cs.columnGap) || 0;
  const gapY = parseFloat(cs.rowGap) || 0;
  const rows = canvasRows();
  return {
    colW: (grid.clientWidth - padX + gapX) / 12,
    rowPitch: (grid.clientHeight - padY + gapY) / rows,
  };
}

// Drag ghost (owner "feels bad" round): a translucent outline marks the
// destination cells while dragging, so the drop target is visible before
// the pointer releases — with the tile itself held semi-transparent.
function showGhost(w) {
  let g = document.getElementById('b-drag-ghost');
  if (!g) {
    g = document.createElement('div');
    g.id = 'b-drag-ghost';
    grid.appendChild(g);
  }
  // ftl .widget-placeholder, placed on the destination cells.
  g.className = 'widget-placeholder';
  g.style.gridColumn = `${w.x + 1} / span ${w.w}`;
  g.style.gridRow = `${w.y + 1} / span ${w.h}`;
}
function hideGhost() {
  document.getElementById('b-drag-ghost')?.remove();
}

// The tile the arrow keys nudge: set by a drag or resize (dragTile), read
// by the keydown handler in wireCompose. Module scope, so both see it
// (BUGLOG RC3: it used to be local to wireCompose and every drag threw).
let lastTouched = null;

function dragTile(tile, wid, startEvent, mode) {
  startEvent.preventDefault();
  const w = widgetOf(wid);
  if (!w) return;
  const { colW, rowPitch } = gridMetrics();
  const startX = startEvent.clientX;
  const startY = startEvent.clientY;
  const orig = { ...w };
  tile.classList.add('is-dragging');
  showGhost(w);
  const move = (e) => {
    const dc = Math.round((e.clientX - startX) / colW);
    const dr = Math.round((e.clientY - startY) / rowPitch);
    if (mode === 'move') {
      w.x = orig.x + dc;
      w.y = orig.y + dr;
    } else {
      w.w = orig.w + dc;
      w.h = orig.h + dr;
    }
    clampTile(w);
    applyGeometry(w.id);
    showGhost(w);
  };
  const up = () => {
    tile.classList.remove('is-dragging');
    hideGhost();
    window.removeEventListener('pointermove', move);
    window.removeEventListener('pointerup', up);
    window.removeEventListener('pointercancel', up);
    clampTile(w);
    lastTouched = { id: wid, w: { ...w } }; // arrow keys nudge from here
    // Overlap (against every OTHER tile) reverts the gesture, flashes a brief
    // purple nudge on the offending tile (UX2 — visible at the pointer, not
    // only in the far toolbar) and stays revert-only: never silently accept.
    const hit = (layout.widgets || []).some((o) => o.id !== w.id && overlaps(w, o));
    if (hit) {
      Object.assign(w, orig);
      applyGeometry(w.id);
      tile.classList.remove('b-overlap');
      void tile.offsetWidth; // restart the shake on rapid retry
      tile.classList.add('b-overlap');
      setTimeout(() => tile.classList.remove('b-overlap'), 1000);
      saveState('Blocked: tiles overlap');
      return;
    }
    if (w.x !== orig.x || w.y !== orig.y || w.w !== orig.w || w.h !== orig.h) scheduleSave();
  };
  window.addEventListener('pointermove', move);
  window.addEventListener('pointerup', up);
  window.addEventListener('pointercancel', up);
}

/**
 * Mobile tile editor (NOTES-board §5.3, shipped 2026-10-04): pointer
 * drag/resize needs precision a phone finger doesn't have at 4.5rem rows ×
 * 12 cols. Below 760 px, while editing, the #b-editor panel lists every
 * tile with X/Y (move) and W/H (resize) steppers — the SAME widget
 * documents as the grid, so clampTile/applyGeometry/scheduleSave overlap
 * rules (and autosave) run identically for both gesture paths. Desktop
 * tiers never display it (CSS keeps it at display:none) — the grid remains
 * the tool there.
 */
// Geom listeners live on window (applyGeometry dispatches there); each
// rebuild tears down the previous batch or Edit-toggles pile handlers on
// dead rows (48 stale closures per toggle on a 12-tile board).
const geomListeners = [];

function buildEditorList(editing) {
  const editor = $('#b-editor');
  if (!editor) return;
  for (const [ev, fn] of geomListeners) window.removeEventListener(ev, fn);
  geomListeners.length = 0;
  if (!editing) { editor.hidden = true; editor.textContent = ''; return; }
  editor.hidden = true; // shown by the ≤760px CSS under data-editing
  editor.textContent = '';
  const title = document.createElement('strong');
  title.textContent = 'Tiles';
  editor.appendChild(title);
  const grid1x = (wid, field, delta, minmax, label) => {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'btn btn-sm';
    b.textContent = delta > 0 ? '+' : '−';
    b.setAttribute('aria-label', `${widgetOf(wid)?.type ?? 'tile'} ${label} ${delta > 0 ? '+' : '−'}`);
    b.addEventListener('click', () => {
      const w = widgetOf(wid);
      if (!w) return;
      const orig = { ...w };
      w[field] = (w[field] | 0) + delta;
      clampTile(w);
      if ((layout.widgets || []).some((o) => o.id !== w.id && overlaps(w, o))) {
        Object.assign(w, orig);
        applyGeometry(wid);
        saveState('Blocked: tiles overlap');
        return;
      }
      if (orig[field] !== w[field]) { applyGeometry(wid); scheduleSave(); }
    });
    return b;
  };
  for (const w of layout.widgets || []) {
    const row = document.createElement('div');
    row.className = 'b-ed-row';
    const name = document.createElement('span');
    name.className = 'b-ed-name';
    setText(name, w.type);
    row.appendChild(name);
    for (const [field, label] of [['x', 'X'], ['y', 'Y'], ['w', 'W'], ['h', 'H']]) {
      const grp = document.createElement('span');
      grp.className = 'cluster is-gap-2xs';
      grp.appendChild(grid1x(w.id, field, -1, null, label));
      const v = document.createElement('span');
      v.className = 'mono b-ed-val';
      const update = () => setText(v, String(widgetOf(w.id)?.[field] ?? ''));
      update();
      // Exact-refresh: applyGeometry dispatches tp-geom-<wid> after each write.
      geomListeners.push(['tp-geom-' + w.id, update]);
      window.addEventListener('tp-geom-' + w.id, update);
      grp.appendChild(v);
      grp.appendChild(grid1x(w.id, field, +1, null, label));
      row.appendChild(grp);
    }
    const gear = document.createElement('button');
    gear.type = 'button';
    gear.className = 'btn btn-sm btn-icon';
    gear.textContent = '⚙';
    gear.setAttribute('aria-label', `${w.type} settings`);
    gear.addEventListener('click', () => { openSettings(w.id); tick(); });
    row.appendChild(gear);
    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'btn btn-sm btn-icon btn-danger';
    del.textContent = '🗑';
    del.setAttribute('aria-label', `Delete ${w.type} tile`);
    del.addEventListener('click', async () => {
      const rest = (layout.widgets || []).filter((x) => x.id !== w.id);
      if (!rest.length) { saveState('A board needs at least one tile'); return; }
      layout.widgets = rest;
      await reloadEditing();
    });
    row.appendChild(del);
    editor.appendChild(row);
  }
  const note = document.createElement('span');
  note.className = 'field-hint';
  note.textContent = 'X/Y move a tile; W/H resize it. Autosaves on change.';
  editor.appendChild(note);
}

function clipBytes(s, n) {
  const enc = new TextEncoder();
  let bytes = 0;
  let out = '';
  for (const ch of s) {
    const b = enc.encode(ch).length;
    if (bytes + b > n) break;
    bytes += b;
    out += ch;
  }
  return out;
}

// Per-widget settings form (tenths / label source / schedule count /
// notice text).
function openSettings(wid) {  const w = widgetOf(wid);
  const form = $('#b-settings');
  if (!w || !form) return;
  form.textContent = '';
  form.hidden = false;
  const title = document.createElement('strong');
  title.textContent = `Tile ${w.type} (${w.id})`;
  form.appendChild(title);
  const mk = (labelText, control) => {
    const lab = document.createElement('label');
    lab.textContent = labelText;
    lab.appendChild(control);
    form.appendChild(lab);
  };
  if (w.type === 'countdown' || w.type === 'wallclock') {
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.checked = (w.opts?.tenths || '0') === '1';
    cb.setAttribute('aria-label', 'Show tenths');
    cb.addEventListener('change', () => {
      w.opts = { ...(w.opts || {}), tenths: cb.checked ? '1' : '0' };
      scheduleSave();
      tick();
    });
    mk('Tenths', cb);
  }
  if (w.type === 'cuelabel') {
    const sel = document.createElement('select');
    sel.className = 'select input-sm';
    for (const v of ['label', 'speaker']) {
      const o = document.createElement('option');
      o.value = v;
      o.textContent = v === 'label' ? 'Cue label' : 'Speaker';
      sel.appendChild(o);
    }
    sel.value = w.opts?.source || 'label';
    sel.addEventListener('change', () => {
      w.opts = { ...(w.opts || {}), source: sel.value };
      scheduleSave();
      renderStatic();
    });
    mk('Source cue field', sel);
  }
  if (w.type === 'schedule') {
    const sel = document.createElement('select');
    sel.className = 'select input-sm';
    for (const v of ['3', '5', '8', 'all']) {
      const o = document.createElement('option');
      o.value = v;
      o.textContent = v === 'all' ? 'All cues' : `${v} cues`;
      sel.appendChild(o);
    }
    sel.value = w.opts?.count || '5';
    sel.addEventListener('change', () => {
      w.opts = { ...(w.opts || {}), count: sel.value };
      scheduleSave();
      renderStatic();
    });
    mk('Rows', sel);
  }
  if (w.type === 'poll' || w.type === 'qa' || w.type === 'wordcloud') {
    const tsel = document.createElement('select');
    tsel.className = 'input input-sm';
    for (const [v, label] of [['audience', 'Shown to Audience'], ['presenter', 'Shown to Presenter']]) {
      const o = document.createElement('option');
      o.value = v;
      o.textContent = label;
      tsel.appendChild(o);
    }
    tsel.value = w.opts?.target === 'presenter' ? 'presenter' : 'audience';
    tsel.addEventListener('change', () => {
      w.opts = { ...(w.opts || {}), target: tsel.value };
      scheduleSave();
      renderStatic();
    });
    mk('Follows', tsel);
  }
  if (!['countdown', 'wallclock', 'progress', 'dayprogress', 'rate'].includes(w.type)) {
    // Operator-customisable appear/disappear animation (PRODUCT L2/L3):
    // blank = the layout's default (set in the Canvas controls).
    const sel = document.createElement('select');
    sel.className = 'input input-sm';
    for (const [v, label] of [['', 'Layout default'], ['fade', 'Fade'], ['slide', 'Slide'], ['pop', 'Pop'], ['none', 'None']]) {
      sel.appendChild(new Option(label, v));
    }
    sel.value = w.opts?.anim || '';
    sel.addEventListener('change', () => {
      const o = { ...(w.opts || {}) };
      if (sel.value) o.anim = sel.value; else delete o.anim;
      w.opts = o;
      scheduleSave();
      renderStatic();
    });
    mk('Animation', sel);
    const num = document.createElement('input');
    num.className = 'input input-sm';
    num.type = 'number';
    num.min = '120';
    num.max = '3000';
    num.step = '20';
    num.placeholder = String(layout.animMS || 400);
    num.value = w.opts?.animMS || '';
    num.setAttribute('aria-label', 'Animation duration (ms)');
    num.addEventListener('change', () => {
      const o = { ...(w.opts || {}) };
      if (num.value === '') delete o.animMS;
      else { const v = String(Math.min(3000, Math.max(120, Number(num.value) || 400))); num.value = v; o.animMS = v; }
      w.opts = o;
      scheduleSave();
    });
    mk('Duration (ms)', num);
  }

  if (w.type === 'map') {
    // Asset PICKER (owner "fiddly" round): select a named upload instead
    // of typing a raw id.
    const sel = document.createElement('select');
    sel.className = 'input input-sm';
    const fill = (assets) => {
      sel.textContent = '';
      const none = document.createElement('option');
      none.value = '0';
      none.textContent = 'No map';
      sel.appendChild(none);
      for (const a of assets || []) {
        const o = document.createElement('option');
        o.value = String(a.id);
        o.textContent = a.name || `Asset ${a.id}`;
        sel.appendChild(o);
      }
      sel.value = w.opts?.assetId || '0';
      if (!sel.value || sel.selectedIndex < 0) sel.value = '0';
    };
    sel.value = w.opts?.assetId || '0';
    fill([]);
    api('GET', `/api/assets?room=${encodeURIComponent(code)}`).then((j) => fill(j?.assets || [])).catch(() => {});
    sel.setAttribute('aria-label', 'Map asset');
    sel.addEventListener('change', () => {
      w.opts = { ...(w.opts || {}), assetId: sel.value.replace(/[^0-9]/g, '') };
      scheduleSave();
      renderStatic();
    });
    mk('Map image', sel);
  }
  if (w.type === 'joinqr') {
    const inp = document.createElement('input');
    inp.className = 'input input-sm';
    inp.value = w.opts?.label || '';
    inp.placeholder = 'Scan to take part';
    inp.setAttribute('aria-label', 'QR caption');
    inp.addEventListener('change', () => {
      w.opts = { ...(w.opts || {}), label: clipBytes(inp.value, 120) };
      scheduleSave();
      renderStatic();
    });
    mk('Caption', inp);
  }
  if (w.type === 'notice') {
    const ta = document.createElement('textarea');
    ta.className = 'input input-sm';
    ta.rows = 3;
    ta.value = w.opts?.text || '';
    ta.setAttribute('aria-label', 'Notice text');
    ta.addEventListener('change', () => {
      // servers clip at 256 BYTES (rune-safe since the review); clamp to
      // the same budget here so the editor shows what the TVs will.
      const v = clipBytes(ta.value, 256);
      ta.value = v;
      w.opts = { ...(w.opts || {}), text: v };
      scheduleSave();
      renderStatic();
    });
    mk('Text', ta);
  }
  if (!form.children.length || form.children.length === 1) {
    const none = document.createElement('span');
    none.className = 'text-muted';
    none.textContent = 'No settings for this tile.';
    form.appendChild(none);
  }
  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'btn btn-sm';
  close.textContent = 'Done';
  close.addEventListener('click', () => { form.hidden = true; });
  form.appendChild(close);
}

// MAX_ROWS mirrors boards.MaxRows: no tile may reach below it (BUGLOG RW33).
const MAX_ROWS = 48;

// freeSpot finds the first free cell block for a w×h tile inside the
// canvas, or null when the board is full.
function freeSpot(w, h) {
  const taken = (x, y) => (layout.widgets || []).some((o) => overlaps({ x, y, w, h }, o));
  for (let y = 0; y + h <= MAX_ROWS; y++) {
    for (let x = 0; x + w <= 12; x++) {
      if (!taken(x, y)) return { x, y };
    }
  }
  return null;
}

// reloadEditing saves a structural change (add, delete, template, reset,
// orientation) and reloads so the server re-renders the tiles. A failed
// save keeps the page and its "Save failed" message, and rolls the local
// layout back to what the server holds (BUGLOG RW43).
async function reloadEditing() {
  if (!(await saveNow())) {
    layout = structuredClone(lastSaved);
    const orient = $('#b-orient');
    if (orient) orient.value = layout.orientation === 'portrait' ? 'portrait' : 'landscape';
    return false;
  }
  try { sessionStorage.setItem('b-editing', '1'); } catch { /* private mode */ }
  location.reload();
  return true;
}

function wireKeys() {
  // Keyboard nudge (owner "feels bad" round): with editing on, the
  // last-touched tile moves by one cell on the arrow keys (Shift resizes),
  // drop-style confirm on every nudge — overlap reverts exactly like a drag.
  document.addEventListener('keydown', (e) => {
    if (!editing || !lastTouched) return;
    const deltas = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };
    const d = deltas[e.key];
    if (!d) return;
    e.preventDefault();
    const w = widgetOf(lastTouched.id);
    if (!w) { lastTouched = null; return; }
    const orig = { ...w };
    if (e.shiftKey) { w.w = Math.max(1, w.w + d[0]); w.h = Math.max(1, w.h + d[1]); }
    else { w.x += d[0]; w.y += d[1]; }
    clampTile(w);
    applyGeometry(w.id);
    const tile = document.getElementById('b-w-' + lastTouched.id);
    const hit = (layout.widgets || []).some((o) => o.id !== w.id && overlaps(w, o));
    if (hit) {
      Object.assign(w, orig);
      applyGeometry(w.id);
      if (tile) tile.classList.add('b-overlap');
      setTimeout(() => tile?.classList.remove('b-overlap'), 600);
      saveState('Blocked: tiles overlap');
      return;
    }
    lastTouched.w = { ...w };
    scheduleSave();
  });
}

function wireCompose() {
  if (!editable || !grid) return;
  const toggle = $('#b-edit-toggle');
  const palette = $('#b-palette');
  const settings = $('#b-settings');
  const setEditing = (on) => {
    editing = on;
    body.toggleAttribute('data-editing', on);
    if (toggle) {
      toggle.textContent = on ? 'Done' : 'Edit layout';
      toggle.setAttribute('aria-pressed', on ? 'true' : 'false');
    }
    if (palette) palette.hidden = !on;
    // Edit-mode visuals come from ftl's dashboard edit state (outlines,
    // grab handle, resize corner, column guides); placement stays ours.
    grid.classList.toggle('dashboard', on);
    grid.classList.toggle('is-editing', on);
    for (const tile of $$('.b-widget', grid)) tile.classList.toggle('widget', on);
    if (!on && settings) settings.hidden = true;
    if (on) for (const tile of $$('.b-widget', grid)) addAlignButtons(tile);
    buildEditorList(on);
  };
  toggle?.addEventListener('click', () => setEditing(!editing));
  // Opened from the Screens page editor (compose=1): edit straight away,
  // on this one layout; the modal's Done closes it (STATUS U8: "Edit
  // layout" no longer needs pressing twice).
  if (new URLSearchParams(location.search).get('compose') === '1') {
    body.dataset.compose = '1';
    try { sessionStorage.removeItem('b-editing'); } catch { /* private mode */ }
    setEditing(true);
  } else {
    // Re-enter editing after a structural reload (add/delete/reset/switch).
    try {
      if (sessionStorage.getItem('b-editing') === '1') {
        sessionStorage.removeItem('b-editing');
        setEditing(true);
      }
    } catch { /* private mode */ }
  }
  wireKeys();

  grid.addEventListener('pointerdown', (e) => {
    if (!editing) return;
    const tile = e.target.closest('.b-widget');
    if (!tile) return;
    const wid = tile.dataset.wid;
    if (e.target.closest('[data-resize]')) dragTile(tile, wid, e, 'resize');
    // UX2: the move handle is the chrome row minus its buttons — gear/delete
    // must stay clean click targets, not drag launches.
    else if (e.target.closest('.b-w-chrome') && !e.target.closest('button')) dragTile(tile, wid, e, 'move');
  });
  grid.addEventListener('click', async (e) => {
    if (!editing) return;
    const tile = e.target.closest('.b-widget');
    if (!tile) return;
    const al = e.target.closest('[data-align]');
    if (al) {
      const w = widgetOf(tile.dataset.wid);
      if (w) {
        w.x = al.dataset.align === 'left' ? 0 : 12 - w.w;
        clampTile(w);
        applyGeometry(w.id);
        scheduleSave();
      }
      return;
    }
    if (e.target.closest('[data-gear]')) openSettings(tile.dataset.wid);
    if (e.target.closest('[data-del]')) {
      const rest = (layout.widgets || []).filter((w) => w.id !== tile.dataset.wid);
      if (!rest.length) { saveState('A board needs at least one tile'); return; }
      layout.widgets = rest;
      await reloadEditing(); // server re-renders tiles, then we relock into edit
    }
  });

  $('#b-palette-list')?.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-add]');
    if (!btn) return;
    const type = btn.dataset.add;
    const def = TILE_DEFAULTS[type];
    if (!def) return;
    const seen = new Set((layout.widgets || []).map((w) => w.id));
    let id = type;
    for (let n = 2; seen.has(id); n++) id = `${type}-${n}`;
    const spot = freeSpot(Math.min(def.w, 12), def.h);
    if (!spot) {
      saveState('No room for that tile: make space or delete one first');
      return;
    }
    layout.widgets = [...(layout.widgets || []), {
      id, type, x: spot.x, y: spot.y, w: Math.min(def.w, 12), h: def.h,
      opts: { ...def.opts },
    }];
    await reloadEditing();
  });

  // Templates: Go's catalog, grouped by display type.
  const presetList = $('#b-preset-list');
  let catalog = [];
  if (presetList) {
    api('GET', '/api/board-templates').then((j) => {
      catalog = j.catalog || [];
      const groups = { audience: 'Audience', walkin: 'Walk-in', presenter: 'Presenter' };
      for (const [kind, label] of Object.entries(groups)) {
        const items = catalog.filter((t) => t.kind === kind);
        if (!items.length) continue;
        presetList.appendChild(Object.assign(document.createElement('span'), { className: 'b-preset-group', textContent: label }));
        for (const t of items) {
          const b = document.createElement('button');
          b.type = 'button';
          b.className = 'btn btn-sm';
          b.dataset.preset = t.key;
          b.title = t.desc;
          b.textContent = t.name;
          presetList.appendChild(b);
        }
      }
    }).catch(() => { /* offline: no templates */ });
  }
  presetList?.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-preset]');
    if (!btn) return;
    const t = catalog.find((x) => x.key === btn.dataset.preset);
    if (!t) return;
    if (!(await tpConfirm(`Replace this layout with "${t.name}"? Your current tiles are replaced.`, { ok: 'Replace', danger: true }))) return;
    layout = structuredClone(t.layout);
    await reloadEditing(); // server re-renders tiles, then we relock into edit
  });

  // Canvas: orientation + row count.
  const orient = $('#b-orient');
  const rowsIn = $('#b-rows');
  if (orient) orient.value = layout.orientation === 'portrait' ? 'portrait' : 'landscape';
  if (rowsIn) rowsIn.value = String(canvasRows());
  orient?.addEventListener('change', async () => {
    layout.orientation = orient.value;
    layout.rows = Math.max(canvasRows(), orient.value === 'portrait' ? 16 : 8);
    await reloadEditing();
  });
  const animSel = $('#b-anim');
  const animMs = $('#b-anim-ms');
  if (animSel) animSel.value = layout.anim || 'fade';
  if (animMs) animMs.value = String(layout.animMS || 400);
  animSel?.addEventListener('change', () => { layout.anim = animSel.value; scheduleSave(); });
  animMs?.addEventListener('change', () => {
    const v = Math.min(3000, Math.max(120, Number(animMs.value) || 400));
    animMs.value = String(v);
    layout.animMS = v;
    scheduleSave();
  });
  rowsIn?.addEventListener('change', () => {
    const ext = (layout.widgets || []).reduce((n, w) => Math.max(n, w.y + w.h), 1);
    const v = Math.max(ext, Math.min(48, Number(rowsIn.value) || 8));
    rowsIn.value = String(v);
    layout.rows = v;
    body.style.setProperty('--b-rows', String(v));
    scheduleSave();
  });

  $('#b-reset')?.addEventListener('click', async () => {
    if (!(await tpConfirm('Reset this board to the factory layout?', { ok: 'Reset', danger: true }))) return;
    // The factory layout comes from the server (boards.DefaultLayout), so
    // it never drifts from Go (STATUS C5).
    try {
      const j = await api('GET', '/api/board-templates');
      if (!j.default?.widgets?.length) throw new Error('no default layout');
      layout = j.default;
    } catch (err) {
      saveState(`Reset failed: ${err.message}`);
      return;
    }
    await reloadEditing();
  });

  $('#b-boards')?.addEventListener('change', (e) => {
    const id = e.target.value;
    const url = new URL(location.href);
    url.searchParams.set('board', id);
    location.href = url.toString();
  });

  $('#b-new')?.addEventListener('click', async () => {
    const res = await tpPrompt(null, 'Lobby', { title: 'New board name', ok: 'Create', fields: [{ id: 'name', label: 'Name', value: 'Lobby' }] });
    const name = res?.name;
    if (!name || !name.trim()) return;
    try {
      const created = await api('POST', `/api/shows/${encodeURIComponent(code)}/boards`, { name: name.trim() });
      const url = new URL(location.href);
      url.searchParams.set('board', String(created.id));
      location.href = url.toString();
    } catch (err) {
      saveState(`New board failed: ${err.message}`);
    }
  });
}

/* ---------------------------------------------------------------- boot -- */

// Editable boots (?edit=1) rewrite the join-card QR to the LOCKED board URL:
// Go bakes the ?edit=1 query into Join.QR/Self (routes/boards.go), so without
// this a phone scanning the TV inherits compose access. The card href is the
// control room (never editable); only the QR image + self line are re-pointed.
function lockJoinCard() {
  if (!editable || !code) return;
  const locked = new URL(location.href);
  locked.searchParams.delete('edit');
  const qr = $('#b-join-qr');
  if (qr) {
    qr.src = `/api/shows/${encodeURIComponent(code)}/qr?data=${encodeURIComponent(locked.toString())}&size=132`;
  }
  const card = $('.b-join-card');
  const selfLine = card ? $('.mono.text-truncate', card) : null;
  if (selfLine) setText(selfLine, locked.toString());
}

initMesh();
startWalkin();
lockJoinCard();
initClientLog();
wireCompose();
setInterval(tick, 100);
// Mesh link is event-driven (onStatusChange) plus a 1 s poll so a master
// handoff or a peer drop repaints the chip even between WS frames.
setInterval(updateLink, 1000);
renderStatic();
tick();
