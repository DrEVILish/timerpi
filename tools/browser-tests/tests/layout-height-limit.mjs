export const name = 'layout editor height agrees with the saved screen';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const url = `${t.base}/api/shows/${room}/boards`;
  const made = await (await ctx.request.post(url, { data: { name: 'Tall canvas' }, headers: H })).json();
  const layout = { v: 1, rows: 24, orientation: 'landscape', widgets: [
    { id: 'tall', type: 'notice', x: 0, y: 0, w: 6, h: 12, opts: { text: 'Height' } },
  ] };
  const response = await ctx.request.put(`${url}/${made.id}`, { data: { layout }, headers: H });
  t.check('the server accepts a tall canvas', response.ok());
  const pg = await ctx.newPage();
  await pg.setViewportSize({ width: 390, height: 844 });
  await pg.goto(`${t.base}/d/${room}?view=board&board=${made.id}&edit=1&preview=1&compose=1`);
  await pg.locator('#b-editor [data-row-wid="tall"] .b-ed-name').click();
  await pg.locator('#b-editor [data-row-wid="tall"] button[aria-label="notice H +"]').click();
  t.check('the editor keeps the server maximum of 12 rows', await pg.$eval('#b-w-tall', (el) => el.style.gridRow.endsWith('span 12')));
  await pg.waitForTimeout(1000);
  const list = await (await ctx.request.get(url)).json();
  const saved = (list.boards || list).find((b) => b.id === made.id).layout;
  t.check('preview and persisted tile height match', saved.widgets[0].h === 12 &&
    await pg.$eval('#b-w-tall', (el) => el.style.gridRow.endsWith('span 12')));
  await ctx.close();
}
