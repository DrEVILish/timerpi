// BUGLOG RW33: tiles never land below the 48-row canvas. A full board
// refuses a new tile with a message (freeSpot used to drop it at y=90,
// where the grid hid it), and the server clamps a tile sent at y=80.
export const name = 'layout editor keeps tiles inside the canvas';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const nb = await (await ctx.request.post(`${t.base}/api/shows/${room}/boards`, { data: { name: 'Full' }, headers: H })).json();
  const full = {
    v: 1, rows: 48, orientation: 'landscape',
    widgets: [0, 12, 24, 36].map((y, i) => ({ id: `c${i}`, type: 'countdown', x: 0, y, w: 12, h: 12 })),
  };
  const put = await ctx.request.put(`${t.base}/api/shows/${room}/boards/${nb.id}`, { data: { layout: full }, headers: H });
  t.check('a full 48-row board saves', put.ok());
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
  await pg.$eval('#b-palette-list [data-add="wallclock"]', (b) => b.click());
  await pg.waitForTimeout(1200);
  const state = await pg.$eval('#b-save-state', (el) => el.textContent);
  t.check(`a full board says there is no room (${state})`, /No room/.test(state));
  t.check('a full board keeps its four tiles', (await layoutOf()).widgets.length === 4);

  const low = { v: 1, widgets: [{ id: 'low', type: 'countdown', x: 0, y: 80, w: 4, h: 3 }] };
  await ctx.request.put(`${t.base}/api/shows/${room}/boards/${nb.id}`, { data: { layout: low }, headers: H });
  const w = (await layoutOf()).widgets[0];
  t.check(`a tile sent at y=80 is kept inside 48 rows (y=${w.y} h=${w.h})`, w.y + w.h <= 48);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
