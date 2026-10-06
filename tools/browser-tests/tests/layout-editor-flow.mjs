// STATUS U8 + U10 + U11: built-ins sit in the Layout list as [built-in];
// Edit layout on a built-in asks for a name and the screens (same type,
// whole event, by room), makes an event layout, and opens an almost
// full-screen editor already in edit mode.
export const name = 'layout editor: [built-in] list, named copy, one-step editor';

export async function run(t) {
  const { ctx, H, rooms } = await t.newEvent(['Stark', 'Banner']);
  const [stark, banner] = rooms;
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  await api('POST', `/api/shows/${stark}/screens/config`, { name: 'Stark door', kind: 'walkin' });
  await api('POST', `/api/shows/${stark}/screens/config`, { name: 'Stark DSM', kind: 'presenter' });
  await api('POST', `/api/shows/${banner}/screens/config`, { name: 'Banner door', kind: 'walkin' });
  await api('POST', `/api/shows/${stark}/screens/template`, { name: 'Stark door', template: 'room' });

  const pg = await ctx.newPage();
  await pg.setViewportSize({ width: 1440, height: 900 });
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/screens/${stark}`);
  const sel = 'select[aria-label="Layout of Stark door"]';
  await pg.waitForSelector(sel);
  const picked = await pg.$eval(sel, (s) => s.selectedOptions[0]?.textContent);
  t.check(`U10: the built-in shows in the same list as "[built-in] …" (${picked})`, picked === '[built-in] Room walk-in');

  // Edit layout on the built-in → name + screens dialog.
  const card = pg.locator('.tp-scr', { has: pg.locator('.tp-scr-name', { hasText: 'Stark door' }) });
  await card.locator('button:text-is("Edit layout")').click();
  await pg.waitForSelector('#tp-layout-copy[open]');
  const labels = await pg.$$eval('#tp-layout-copy-list label', (ls) => ls.map((l) => `${l.textContent.trim()}${l.querySelector('input').checked ? '*' : ''}`));
  t.check(`U11: same-type screens across the event, this one ticked (${labels.join(', ')})`,
    labels.includes('Stark door*') && labels.includes('Banner door') && !labels.some((l) => l.startsWith('Stark DSM')));
  await pg.fill('#tp-layout-copy-name', 'Walk-in v2');
  await pg.check('#tp-layout-copy-list input[data-name="Banner door"]');
  await pg.click('#tp-layout-copy button[type=submit]');

  // The editor: almost full screen, already editing, shared warning.
  await pg.waitForSelector('#tp-screen-edit[open]');
  const box = await (await pg.$('#tp-screen-edit')).boundingBox();
  t.check(`U8: the editor fills most of the window (${Math.round(box.width)}×${Math.round(box.height)})`, box.width > 1440 * 0.9 && box.height > 900 * 0.9);
  t.check('U8: shared warning shown', (await pg.textContent('#tp-screen-edit-shared')).includes('Used by 2 screens'));
  const frame = pg.frameLocator('#tp-screen-edit-frame');
  await frame.locator('body[data-editing]').waitFor({ timeout: 8000 });
  t.check('U8: already in edit mode (no second "Edit layout" press)', true);
  t.check('U8: the in-frame Edit toggle is hidden', !(await frame.locator('#b-edit-toggle').isVisible()));
  const tips = await frame.locator('.b-w-chrome').first().getAttribute('title');
  t.check(`U8: handles explain themselves (move tooltip: "${tips}")`, /^Drag to move/.test(tips || ''));

  // Both screens now use the new event layout.
  const scr = async (room, name) => (await (await api('GET', `/api/shows/${room}/screens`)).json()).screens.find((s) => s.name === name);
  const a = await scr(stark, 'Stark door');
  const b = await scr(banner, 'Banner door');
  t.check(`U11: both ticked screens use the copy (${a.boardId}, ${b.boardId})`, a.boardId > 0 && a.boardId === b.boardId && !a.template);
  const layouts = await (await api('GET', `/api/shows/${banner}/boards`)).json();
  t.check('U11: the copy is an event layout, listed in the other room too', layouts.some((l) => l.name === 'Walk-in v2'));

  // A drag inside the editor saves (handlers are live in compose mode).
  const layoutOf = async () => (await (await api('GET', `/api/shows/${stark}/boards`)).json()).find((l) => l.id === a.boardId).layout;
  const before = await layoutOf();
  const parse = (l) => (typeof l === 'string' ? JSON.parse(l) : l);
  let moved = false;
  for (const chrome of await frame.locator('.b-w-chrome').all()) {
    const bb = await chrome.boundingBox();
    await pg.mouse.move(bb.x + 30, bb.y + bb.height / 2);
    await pg.mouse.down();
    await pg.mouse.move(bb.x + 30 + bb.width, bb.y + bb.height / 2, { steps: 8 });
    await pg.mouse.up();
    await pg.waitForTimeout(1200);
    if (JSON.stringify(parse(await layoutOf()).widgets) !== JSON.stringify(parse(before).widgets)) { moved = true; break; }
  }
  t.check('U8: dragging a tile in the editor saves', moved);
  if (process.env.SHOTS) await pg.screenshot({ path: `${process.env.SHOTS}/editor.png` });
  await pg.click('#tp-screen-edit-close');
  // Picking a built-in from the list applies directly.
  await pg.waitForSelector(sel);
  await pg.selectOption(sel, 't:clockroom');
  await pg.waitForTimeout(1200);
  const c = await scr(stark, 'Stark door');
  t.check(`U10: picking a built-in applies it as is (${c.template}, board ${c.boardId})`, c.template === 'clockroom' && c.boardId === 0);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
