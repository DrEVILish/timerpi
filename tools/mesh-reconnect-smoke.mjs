#!/usr/bin/env node
/**
 * mesh-reconnect-smoke.mjs — regression smoke for the reconnect fixes
 * (owner bug report 2026-10-05: browser displays/controls did not
 * auto-reconnect after the network returned, and peer channels stayed
 * dead after an outage). Drives the real Mesh class with stubbed browser
 * globals; the Go suite can't cover client JS, this can.
 *
 *   node tools/mesh-reconnect-smoke.mjs   (exits non-zero on failure)
 */
import { Mesh, screenName } from '../public/src/mesh.js';
import { applyCommand, fmtRemaining } from '../public/src/engine.js';

let failures = 0;
const check = (name, cond) => {
  if (!cond) { failures++; console.error(`FAIL: ${name}`); }
  else console.log(`ok: ${name}`);
};

/* --- fake timers ---------------------------------------------------------- */
let timerSeq = 0;
const timeouts = new Map(); // id → {ms, fn, cleared}
globalThis.setTimeout = (fn, ms = 0) => {
  const id = ++timerSeq;
  timeouts.set(id, { ms, fn, cleared: false });
  return id;
};
globalThis.clearTimeout = (id) => {
  const t = timeouts.get(id);
  if (t) t.cleared = true;
};
const intervals = new Map();
globalThis.setInterval = (fn, ms) => { const id = ++timerSeq; intervals.set(id, { ms, fn }); return id; };
globalThis.clearInterval = (id) => { intervals.delete(id); };
const fireTimeout = (id) => {
  const t = timeouts.get(id);
  if (!t || t.cleared) return false;
  timeouts.delete(id);
  t.fn();
  return true;
};
const liveTimeouts = (ms) => [...timeouts.values()].filter((t) => !t.cleared && (ms === undefined || t.ms === ms));

/* --- fake browser surface ------------------------------------------------- */
const sockets = [];
globalThis.WebSocket = class {
  static CONNECTING = 0; static OPEN = 1; static CLOSED = 3;
  constructor(url) { this.url = url; this.readyState = 0; this.sent = []; this.closedByMe = false; sockets.push(this); }
  send(d) { this.sent.push(d); }
  close() { this.closedByMe = true; this.readyState = 3; }
};
globalThis.location = { protocol: 'http:', host: 'test.local', href: 'http://test.local/c/ABCD1234' };
globalThis.history = { state: null, replaceState() { throw new Error('replaceState must not fire without ?screen='); } };
const ls = new Map();
globalThis.localStorage = {
  getItem: (k) => (ls.has(k) ? ls.get(k) : null),
  setItem: (k, v) => ls.set(k, String(v)),
  removeItem: (k) => ls.delete(k),
};
const ss = new Map(); // sessionStorage = window-scoped identity
globalThis.sessionStorage = {
  getItem: (k) => (ss.has(k) ? ss.get(k) : null),
  setItem: (k, v) => ss.set(k, String(v)),
  removeItem: (k) => ss.delete(k),
};
const winListeners = {};
globalThis.window = {
  addEventListener: (k, fn) => { (winListeners[k] ||= []).push(fn); },
  removeEventListener: (k, fn) => { winListeners[k] = (winListeners[k] || []).filter((f) => f !== fn); },
};
const docListeners = {};
globalThis.document = {
  visibilityState: 'visible',
  addEventListener: (k, fn) => { (docListeners[k] ||= []).push(fn); },
  removeEventListener: (k, fn) => { docListeners[k] = (docListeners[k] || []).filter((f) => f !== fn); },
};

/* --- drive it ------------------------------------------------------------- */
const mesh = new Mesh({ showId: 'ABCD1234', role: 'controls' });
mesh.start();

check('start creates a socket', sockets.length === 1);
check('online listener registered', (winListeners.online || []).length === 1);
check('visibility listener registered', (docListeners.visibilitychange || []).length === 1);
const ws0 = sockets[0];
const handshake = liveTimeouts(6000)[0];
check('handshake watchdog armed (6 s)', !!handshake);
check('handshake bound to this socket', ws0.readyState === 0);

// Watchdog: network dead — socket stuck CONNECTING; the timer must kill it
// and keep the retry loop alive (the old code stalled here forever).
fireTimeout([...timeouts.entries()].find(([, t]) => t === handshake)[0]);
check('stuck handshake force-closed', ws0.closedByMe === true);
check('status went offline', mesh.wsStatus === 'offline');
check('socket detached', mesh._ws === null);
const rt = liveTimeouts(1200); // 800 * 1.5
check('retry scheduled after backoff', rt.length === 1);

// The browser's async close event for the same socket must NOT re-penalize.
ws0.onclose?.();
check('double fault does not re-penalize',
  liveTimeouts(1200).length === 1 && mesh._wsBackoff === 1200);

// Network returns → 'online' listener reconnects immediately, backoff reset.
winListeners.online[0]();
check('online → immediate new socket', sockets.length === 2 && liveTimeouts(1200).every((t) => t.cleared));
check('backoff reset to base', mesh._wsBackoff === 800);

// Re-join after the outage: half-dead peer channels (never opened) are
// pruned so re-negotiation can happen; healthy channels survive.
const ws1 = sockets[1];
let deadClosed = false;
mesh.connections.set('dead', { pc: { close: () => { deadClosed = true; } }, dc: { readyState: 'connecting' }, peerId: 'dead' });
mesh.connections.set('live', { pc: { close() {} }, dc: { readyState: 'open', send() {} }, peerId: 'live' });
ws1.onmessage({
  data: JSON.stringify({
    t: 'joined',
    you: { peerId: 'p1', role: 'controls', joinedAt: 5 },
    snapshot: {
      updatedAt: 10, serverTime: 10, show: { code: 'ABCD1234', id: 1, title: 't' },
      runtime: { activePos: 0, rate: 1 }, cues: [], messages: [],
    },
    peers: [],
  }),
});
check('joined frame adopted', !!mesh.snap && mesh.joinedAt === 5);
check('stale peer channel pruned', !mesh.connections.has('dead') && deadClosed);
check('open peer channel kept', mesh.connections.has('live'));
check('single-tab election crowns self master', mesh.isMaster());

mesh.stop();
check('stop closed the live socket', ws1.closedByMe === true);
check('stop detached listeners', (winListeners.online || []).length === 0);

/* --- screen identity (F1) ------------------------------------------------ */
{
  const before = sockets.length;
  globalThis.location.href = 'http://test.local/d/ABCD1234?screen=Stage+Left';
  check('screenName honors ?screen=', screenName() === 'Stage Left');
  check('screenName persists to sessionStorage', ss.get('tp.screen') === 'Stage Left');
  globalThis.location.href = 'http://test.local/d/ABCD1234';
  check('storage is the fallback identity', screenName() === 'Stage Left');
  // Per-window identity (owner bug report 2026-10-05): a FRESH window
  // (empty sessionStorage) must get its OWN name — three display windows on
  // one browser are three screens, never grouped under one shared name.
  ss.delete('tp.screen');
  const gen = screenName();
  check('generated name is stable per window', /^Screen-[0-9A-Z]{4}$/.test(gen) && screenName() === gen);
  ss.delete('tp.screen');
  check('second window gets a separate name', screenName() !== gen);
  ss.delete('tp.screen');

  const disp = new Mesh({ showId: 'ABCD1234', role: 'display', screen: screenName() });
  disp.start();
  const dws = sockets[sockets.length - 1];
  check('display mesh opens its own socket', sockets.length === before + 1);
  dws.readyState = 1;
  dws.onopen(); // join frame goes out on open
  const join = JSON.parse(dws.sent[0]);
  check('join frame carries the screen name', typeof join.screen === 'string' && join.screen.startsWith('Screen-'));

  // Operator rename: adopt + persist + rejoin (fresh socket under the new name).
  dws.onmessage({ data: JSON.stringify({ t: 'screen-rename', name: 'Stage Right' }) });
  check('rename adopts new identity', disp.screen === 'Stage Right' && ss.get('tp.screen') === 'Stage Right');
  check('rename closes the old socket', dws.closedByMe === true);
  const nws = sockets[sockets.length - 1];
  check('rename rejoin carries new name', sockets.length === before + 2 && nws !== dws);
  nws.readyState = 1;
  nws.onopen();
  check('rejoin join frame uses new name', JSON.parse(nws.sent[0]).screen === 'Stage Right');
  disp.stop();
}

/* --- heartbeat + push accounting (owner bug report batch) ---------------- */
{
  const hm = new Mesh({ showId: 'ABCD1234', role: 'controls' });
  hm.start();
  const hws = sockets[sockets.length - 1];
  hws.readyState = 1; // serverOnline()
  hm.snap = { updatedAt: 5, show: { code: 'ABCD1234' }, runtime: {}, cues: [], messages: [] };
  hm.masterId = hm.peerId;
  let meshSends = 0;
  hm.connections.set('p1', { pc: { close() {} }, dc: { readyState: 'open', send() { meshSends++; } } });
  hm._heartbeat();
  check('online heartbeat stops flooding mesh-state', meshSends === 0);

  let pushes = 0;
  let refuse = true;
  globalThis.fetch = async () => {
    pushes++;
    return refuse
      ? { ok: false, status: 500, json: async () => ({ ok: false, error: 'boom' }) }
      : { ok: true, status: 200, json: async () => ({ ok: true }) };
  };
  hm._advancedOffline = true;
  await hm._postReconnectSync();
  check('refused push KEEPS the dirty flag (retry later)', hm._advancedOffline === true && pushes === 1);
  hm._heartbeat(); // online path re-drives the pending push from the heartbeat
  refuse = false;
  await hm._postReconnectSync();
  check('accepted push clears the dirty flag', hm._advancedOffline === false);
  hm.stop();

  check('offline heartbeat still broadcasts', (() => {
    const om = new Mesh({ showId: 'ABCD1234', role: 'controls' });
    om.snap = { updatedAt: 1, show: { code: 'ABCD1234' }, runtime: {}, cues: [], messages: [] };
    om.masterId = om.peerId;
    let got = 0;
    om.connections.set('p', { pc: { close() {} }, dc: { readyState: 'open', send() { got++; } } });
    om._heartbeat();
    return got === 1;
  })());
}

/* --- engine.js mirror + formatter --------------------------------------- */
{
  const snap = { show: { code: 'X', title: 'T' }, runtime: { activePos: 0, running: false, paused: false }, cues: [], messages: [] };
  check('offline mirror: blank', applyCommand(snap, 'blank', {}, Date.now()) === 'applied' && snap.show.blanked === true);
  check('offline mirror: unblank', applyCommand(snap, 'unblank', {}, Date.now()) === 'applied' && snap.show.blanked === false);
  check('offline mirror: settings ts', applyCommand(snap, 'settings', { ts: 123 }, Date.now()) === 'applied' && snap.runtime.dayStartTS === 123);
  check('offline mirror: settings empty ignored', applyCommand(snap, 'settings', {}, Date.now()) === 'ignored');

  check('fmtRemaining no upward jump at the tenths gate',
    fmtRemaining(10000) === '0:10' && fmtRemaining(9999) === '0:10.0' && fmtRemaining(9900) === '0:09.9'
    && fmtRemaining(9050) === '0:09.1' && fmtRemaining(1000) === '0:01.0');
  check('fmtRemaining hour form', fmtRemaining(3600000) === '1:00:00');
}

process.exit(failures ? 1 : 0);
