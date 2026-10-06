// BUGLOG RW43: when the server refuses a layout change, the editor keeps
// the page and its "Save failed" message instead of reloading it away.
export const name = 'layout editor keeps the page when a save fails';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const nb = await (await ctx.request.post(`${t.base}/api/shows/${room}/boards`, { data: { name: 'Fails' }, headers: H })).json();
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/d/${room}?view=board&board=${nb.id}&edit=1&preview=1`);
  await pg.click('#b-edit-toggle');
  const tiles = await pg.$$eval('.b-widget', (n) => n.length);
  await pg.route('**/boards/*', (r) => (r.request().method() === 'PUT'
    ? r.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"disk full"}' })
    : r.continue()));
  await pg.evaluate(() => { window.__noReload = 1; });
  await pg.$eval('#b-palette-list [data-add="wallclock"]', (b) => b.click());
  await pg.waitForTimeout(1500);
  t.check('a failed save does not reload the page', await pg.evaluate(() => window.__noReload === 1));
  const state = await pg.$eval('#b-save-state', (el) => el.textContent);
  t.check(`the failure is shown (${state})`, /Save failed: disk full/.test(state));
  await pg.unroute('**/boards/*');
  // The rolled-back layout matches the server: the next change saves cleanly
  // with the original tiles plus the new one.
  await pg.$eval('#b-palette-list [data-add="wallclock"]', (b) => b.click());
  await pg.waitForLoadState('load');
  await pg.waitForTimeout(1500);
  const l = await (await ctx.request.get(`${t.base}/api/shows/${room}/boards`)).json();
  const b = (l.boards || l).find((x) => x.id === nb.id);
  const layout = typeof b.layout === 'string' ? JSON.parse(b.layout) : b.layout;
  t.check(`after the failure, adding saves one new tile (${tiles} → ${layout.widgets.length})`, layout.widgets.length === tiles + 1);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
