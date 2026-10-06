// BUGLOG RW45: deleting a room sends its live screens to the waiting
// overlay at once, and they stay there — no flicker, no reconnect storm. RW46: dropped WebRTC
// connections are closed, and a late event from an old connection never
// removes a newer one.
export const name = 'screen of a deleted room waits quietly; peer connections are closed';

export async function run(t) {
  const { ctx, H, event, rooms } = await t.newEvent(['Main', 'Doomed']);
  const doomed = rooms[1];
  const tvCtx = await t.browser.newContext({ viewport: { width: 1280, height: 720 } });
  await tvCtx.addInitScript(() => {
    const Real = window.WebSocket;
    window.__socks = 0;
    window.WebSocket = function (...a) { window.__socks++; return new Real(...a); };
    window.WebSocket.prototype = Real.prototype;
    Object.assign(window.WebSocket, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 });
    window.__flips = 0;
    new MutationObserver((ms) => { for (const m of ms) if (m.attributeName === 'data-waiting') window.__flips++; })
      .observe(document, { attributes: true, subtree: true, attributeFilter: ['data-waiting'] });
  });
  const tv = await tvCtx.newPage();
  const errs = [];
  tv.on('pageerror', (e) => errs.push(e.message));
  await tv.goto(`${t.base}/d/${doomed}?screen=Lobby`);
  await tv.waitForTimeout(1500);
  const del = await ctx.request.delete(`${t.base}/api/events/${event}/rooms/${doomed}`, { headers: H });
  t.check('the room is deleted', del.ok());
  await tv.waitForFunction(() => document.body.hasAttribute('data-waiting'), null, { timeout: 5000 });
  t.check('the live screen goes to the waiting overlay', true);
  const s0 = await tv.evaluate(() => ({ socks: window.__socks, flips: window.__flips }));
  await tv.waitForTimeout(12000);
  const s1 = await tv.evaluate(() => ({ socks: window.__socks, flips: window.__flips, waiting: document.body.hasAttribute('data-waiting') }));
  t.check(`still waiting after 12 s (${s1.waiting})`, s1.waiting);
  t.check(`no reconnect storm (${s1.socks - s0.socks} new sockets in 12 s)`, s1.socks - s0.socks <= 1);
  t.check(`the overlay does not flicker (${s1.flips - s0.flips} flips)`, s1.flips - s0.flips === 0);

  // RW46, on the Mesh class itself.
  const r = await tv.evaluate(async () => {
    const { Mesh } = await import('/src/mesh.js');
    const m = new Mesh({ showId: 'X' });
    const fake = () => { const pc = { closed: 0, close() { this.closed++; } }; return pc; };
    const oldPc = fake(), newPc = fake();
    m.connections.set('peer', { pc: newPc, dc: null, peerId: 'peer' });
    m._dropConnection('peer', oldPc); // a late event from the old one
    const keptNew = m.connections.get('peer')?.pc === newPc;
    m._dropConnection('peer', newPc);
    return { keptNew, oldClosed: oldPc.closed, newClosed: newPc.closed, gone: !m.connections.has('peer') };
  });
  t.check('a late event from an old connection keeps the newer one', r.keptNew);
  t.check('both connections were closed', r.oldClosed === 1 && r.newClosed === 1);
  t.check('the dropped connection is forgotten', r.gone);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await tvCtx.close();
  await ctx.close();
}
