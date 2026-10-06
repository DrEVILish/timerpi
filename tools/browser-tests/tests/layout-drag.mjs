// BUGLOG RC3: dragging a tile in the layout editor saves, and the arrow-key
// nudge moves the tile on screen and saves too.
export const name = 'layout editor drag and arrow-key nudge save';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const nb = await (await ctx.request.post(`${t.base}/api/shows/${room}/boards`, { data: { name: 'Edit me' }, headers: H })).json();
  const layoutOf = async () => {
    const l = await (await ctx.request.get(`${t.base}/api/shows/${room}/boards`)).json();
    const b = (l.boards || l).find((x) => x.id === nb.id);
    return typeof b.layout === 'string' ? JSON.parse(b.layout) : b.layout;
  };
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/d/${room}?view=board&board=${nb.id}&edit=1&preview=1`);
  await pg.click('#b-edit-toggle');
  const before = await layoutOf();
  let moved = null;
  for (const tile of await pg.$$('.b-widget')) {
    const wid = await tile.getAttribute('data-wid');
    const w0 = before.widgets.find((w) => w.id === wid);
    const bb = await (await tile.$('.b-w-chrome')).boundingBox();
    await pg.mouse.move(bb.x + 8, bb.y + bb.height / 2);
    await pg.mouse.down();
    await pg.mouse.move(bb.x + 8 + 1280 / 12, bb.y + bb.height / 2, { steps: 6 });
    await pg.mouse.up();
    await pg.waitForTimeout(1200);
    const w1 = (await layoutOf()).widgets.find((w) => w.id === wid);
    if (w0 && w1 && w1.x !== w0.x) { moved = { wid, from: w0.x }; break; }
  }
  t.check('a dragged tile saves its new position', !!moved);
  if (moved) {
    await pg.keyboard.press('ArrowLeft');
    await pg.waitForTimeout(1200);
    const w2 = (await layoutOf()).widgets.find((w) => w.id === moved.wid);
    t.check('arrow-key nudge saves', w2.x === moved.from);
    const style = await pg.$eval(`[data-wid="${moved.wid}"]`, (el) => el.style.gridColumn);
    t.check('arrow-key nudge moves the tile on screen', style.startsWith(`${moved.from + 1} `));
  }
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
