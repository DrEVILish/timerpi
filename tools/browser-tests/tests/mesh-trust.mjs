// E2E #1/#7/#9: the browser mesh carries operator state only between
// trusted tabs. An unkeyed display (room code is on the audience QR) gets
// no secrets over WebRTC or WS; a keyed display still gets the operator's
// state peer-to-peer; a display that loses the server shows LINK DOWN
// unless it still reaches an operator; display pages never toast server
// errors.
export const name = 'mesh: secrets only to trusted peers, honest LINK DOWN';

const spy = () => {
  window.__dc = []; window.__ws = [];
  const d = Object.getOwnPropertyDescriptor(RTCDataChannel.prototype, 'onmessage');
  Object.defineProperty(RTCDataChannel.prototype, 'onmessage', {
    set(fn) { this.addEventListener('message', (e) => window.__dc.push(String(e.data))); d.set.call(this, fn); },
    get() { return d.get.call(this); },
  });
  const W = window.WebSocket;
  window.WebSocket = class extends W {
    constructor(...a) { super(...a); this.addEventListener('message', (e) => window.__ws.push(String(e.data))); }
  };
};

// A display context whose /ws can be cut (and kept down) on demand.
async function displayCtx(t) {
  const ctx = await t.browser.newContext({ viewport: { width: 1280, height: 720 } });
  await ctx.addInitScript(spy);
  const state = { down: false, open: new Set() };
  await ctx.routeWebSocket(/\/ws$/, (ws) => {
    if (state.down) { ws.close(); return; }
    ws.connectToServer();
    state.open.add(ws);
  });
  state.cut = () => { state.down = true; for (const ws of state.open) ws.close(); };
  return { ctx, state };
}

const offlineShown = (pg) => pg.evaluate(() => !!document.querySelector('#tp-offline.is-visible'));

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const link = async (name) => {
    const r = await ctx.request.post(`${t.base}/api/shows/${room}/screens/link`, { data: { name }, headers: H });
    return (await r.json()).link;
  };

  // Operator puts secrets into the room.
  const op = await ctx.newPage();
  await op.goto(`${t.base}/c/${room}`);
  await op.waitForFunction(() => window.__tpmesh?.serverOnline());
  await op.evaluate(() => {
    window.__tpmesh.sendCommand('cueAdd', { label: 'Keynote', durationMS: 600000, notes: 'SECRET-CUE-NOTE' });
    window.__tpmesh.sendCommand('addMsg', { text: 'SECRET-STAGE-MSG', show: true });
  });
  await op.waitForFunction(() => window.__tpmesh.snap?.messages?.length > 0);

  // Unkeyed display (anyone with the room code) and a keyed one.
  const anon = await displayCtx(t);
  const ap = await anon.ctx.newPage();
  await ap.goto(`${t.base}/d/${room}?screen=Lobby`);
  const keyed = await displayCtx(t);
  const kp = await keyed.ctx.newPage();
  const kl = await link('Stage');
  await kp.goto(kl.startsWith('http') ? kl : t.base + kl);
  await kp.waitForFunction(() => window.__dc.some((m) => m.includes('"hi')), null, { timeout: 15000 })
    .catch(() => {});
  await ap.waitForTimeout(2500);

  const secrets = (s) => [...new Set(s.match(/SECRET-[A-Z-]+/g) || [])].join(',');
  const a = await ap.evaluate(() => ({ dc: window.__dc.join('\n'), ws: window.__ws.join('\n'), n: window.__dc.length }));
  t.check(`unkeyed display: no secrets over the data channel (${secrets(a.dc) || 'none'}, ${a.n} msgs)`, !secrets(a.dc));
  t.check(`unkeyed display: no secrets over the websocket (${secrets(a.ws) || 'none'})`, !secrets(a.ws));
  const peerIds = a.ws.split('\n').flatMap((m) => { try { return JSON.parse(m).peers || []; } catch { return []; } });
  t.check(`unkeyed display: no peer ids from the hub (${peerIds.length})`, peerIds.length === 0);
  const k = await kp.evaluate(() => window.__dc.join('\n'));
  t.check('keyed display: operator state still arrives peer-to-peer', k.includes('SECRET-STAGE-MSG'));

  // #9: a server err frame does not toast on a display page.
  await ap.evaluate(() => window.__tpmesh?._ws?.send(JSON.stringify({ t: 'cmd', action: 'go' })));
  await ap.waitForTimeout(800);
  t.check('display page: server error not toasted', await ap.evaluate(() => !document.querySelector('#toast-region .toast')));

  // #7: server gone. The keyed display still reaches the operator over the
  // mesh (live); the unkeyed one is alone — LINK DOWN, not a self-elected
  // "master" link.
  keyed.state.cut();
  anon.state.cut();
  await ap.waitForTimeout(3500);
  t.check('lone display without the server shows LINK DOWN', await offlineShown(ap));
  t.check('lone display: linkLive false though it elected itself master',
    await ap.evaluate(() => window.__tpmesh.isMaster() && !window.__tpmesh.linkLive()));
  t.check('keyed display reaching the operator over the mesh stays live', !(await offlineShown(kp)));

  // Operator leaves: the keyed display is on its own now.
  await op.close();
  // (the data channel's close can take the ICE timeout to notice)
  const t0 = Date.now();
  await kp.waitForFunction(() => !!document.querySelector('#tp-offline.is-visible'), null, { timeout: 40000 }).catch(() => {});
  t.check(`keyed display without operator or server shows LINK DOWN (${Date.now() - t0} ms)`, await offlineShown(kp));

  await anon.ctx.close();
  await keyed.ctx.close();
  await ctx.close();
}
