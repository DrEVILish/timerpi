// BUGLOG RW23 / RW22 / RS11 on the audience phone page.
export const name = 'phone page: stable ask box, one socket, refused votes undone';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const qa = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'qa', question: 'Ask the panel', autoApprove: true })).json();
  await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target: 'audience', on: true });

  const phoneCtx = await t.browser.newContext({ viewport: { width: 390, height: 780 }, isMobile: true, hasTouch: true });
  // Count sockets the page opens (RW22).
  await phoneCtx.addInitScript(() => {
    const Real = window.WebSocket;
    window.__socks = [];
    window.WebSocket = function (...a) { const s = new Real(...a); window.__socks.push(s); return s; };
    window.WebSocket.prototype = Real.prototype;
    Object.assign(window.WebSocket, { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 });
  });
  const phone = await phoneCtx.newPage();
  await phone.goto(`${t.base}/a/${room}`);
  await phone.waitForSelector('textarea');

  // RW23: typing survives live updates (another phone's questions land).
  await phone.click('textarea');
  await phone.keyboard.type('Will slides be shared');
  const other = await (await t.browser.newContext()).newPage();
  await other.goto(`${t.base}/a/${room}`);
  for (const q of ['First?', 'Second?']) {
    await other.fill('textarea', q);
    await other.click('form.tp-aud-ask button[type=submit]');
    await other.waitForTimeout(3200); // the per-phone ask throttle is 3 s
  }
  const st = await phone.evaluate(() => ({ v: document.querySelector('textarea').value, f: document.activeElement?.tagName }));
  t.check(`draft kept with focus through live updates ("${st.v}", ${st.f})`, st.v === 'Will slides be shared' && st.f === 'TEXTAREA');
  t.check('the wall updated meanwhile', (await phone.textContent('.tp-aud-wall')).includes('Second?'));
  await phone.click('form.tp-aud-ask button[type=submit]');
  await phone.waitForTimeout(1200);
  t.check('after sending, the box is empty', (await phone.$eval('textarea', (x) => x.value)) === '');
  const items = (await (await api('GET', `/api/shows/${room}/polls`)).json()).items;
  const mine = items.find((i) => i.id === qa.id).children.filter((c) => c.question === 'Will slides be shared');
  t.check(`sent exactly once (${mine.length})`, mine.length === 1);

  // RW22: drop the socket, then wake the page twice: still one live socket.
  await phone.evaluate(() => { window.__socks.forEach((s) => s.close()); });
  await phone.waitForTimeout(200);
  await phone.evaluate(() => { for (let i = 0; i < 2; i++) document.dispatchEvent(new Event('visibilitychange')); });
  await phone.waitForTimeout(4500); // past the retry backoff
  const live = await phone.evaluate(() => window.__socks.filter((s) => s.readyState <= 1).length);
  t.check(`one live socket after drop + wakes (${live})`, live === 1);

  // RS11: a vote the server refuses is not shown as this phone's.
  const poll = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'poll', question: 'Lunch?', options: ['Pizza', 'Soup'] })).json();
  await api('POST', `/api/shows/${room}/polls/${qa.id}/hide`, {});
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'audience', on: true });
  await phone.waitForSelector('.tp-aud-opt');
  await phone.route('**/vote', (r) => r.fulfill({ status: 400, contentType: 'application/json', body: '{"ok":false,"error":"voting is not open"}' }));
  await phone.click('.tp-aud-opt >> nth=0');
  await phone.waitForTimeout(800);
  t.check('refused vote not marked as mine', (await phone.getAttribute('.tp-aud-opt >> nth=0', 'aria-pressed')) === 'false');
  await ctx.close();
}
