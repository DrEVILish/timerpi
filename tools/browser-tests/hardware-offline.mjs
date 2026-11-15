#!/usr/bin/env node
// External S5 qualification. Creates/deletes only its own disposable event or room.
// Usage: node hardware-offline.mjs <base-url> [result.json] [private-session-state.json]
import { chromium, request } from 'playwright';
import { randomBytes } from 'node:crypto';
import { writeFileSync, readFileSync } from 'node:fs';

const base = process.argv[2]?.replace(/\/$/, '');
if (!base || !/^https?:\/\//.test(base)) throw new Error('usage: node hardware-offline.mjs <base-url> [result.json] [private-session-state.json]');
const output = process.argv[3];
const result = { base, startedAt: new Date().toISOString(), limits: 'Two Chromium processes on CT108; real RTC; browser-server network partition, not a Pi power failure.', checks: [] };
const clients = [];
let api, event, room;
let ownsEvent = true;
function check(name, value, details = {}) {
  result.checks.push({ name, passed: !!value, ...details });
  console.log(`${value ? 'PASS' : 'FAIL'} ${name}`, JSON.stringify(details));
  if (!value) throw new Error(name);
}
async function endpoint(method, path, data) {
  const response = await api.fetch(base + path, { method, data });
  if (!response.ok()) throw new Error(`${method} endpoint returned ${response.status()}`);
  return response.json();
}
function spy() {
  window.__rtcMessages = 0;
  const descriptor = Object.getOwnPropertyDescriptor(RTCDataChannel.prototype, 'onmessage');
  Object.defineProperty(RTCDataChannel.prototype, 'onmessage', {
    set(fn) { this.addEventListener('message', () => window.__rtcMessages++); descriptor.set.call(this, fn); },
    get() { return descriptor.get.call(this); },
  });
}
async function client(storageState) {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium' });
  const context = await browser.newContext({ storageState, viewport: { width: 1280, height: 900 } });
  const state = { down: false, sockets: new Set(), blocked: 0 };
  await context.addInitScript(spy);
  await context.route(base + '/**', async (route) => {
    if (state.down) { state.blocked++; await route.abort('internetdisconnected'); }
    else await route.continue();
  });
  await context.routeWebSocket(/\/ws$/, (socket) => {
    if (state.down) { state.blocked++; socket.close(); return; }
    const server = socket.connectToServer();
    state.sockets.add({ socket, server });
    socket.onMessage((message) => { if (!state.down) server.send(message); });
    server.onMessage((message) => { if (!state.down) socket.send(message); });
  });
  const page = await context.newPage();
  const entry = { browser, context, state, page };
  clients.push(entry);
  return entry;
}
async function state(page) {
  return page.evaluate(() => {
    const mesh = window.__tpmesh;
    return { online: mesh.serverOnline(), role: mesh.role, isMaster: mesh.isMaster(), peerId: mesh.peerId, masterId: mesh.masterId,
      peers: mesh.openPeerIds().length, rtcMessages: window.__rtcMessages,
      channels: [...mesh.connections.values()].map(({ pc, dc }) => ({ connection: pc.connectionState, channel: dc?.readyState })),
      labels: mesh.snap.cues.map(c => c.label), running: mesh.snap.runtime.running,
      messages: mesh.snap.messages.map(m => m.text), anchor: mesh.snap.runtime.anchorTS,
      clock: document.querySelector('#d-clock')?.textContent,
      visibleMessage: document.querySelector('#d-message:not([hidden])')?.innerText.trim(),
      elapsed: mesh.snap.runtime.pausedElapsedMS + (mesh.snap.runtime.running && !mesh.snap.runtime.paused ? mesh.now() - mesh.snap.runtime.anchorTS : 0) };
  });
}
try {
  api = await request.newContext({ extraHTTPHeaders: { Origin: base } });
  if (process.argv[4]) {
    const credentials = JSON.parse(readFileSync(process.argv[4], 'utf8'));
    event = credentials[new URL(base).hostname.split('.').at(-1)].event;
    ownsEvent = false;
    await endpoint('POST', `/api/events/${event}/login`, { pw: credentials.password });
    room = (await endpoint('POST', `/api/events/${event}/rooms`, { name: 'Disposable RTC hardware qualification' })).code;
  } else {
    const created = await endpoint('POST', '/api/events', { name: 'Disposable RTC hardware qualification', password: randomBytes(24).toString('hex'), rooms: ['RTC bench'] });
    event = created.code;
    const lobby = await endpoint('GET', '/api/events/' + event);
    room = lobby.event.rooms[0].code;
  }
  console.log('PASS isolated disposable RTC room created');
  await endpoint('PUT', `/api/shows/${room}/cues`, [{ label: 'Initial RTC cue', durationMS: 600000 }]);
  const screen = await endpoint('POST', `/api/shows/${room}/screens/link`, { name: 'RTC qualification display' });
  const operator = await client(await api.storageState());
  await operator.page.goto(`${base}/c/${room}`);
  await operator.page.waitForFunction(() => window.__tpmesh?.serverOnline());
  const display = await client();
  await display.page.goto(new URL(screen.link, base).href);
  await display.page.waitForFunction(() => window.__tpmesh?.serverOnline());
  await Promise.all(clients.map(({ page }) => page.waitForFunction(() => window.__tpmesh.openPeerIds().length > 0, null, { timeout: 30000 })));
  const a = await state(operator.page), b = await state(display.page);
  check('real RTC connection and shared operator master', a.isMaster && !b.isMaster && a.masterId === b.masterId && a.peerId === b.masterId && [a, b].every(s => s.channels.some(c => c.connection === 'connected' && c.channel === 'open')),
    { operatorChannels: a.channels, displayChannels: b.channels });
  for (const { state: s } of clients) {
    s.down = true;
    for (const { socket, server } of s.sockets) { socket.close(); server.close(); }
  }
  await Promise.all(clients.map(({ page }) => page.waitForFunction(() => !window.__tpmesh.serverOnline())));
  for (const { page } of clients) {
    const blocked = await page.evaluate(() => fetch('/health').then(() => false, () => true));
    check('browser HTTP partition enforced', blocked);
  }
  await operator.page.evaluate(() => {
    window.__tpmesh.sendCommand('cueAdd', { label: 'Offline RTC cue', durationMS: 600000 });
    window.__tpmesh.sendCommand('go', {});
    window.__tpmesh.sendCommand('addMsg', { text: 'OFFLINE RTC MESSAGE', show: true });
  });
  await display.page.waitForFunction(() => window.__tpmesh.snap.cues.some(c => c.label === 'Offline RTC cue') && window.__tpmesh.snap.runtime.running && window.__tpmesh.snap.messages.some(m => m.text === 'OFFLINE RTC MESSAGE'), null, { timeout: 10000 });
  const offlineA = await state(operator.page), offlineB = await state(display.page);
  check('offline edit GO and message reach display over RTC', !offlineA.online && !offlineB.online && offlineA.labels.join() === offlineB.labels.join() && offlineB.rtcMessages > b.rtcMessages,
    { messagesReceived: offlineB.rtcMessages - b.rtcMessages, labels: offlineB.labels, connections: offlineB.channels });
  check('display paints offline stage message', offlineB.visibleMessage === 'OFFLINE RTC MESSAGE');
  await display.page.waitForTimeout(2100);
  const advanced = await state(display.page);
  check('offline display countdown advances', advanced.elapsed - offlineB.elapsed >= 1900 && advanced.clock !== offlineB.clock && /^[0-9]+:[0-9]{2}/.test(advanced.clock), { elapsedAdvanceMS: advanced.elapsed - offlineB.elapsed, before: offlineB.clock, after: advanced.clock });
  const during = await endpoint('GET', `/api/shows/${room}`);
  check('Pi did not receive offline changes during partition', !JSON.stringify(during).includes('Offline RTC cue') && !JSON.stringify(during).includes('OFFLINE RTC MESSAGE'));
  for (const entry of clients) { entry.state.down = false; await entry.page.evaluate(() => window.dispatchEvent(new Event('online'))); }
  await Promise.all(clients.map(({ page }) => page.waitForFunction(() => window.__tpmesh.serverOnline(), null, { timeout: 25000 })));
  let restored;
  for (let i = 0; i < 40; i++) {
    restored = await endpoint('GET', `/api/shows/${room}`);
    if (JSON.stringify(restored).includes('Offline RTC cue') && JSON.stringify(restored).includes('OFFLINE RTC MESSAGE')) break;
    await new Promise(r => setTimeout(r, 250));
  }
  check('reconnect automatically commits offline changes to real Pi', JSON.stringify(restored).includes('Offline RTC cue') && JSON.stringify(restored).includes('OFFLINE RTC MESSAGE'));
  result.passed = true;
} catch (error) {
  result.passed = false;
  result.error = error.message;
  console.error('FAIL', error.message);
  for (const { page } of clients) { try { const s = await state(page); delete s.peerId; delete s.masterId; console.log('diagnostic', JSON.stringify(s)); } catch {} }
} finally {
  for (const { browser } of clients) await browser.close();
  if (api && event && (ownsEvent || room)) { try { await endpoint('DELETE', ownsEvent ? '/api/events/' + event : `/api/events/${event}/rooms/${room}`); } catch (error) { result.cleanupError = error.message; result.passed = false; } }
  await api?.dispose();
  result.completedAt = new Date().toISOString();
  if (output) writeFileSync(output, JSON.stringify(result, null, 2) + '\n');
  process.exitCode = result.passed ? 0 : 1;
}
