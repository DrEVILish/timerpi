export const name = 'layout alignment and phone edits share collision rules';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const url = `${t.base}/api/shows/${room}/boards`;
  const created = await (await ctx.request.post(url, { data: { name: 'Two tiles' }, headers: H })).json();
  const layout = {
    v: 1, rows: 8, orientation: 'landscape',
    widgets: ['a', 'b'].map((id, i) => ({ id, type: 'notice', x: i * 6, y: 0, w: 6, h: 4, opts: { text: id } })),
  };
  const reset = () => ctx.request.put(`${url}/${created.id}`, { data: { layout }, headers: H });
  const read = async () => {
    const list = await (await ctx.request.get(url)).json();
    const saved = (list.boards || list).find((b) => b.id === created.id).layout;
    return typeof saved === 'string' ? JSON.parse(saved) : saved;
  };
  await reset();
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/d/${room}?view=board&board=${created.id}&edit=1&preview=1`);
  await pg.click('#b-edit-toggle');
  await pg.$eval('#b-w-a [data-align="right"]', (b) => b.click());
  await pg.waitForFunction(() => document.querySelector('#b-save-state')?.textContent.startsWith('Saved '));
  let saved = await read();
  const a = saved.widgets.find((w) => w.id === 'a'), b = saved.widgets.find((w) => w.id === 'b');
  t.check(`aligning into one neighbour swaps and saves both tiles (a=${a.x}, b=${b.x})`, a.x === 6 && b.x === 0);
  t.check('alignment paints the moved neighbour too', await pg.$eval('#b-w-b', (el) => el.style.gridColumn.startsWith('1 /')));

  await reset();
  await pg.setViewportSize({ width: 390, height: 844 });
  await pg.reload();
  await pg.click('#b-edit-toggle');
  await pg.click('#b-w-a');
  // Growing into the neighbour uses the same trim rule as a pointer resize.
  await pg.locator('#b-editor [data-row-wid="a"] button[aria-label="notice W +"]').click();
  await pg.waitForFunction(() => document.querySelector('#b-save-state')?.textContent.startsWith('Saved '));
  saved = await read();
  const phoneA = saved.widgets.find((w) => w.id === 'a'), phoneB = saved.widgets.find((w) => w.id === 'b');
  t.check('phone resize saves the tile and trimmed neighbour', phoneA.w === 7 && phoneB.x === 7 && phoneB.w === 5);
  await ctx.close();
}
