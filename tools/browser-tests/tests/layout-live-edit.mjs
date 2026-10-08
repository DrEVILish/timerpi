// E2E #10: a layout edit reaches the live screens showing it (assigned or
// as the event default) without a manual reload, and deleting a layout in
// use sends its screens back to the plain timer at once.
export const name = 'layout edits and deletes reach live screens';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = async (method, path, data) => (await ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H })).json();
  const b = await api('POST', `/api/shows/${room}/boards`, { name: 'Live' });
  await api('POST', `/api/shows/${room}/screens/config`, { name: 'Stage TV', theme: '', boardId: b.id, room: '' });
  const link = (await api('POST', `/api/shows/${room}/screens/link`, { name: 'Stage TV' })).link;

  const tvCtx = await t.browser.newContext({ viewport: { width: 960, height: 540 } });
  const tv = await tvCtx.newPage();
  await tv.goto(link);
  await tv.waitForSelector('#b-grid');
  const lobby = await tvCtx.newPage(); // unnamed: shows the event default (the only layout)
  await lobby.goto(`${t.base}/d/${room}?view=board`);
  await lobby.waitForSelector('#b-grid');
  await tv.waitForTimeout(1200);

  const one = (text) => ({ v: 1, rows: 8, orientation: 'landscape', widgets: [{ id: 'n1', type: 'notice', x: 0, y: 0, w: 6, h: 2, opts: { text } }] });
  await api('PUT', `/api/shows/${room}/boards/${b.id}`, { layout: one('Edited live') });
  const shows = (pg, s) => pg.waitForFunction((s) => document.querySelector('.b-js-notice')?.textContent === s, s, { timeout: 6000 }).then(() => true, () => false);
  t.check('the assigned screen shows the edit without a reload', await shows(tv, 'Edited live'));
  t.check('a screen showing it as the default does too', await shows(lobby, 'Edited live'));

  await api('DELETE', `/api/shows/${room}/boards/${b.id}`);
  const plain = await tv.waitForFunction(() => !location.search.includes('view=board') && !!document.querySelector('#d-stage'), null, { timeout: 6000 }).then(() => true, () => false);
  t.check(`deleting it sends the assigned screen to the plain timer (${tv.url().replace(/key=\w+/, 'key=…')})`, plain);
  const fallback = await lobby.waitForFunction(() => document.querySelector('.b-js-notice')?.textContent !== 'Edited live', null, { timeout: 6000 }).then(() => true, () => false);
  t.check('the default screen falls back to the factory layout', fallback);
  await tvCtx.close();
  await ctx.close();
}
