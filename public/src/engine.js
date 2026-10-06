/**
 * TimerPi client engine — pure, dependency-free snapshot math + commands.
 *
 * The SERVER (Agent B, timerpi/engine.go) is authoritative; this module is the
 * browser-side mirror so a mesh MASTER peer can execute the exact same
 * commands while the server link is down (see mesh.js), and so every client
 * can render smooth clocks locally (never server-ticked digits).
 *
 * Works on the PROTOCOL.md snapshot shape:
 *   { updatedAt, serverTime, show:{id,title},
 *     runtime: { running, paused, activePos, prevPos, nextPos, anchorTS,
 *                pausedElapsedMS, rate, dayStartTS },
 *     cues: [ { pos, label, durationMS, kind, tags, speaker, holdMS,
 *               timerKind, alert1MS, alert2MS, alertColor1, alertColor2,
 *               endAction, autoContinue, notes, color } ],
 *     messages: [ { id, text, color, shownAt } ] }
 *
 * All times are epoch milliseconds (server clock domain — callers pass an
 * offset-corrected `now`).
 */

/* ------------------------------------------------------------------ cues -- */

export function cueByPos(snap, pos) {
  if (!snap || !pos) return null;
  return snap.cues.find(c => c.pos === pos) || null;
}

export function activeCue(snap) {
  return snap ? cueByPos(snap, snap.runtime.activePos) : null;
}

export function cueAfter(snap, pos) {
  const list = snap?.cues || [];
  for (const c of list) if (c.pos > (pos || 0)) return c;
  return null;
}

export function cueBefore(snap, pos) {
  const list = snap?.cues || [];
  for (let i = list.length - 1; i >= 0; i--) {
    if (list[i].pos < (pos || Infinity)) return list[i];
  }
  return null;
}

/* ----------------------------------------------------------------- clock -- */

/**
 * Scaled elapsed of the active cue at `now` (server-domain epoch ms).
 * Parity with timerpi.DisplayedRemaining (snapshot.go):
 *   scaledElapsed = running && !paused ? (now - anchorTS) * rate : 0
 *   elapsed       = scaledElapsed + pausedElapsedMS
 * (NOT gated to 0 when idle — a HOLD/BLANK cue frozen by the server tick
 * carries PausedElapsedMS == durationMS with running=false, and that IS the
 * held state; REVIEW-3 R2.)
 */
export function elapsedMS(snap, now) {
  if (!snap) return 0;
  const r = snap.runtime;
  const scaled = r.running && !r.paused && r.anchorTS > 0
    ? Math.max(0, (now - r.anchorTS) * (r.rate && r.rate > 0 ? r.rate : 1))
    : 0;
  return scaled + (r.pausedElapsedMS || 0);
}

/** Remaining (negative = overtime) of the active cue at `now`. */
export function remainingMS(snap, now) {
  const cue = activeCue(snap);
  if (!cue) return null;
  switch (cue.timerKind) {
    case 'CLOCK': return 0;        // wall-clock cue: server ships 0
    case 'COUNTSTOP': return elapsedMS(snap, now); // stopwatch: counts UP (snapshot.go parity)
    default: return cue.durationMS - elapsedMS(snap, now);
  }
}

/** True when the cue should display a count-up past zero (overtime STATE). */
export function isOvertime(snap, now) {
  const cue = activeCue(snap);
  if (!cue || !snap.runtime.running) return false;
  if (cue.timerKind === 'CLOCK' || cue.timerKind === 'COUNTSTOP') return false;
  return cue.endAction === 'OVERTIME' && remainingMS(snap, now) < 0;
}

/**
 * Alert band 0|1|2 for the active cue at `now` — parity with DisplayedRemaining:
 * thresholds saturate at the deepest configured state once remaining passes
 * them (or zero), computed EVEN when the cue is not running (a held cue at
 * zero keeps its deepest alert), and CLOCK/COUNTSTOP alert nothing.
 */
export function alertState(snap, now) {
  const cue = activeCue(snap);
  if (!cue || cue.timerKind === 'CLOCK' || cue.timerKind === 'COUNTSTOP') return 0;
  const rem = remainingMS(snap, now);
  if (rem == null) return 0;
  let st = 0;
  if (cue.alert1MS > 0 && rem <= cue.alert1MS) st = 1;
  if (cue.alert2MS > 0 && rem <= cue.alert2MS) st = 2;
  // Saturate at the deepest configured state once the timer is spent.
  if (rem <= 0) {
    if (cue.alert2MS > 0) st = 2;
    else if (cue.alert1MS > 0) st = 1;
  }
  return st;
}

/**
 * Clock view for renderers:
 *   { state, remaining, elapsed, alert }
 * state: idle | armed | running | overtime | paused | held | blank
 *        | alert1 | alert2
 * HELD: frozen at zero by the tick — server leaves running=false with
 * pausedElapsedMS == durationMS (Paused stays false); the local mirror may
 * set paused=true — BOTH shapes read as held here. BLANK: the server stamps
 * runtime.blank on a frozen BLANK cue → zeros, no countdown.
 */
export function clockView(snap, now) {
  if (!snap || !snap.runtime.activePos) return { state: 'idle', remaining: null, elapsed: 0, alert: 0 };
  const cue = activeCue(snap);
  const r = snap.runtime;
  if (!cue) return { state: 'idle', remaining: null, elapsed: 0, alert: 0 };
  // Frozen at zero (held/blank): server tick froze it, or the local master did.
  if (cue.timerKind === 'COUNTDOWN' && cue.durationMS > 0 && !r.running
      && (r.pausedElapsedMS || 0) >= cue.durationMS) {
    if (cue.endAction === 'BLANK' || r.blank) {
      return { state: 'blank', remaining: 0, elapsed: cue.durationMS, alert: 0 };
    }
    return { state: 'held', remaining: 0, elapsed: cue.durationMS, alert: 0 };
  }
  if (r.blank) { // defensive: server-stamped blank outside the frozen shape
    return { state: 'blank', remaining: 0, elapsed: elapsedMS(snap, now), alert: 0 };
  }
  if (r.paused) return { state: 'paused', remaining: remainingMS(snap, now), elapsed: elapsedMS(snap, now), alert: alertState(snap, now) };
  if (r.running) {
    const rem = remainingMS(snap, now);
    if (rem < 0 && isOvertime(snap, now)) return { state: 'overtime', remaining: rem, elapsed: elapsedMS(snap, now), alert: alertState(snap, now) };
    const a = alertState(snap, now);
    return { state: a === 2 ? 'alert2' : a === 1 ? 'alert1' : 'running', remaining: rem, elapsed: elapsedMS(snap, now), alert: a };
  }
  return { state: 'armed', remaining: cue.durationMS, elapsed: 0, alert: 0 };
}

/* -------------------------------------------------------------- commands -- */

function findNextArmed(snap) {
  const r = snap.runtime;
  if (r.activePos) {
    const cue = activeCue(snap);
    if (cue && !(r.running || r.paused)) return cue; // armed current
    const after = cueAfter(snap, r.activePos);
    if (after) return after;
    return cue;
  }
  return snap.cues[0] || null;
}

function beginCue(snap, cue, now) {
  const r = snap.runtime;
  r.prevPos = r.activePos;
  r.activePos = cue.pos;
  r.nextPos = cueAfter(snap, cue.pos)?.pos || 0;
  r.anchorTS = now;
  r.pausedElapsedMS = 0;
  r.running = true;
  r.paused = false;
  r.blank = false;
  if (!r.dayStartTS) r.dayStartTS = now;
}

/**
 * Apply a control command to the snapshot in place (master-executes the same
 * rules as the Go engine). Returns 'applied' | 'ignored'.
 * Actions: start | pause | resume | reset | next | prev | go | jump | rate
 *          addMsg | showMsg | hideMsg | clearMsgs
 *          cueAdd | cueEdit | cueDel | cueMove | cueDup (offline master only —
 *          the server serves these over WS when online; semantics parity per
 *          OFFLINE-EDIT.md, stamps + tombstones for the sync merge)
 */
export function applyCommand(snap, action, args = {}, now = Date.now()) {
  if (!snap) return 'ignored';
  const r = snap.runtime;
  const cue = activeCue(snap);

  switch (action) {
    case 'start': {
      const target = args.pos ? cueByPos(snap, args.pos) : (cue || findNextArmed(snap));
      if (!target) return 'ignored';
      beginCue(snap, target, now);
      return 'applied';
    }
    case 'go': {
      const target = args.pos ? cueByPos(snap, args.pos) : findNextArmed(snap);
      if (!target) return 'ignored';
      beginCue(snap, target, now);
      return 'applied';
    }
    case 'pause': {
      if (!cue) return 'ignored';
      if (r.paused) { // toggle: resume
        // Never resume a cue frozen at zero (held): it must GO forward, not
        // drain its stale remainder (parity with the hub's held-pause rule).
        if ((r.pausedElapsedMS || 0) >= (cue.durationMS || 0) && cue.durationMS > 0) return 'ignored';
        r.anchorTS = now;
        r.running = true;
        r.paused = false;
        return 'applied';
      }
      if (!r.running) return 'ignored';
      r.pausedElapsedMS = (now - r.anchorTS) * (r.rate || 1) + (r.pausedElapsedMS || 0);
      r.running = false;
      r.paused = true;
      return 'applied';
    }
    case 'resume': {
      if (!r.paused || !cue) return 'ignored';
      if ((r.pausedElapsedMS || 0) >= (cue.durationMS || 0) && cue.durationMS > 0) return 'ignored'; // held
      r.anchorTS = now;
      r.running = true;
      r.paused = false;
      return 'applied';
    }
    case 'reset': {
      if (!cue) return 'ignored';
      r.running = false;
      r.paused = false;
      r.pausedElapsedMS = 0;
      r.blank = false;
      return 'applied';
    }
    case 'next': {
      const target = cueAfter(snap, r.activePos) || cue;
      if (!target) return 'ignored';
      beginCue(snap, target, now);
      return 'applied';
    }
    case 'prev': {
      const target = cueBefore(snap, r.activePos);
      if (!target) return 'ignored';
      beginCue(snap, target, now);
      return 'applied';
    }
    case 'jump': {
      const target = cueByPos(snap, args.pos);
      if (!target) return 'ignored';
      if (args.start === false) { // arm without starting
        r.prevPos = r.activePos; r.activePos = target.pos;
        r.nextPos = cueAfter(snap, target.pos)?.pos || 0;
        r.running = false; r.paused = false; r.pausedElapsedMS = 0; r.blank = false;
      } else {
        beginCue(snap, target, now);
      }
      return 'applied';
    }
    case 'rate': {
      const rate = Number(args.rate);
      if (!Number.isFinite(rate) || rate <= 0) return 'ignored';
      if (r.running && cue) {
        // Re-anchor: absorb scaled elapsed since the last anchor; schedule untouched.
        r.pausedElapsedMS = (now - r.anchorTS) * r.rate + (r.pausedElapsedMS || 0);
        r.anchorTS = now;
      }
      r.rate = rate;
      return 'applied';
    }
    case 'addMsg': {
      const text = String(args.text || '').slice(0, 300);
      if (!text) return 'ignored';
      snap.messages.push({
        id: -(Date.now() % 1e9), text, color: args.color || '',
        shownAt: args.show ? now : 0, // data-show / args.show adds it live immediately
      });
      snap.updatedAt = bump(snap, now);
      return 'applied';
    }
    case 'showMsg': {
      const m = snap.messages.find(m => m.id === args.id) || snap.messages[snap.messages.length - 1];
      if (!m) return 'ignored';
      m.shownAt = now;
      snap.updatedAt = bump(snap, now);
      return 'applied';
    }
    case 'hideMsg': {
      const m = snap.messages.find(m => m.id === args.id) || snap.messages[snap.messages.length - 1];
      if (!m) return 'ignored';
      m.shownAt = 0;
      snap.updatedAt = bump(snap, now);
      return 'applied';
    }
    case 'clearMsgs': {
      snap.messages = [];
      snap.updatedAt = bump(snap, now);
      return 'applied';
    }
    case 'cueAdd':
      return cueAdd(snap, args, now);
    case 'cueEdit':
      return cueEdit(snap, args, now);
    case 'cueDel':
      return cueDel(snap, args, now);
    case 'cueMove':
      return cueMove(snap, args, now);
    case 'cueDup':
      return cueDup(snap, args, now);
    case 'blank':
      if (!snap.show) return 'ignored';
      snap.show.blanked = true;
      return 'applied';
    case 'unblank':
      if (!snap.show) return 'ignored';
      snap.show.blanked = false;
      return 'applied';
    case 'settings': {
      // Subset mirror of ws/commands.go settings (client-side working
      // copy only — persistence joins back at reconnect via the sync push).
      let touched = false;
      if (args.ts != null && Number(args.ts) > 0) { r.dayStartTS = Number(args.ts); touched = true; }
      if (typeof args.dayStart === 'string' && snap.show) { snap.show.dayStart = args.dayStart; touched = true; }
      if (typeof args.title === 'string' && args.title.trim() && snap.show) { snap.show.title = args.title.trim(); touched = true; }
      return touched ? 'applied' : 'ignored';
    }
    default:
      return 'ignored';
  }
}

/* -------------------------------- offline cue CRUD (OFFLINE-EDIT.md) ------ */

/**
 * Offline cue ops for the mesh MASTER (mesh.js executes them while the
 * server link is down; the server runs the equivalent WS `cue.*` commands
 * in ws/commands.go + timerpi/db.go when online).
 *
 * Stamp rule: ONLY content-touched rows get a fresh updatedAt (added /
 * edited / swapped / duplicated). Pure positional shifts from an insert or
 * delete keep their old stamp, so against an idle server the merge tie
 * (incoming-wins) preserves the offline order, while a concurrent remote
 * CONTENT edit to a shifted row still wins that row. Deletes leave a
 * tombstone {id, deleted:true, updatedAt} on snap.tombstones (cap 200).
 * Stamps use the caller's server-anchored `now` (mesh passes this.now()).
 */

const MAX_TOMBSTONES = 200;

function stampCue(cue, now) {
  cue.updatedAt = Math.max((cue.updatedAt || 0) + 1, now);
  return cue.updatedAt;
}

function renumber(snap) {
  // Assign dense 1..N in CURRENT array order (splices above already place
  // rows; sorting by the stale pos values would undo the operation).
  snap.cues.forEach((c, i) => { c.pos = i + 1; });
}

function nextTempId(snap) {
  // Negative IDs can never collide with server rows; the per-snap sequence
  // keeps rapid offline adds distinct (mirrors the negative-temp convention
  // of addMsg above).
  snap._tempSeq = (snap._tempSeq || 0) + 1;
  let id = -((Date.now() % 1e9) * 1000 + (snap._tempSeq % 1000)) - 1;
  const seen = new Set(snap.cues.map(c => c.id));
  while (seen.has(id)) id -= 1;
  return id;
}

function pushTombstone(snap, id, now) {
  if (!(id > 0)) return; // temp rows the server never saw need no tombstone
  if (!Array.isArray(snap.tombstones)) snap.tombstones = [];
  const ts = Math.max(now, ...snap.tombstones.map(t => t.updatedAt || 0), 0);
  snap.tombstones.push({ id, deleted: true, updatedAt: ts });
  if (snap.tombstones.length > MAX_TOMBSTONES) {
    snap.tombstones.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0));
    snap.tombstones.length = MAX_TOMBSTONES;
  }
}

const COLOR_RE = /^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/;
const CUE_KINDS = new Set(['session', 'break']);
const TIMER_KINDS = new Set(['COUNTDOWN', 'COUNTSTOP', 'CLOCK']);
const END_ACTIONS = new Set(['HOLD', 'OVERTIME', 'BLANK']);

/** cueAdd {label, durationMS?, kind?, pos?} — parity with ws `cueAdd` (server only reads label/duration/kind; pos insert mirrors DB CreateCue). */
export function cueAdd(snap, args = {}, now = Date.now()) {
  const label = String(args.label ?? '').trim();
  if (!label) return 'ignored';
  const durationMS = Number(args.durationMS) > 0 ? Math.floor(Number(args.durationMS)) : 0;
  const cue = {
    id: nextTempId(snap),
    pos: 0, // set below
    label,
    durationMS,
    kind: args.kind === 'break' ? 'break' : 'session',
    tags: '', speaker: '', holdMS: 0,
    timerKind: 'COUNTDOWN', alert1MS: 0, alert2MS: 0,
    alertColor1: '#ffaa00', alertColor2: '#ff4444',
    endAction: 'HOLD', autoContinue: false, notes: '', color: '',
  };
  stampCue(cue, now);
  const at = Number(args.pos) > 0 ? Math.min(Math.max(1, Math.floor(Number(args.pos))), snap.cues.length + 1) : snap.cues.length + 1;
  cue.pos = at; // position BEFORE the splice so renumber keeps the placement
  snap.cues.splice(at - 1, 0, cue);
  renumber(snap);
  snap.updatedAt = bump(snap, now);
  return 'applied';
}

/** cueEdit {pos, ...fields} — partial update parity with ws `cueEdit` allowlist. */
export function cueEdit(snap, args = {}, now = Date.now()) {
  const cue = cueByPos(snap, Number(args.pos));
  if (!cue) return 'ignored';
  // Whole-op rejections mirror the server Validate() failure (nothing applied).
  if (args.timerKind !== undefined && !TIMER_KINDS.has(args.timerKind)) return 'ignored';
  if (args.endAction !== undefined && !END_ACTIONS.has(args.endAction)) return 'ignored';
  if (args.label !== undefined) cue.label = String(args.label);
  if (args.durationMS !== undefined && Number.isFinite(Number(args.durationMS)) && Number(args.durationMS) >= 0) {
    cue.durationMS = Math.floor(Number(args.durationMS));
  }
  if (args.kind !== undefined && CUE_KINDS.has(args.kind)) cue.kind = args.kind;
  if (args.tags !== undefined) cue.tags = String(args.tags);
  if (args.speaker !== undefined) cue.speaker = String(args.speaker);
  if (args.holdMS !== undefined && Number.isFinite(Number(args.holdMS)) && Number(args.holdMS) >= 0) {
    cue.holdMS = Math.floor(Number(args.holdMS));
  }
  if (args.timerKind !== undefined) cue.timerKind = args.timerKind;
  if (args.alert1MS !== undefined && Number.isFinite(Number(args.alert1MS)) && Number(args.alert1MS) >= 0) {
    cue.alert1MS = Math.floor(Number(args.alert1MS));
  }
  if (args.alert2MS !== undefined && Number.isFinite(Number(args.alert2MS)) && Number(args.alert2MS) >= 0) {
    cue.alert2MS = Math.floor(Number(args.alert2MS));
  }
  if (args.alertColor1 !== undefined && (args.alertColor1 === '' || COLOR_RE.test(args.alertColor1))) cue.alertColor1 = args.alertColor1;
  if (args.alertColor2 !== undefined && (args.alertColor2 === '' || COLOR_RE.test(args.alertColor2))) cue.alertColor2 = args.alertColor2;
  if (args.endAction !== undefined) cue.endAction = args.endAction;
  if (args.autoContinue !== undefined) cue.autoContinue = args.autoContinue === true || args.autoContinue === 'true' || args.autoContinue === 1;
  if (args.notes !== undefined) cue.notes = String(args.notes);
  if (args.color !== undefined && (args.color === '' || COLOR_RE.test(args.color))) cue.color = args.color;
  stampCue(cue, now);
  snap.updatedAt = bump(snap, now);
  return 'applied';
}

/** cueDel {pos} — parity with ws `cueDel`; leaves a tombstone for the merge. */
export function cueDel(snap, args = {}, now = Date.now()) {
  const pos = Number(args.pos);
  const idx = snap.cues.findIndex(c => c.pos === pos);
  if (!(pos > 0) || idx < 0) return 'ignored';
  const [gone] = snap.cues.splice(idx, 1);
  pushTombstone(snap, gone.id, now);
  renumber(snap);
  snap.updatedAt = bump(snap, now);
  return 'applied';
}

/** cueMove {pos, dir: up|down} — parity with ws `cueMove` (adjacent swap). Both swapped rows are content-touched (see stamp rule above). */
export function cueMove(snap, args = {}, now = Date.now()) {
  const pos = Number(args.pos);
  const dir = args.dir;
  if (!(pos > 0) || (dir !== 'up' && dir !== 'down')) return 'ignored';
  renumber(snap);
  const idx = snap.cues.findIndex(c => c.pos === pos);
  const other = dir === 'up' ? idx - 1 : idx + 1;
  if (idx < 0 || other < 0 || other >= snap.cues.length) return 'ignored'; // edge: no-op like the DB layer
  const neighbour = snap.cues[other];
  const [moved] = snap.cues.splice(idx, 1);
  snap.cues.splice(other, 0, moved);
  renumber(snap);
  stampCue(moved, now);
  stampCue(neighbour, now);
  snap.updatedAt = bump(snap, now);
  return 'applied';
}

/** cueDup {pos} — parity with ws `cueDup` (copy inserted directly after). */
export function cueDup(snap, args = {}, now = Date.now()) {
  const pos = Number(args.pos);
  const idx = snap.cues.findIndex(c => c.pos === pos);
  if (!(pos > 0) || idx < 0) return 'ignored';
  const src = snap.cues[idx];
  const copy = { ...src, id: nextTempId(snap) };
  stampCue(copy, now);
  snap.cues.splice(idx + 1, 0, copy);
  renumber(snap);
  snap.updatedAt = bump(snap, now);
  return 'applied';
}

function bump(snap, now) {
  // Keep updatedAt on the server clock domain so dominance compares hold.
  return Math.max((snap.updatedAt || 0) + 1, now);
}

/**
 * Zero-crossing housekeeping for the offline master (call ~4x/sec).
 * Parity with timerpi/engine.go Tick: only RUNNING, UNPAUSED COUNTDOWN cues
 * cross. On crossing: OVERTIME keeps counting up (no change); HOLD and BLANK
 * both freeze at PausedElapsedMS == durationMS with running=false (HOLD then
 * renders 00:00 HELD, BLANK renders blank); an auto-continuing HOLD cue
 * starts the next cue instead.
 * Returns true when the snapshot changed (caller must broadcast + sync).
 */
export function tickZeroCrossing(snap, now = Date.now()) {
  if (!snap || !snap.runtime.running || snap.runtime.paused) return false;
  const cue = activeCue(snap);
  if (!cue || cue.timerKind !== 'COUNTDOWN') return false;
  const rem = remainingMS(snap, now);
  if (rem > 0) return false;

  if (cue.endAction === 'OVERTIME') return false;
  if (cue.autoContinue && cue.endAction === 'HOLD') {
    const next = cueAfter(snap, cue.pos);
    if (next) { beginCue(snap, next, now); snap.updatedAt = bump(snap, now); return true; }
  }
  if (cue.endAction !== 'HOLD' && cue.endAction !== 'BLANK') return false; // unknown action: mirror go (no freeze)
  // HOLD (no next / no autoContinue) and BLANK: freeze the countdown at zero
  // — running=false + elapsed == duration reads back as remaining 0
  // everywhere (engine.go EndHold/EndBlank branch; Paused stays false).
  snap.runtime.running = false;
  snap.runtime.pausedElapsedMS = cue.durationMS;
  snap.updatedAt = bump(snap, now);
  return true;
}

/* -------------------------------------------------------------- schedule -- */

/**
 * Pure schedule computation (client mirror of timerpi.ComputeSchedule):
 * cumulative start/end per cue including HoldMS; times relative to dayStartTS.
 * Returns { rows: [{pos, startMS, endMS, holdMS, isBreak}], totalMS }.
 */
export function computeSchedule(snap) {
  const rows = [];
  let cum = 0;
  for (const c of snap?.cues || []) {
    // Hold after is gone (STATUS U42): a stored holdMS moves nothing.
    const isBreak = c.kind === 'break';
    rows.push({ pos: c.pos, startMS: cum, endMS: cum + c.durationMS, holdMS: 0, isBreak });
    cum += c.durationMS;
  }
  return { rows, totalMS: cum };
}

/* -------------------------------------------------------------- formatting -- */

/** Format epoch-ms-relative duration: h:mm:ss / m:ss (+ tenths below 10 s). */
export function fmtRemaining(ms) {
  const neg = ms < 0;
  const abs = Math.abs(ms);
  // One tenths-space integer drives BOTH digits (the old split — seconds
  // from a ceil, the tenth from a floor — rendered 0:10 → 0:10.9 across
  // the 10 s boundary: the clock visibly counting up on the most-watched
  // part of the stage display).
  const tenths = Math.ceil(abs / 100);
  const whole = Math.floor(tenths / 10);
  const h = Math.floor(whole / 3600);
  const m = Math.floor((whole % 3600) / 60);
  const s = whole % 60;
  let text;
  if (h > 0) text = `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  else text = `${m}:${String(s).padStart(2, '0')}`;
  if (abs < 10000) text += `.${tenths % 10}`;
  return (neg ? '+' : '') + text;
}

/** Format a duration field (planned): m:ss, no tenths. */
export function fmtDuration(ms) {
  const total = Math.round(Math.abs(ms) / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  return `${m}:${String(s).padStart(2, '0')}`;
}

/** Wall-clock of a server-domain timestamp: HH:MM:SS. */
export function fmtTimeOfDay(ts) {
  const d = new Date(ts);
  return [d.getHours(), d.getMinutes(), d.getSeconds()].map(n => String(n).padStart(2, '0')).join(':');
}

/** K7QP-M3XB — 4-4 rendered share code (Agent L: alphanumeric Crockford
 *  base32, uppercase; parity with server timerpi/gen.go FmtCode). A short
 *  numeric legacy value keeps the old 0000-XXXX padding, only so pre-code
 *  snapshots can't render raw digits weirdly. */
export function fmtCode(code) {
  const s = String(code || '').toUpperCase().replace(/[^0-9A-Z]/g, '');
  if (s.length !== 8) {
    const legacy = s.replace(/\D/g, '').slice(0, 8).padStart(8, '0');
    return legacy.slice(0, 4) + '-' + legacy.slice(4);
  }
  return s.slice(0, 4) + '-' + s.slice(4);
}
