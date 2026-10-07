// The layout editor edits one named layout's landscape and portrait
// versions: a switch flips between them and a save keeps both (owner
// 2026-10-07).
export const name = 'layout editor: landscape/portrait switch saves both versions';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent(['Stark']);
  const made = await (await ctx.request.post(`${t.base}/api/shows/${room}/layouts`, { data: { name: 'Door poster', template: 'room' }, headers: H })).json();
  const id = made.boardId || made.id;
  t.check(`a layout made from a built-in (${id})`, !!id);
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/d/${room}?view=board&edit=1&preview=1&compose=1&board=${id}&orient=landscape`);
  await pg.waitForSelector('#b-orient [data-orient="portrait"]');
  t.check('editing the landscape version', await pg.evaluate(() => document.documentElement.dataset.layoutOrient) === 'landscape');
  await pg.click('#b-orient [data-orient="portrait"]');
  await pg.waitForFunction(() => document.documentElement.dataset.layoutOrient === 'portrait', null, { timeout: 8000 });
  await pg.waitForSelector('#b-orient [data-orient="portrait"].is-active');
  t.check('the switch opened the portrait version', true);
  const get = async () => {
    const boards = await (await ctx.request.get(`${t.base}/api/shows/${room}/boards`, { headers: H })).json();
    return (boards.boards || boards).find((x) => x.id === id)?.layout;
  };
  const before = await get();
  t.check(`saved with both versions (${before?.orientation} + ${before?.alt?.orientation})`,
    before?.orientation === 'landscape' && before?.alt?.orientation === 'portrait' && before.alt.widgets.length > 0);
  // Add a tile to the portrait version: only that version changes.
  await pg.click('[data-add="notice"]');
  await pg.waitForFunction(() => document.documentElement.dataset.layoutOrient === 'portrait' && document.querySelectorAll('.b-widget').length > 0, null, { timeout: 8000 });
  await pg.waitForTimeout(800);
  const after = await get();
  t.check(`portrait edit saved (${before.alt.widgets.length} → ${after?.alt?.widgets?.length} tiles); landscape untouched (${before.widgets.length} → ${after?.widgets?.length})`,
    after?.alt?.widgets?.length === before.alt.widgets.length + 1 && after.widgets.length === before.widgets.length);
  await ctx.close();
}
