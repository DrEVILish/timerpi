// Screens page: Type → Mounted → Layout. Changing the type or the mounting
// refits a built-in layout, and the Layout list offers only layouts that
// fit both (owner bug 2026-10-07).
export const name = 'screen card: type and mounting keep the layout fitting';

const S = process.env.SHOTS;

export async function run(t) {
  const { ctx, H, room } = await t.newEvent(['Stark']);
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/template`, { data: { name: 'Door', template: 'room' }, headers: H });
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/screens/${room}`);
  const sel = (what) => pg.locator(`.tp-scr select[aria-label^="${what}"]`).first();
  const layouts = async () => (await sel('Layout').locator('option').allTextContents()).filter((x) => x.startsWith('[built-in]'));
  await sel('Display type').waitFor();
  t.check(`landscape walk-in offers landscape walk-in layouts (${(await layouts()).join(', ')})`,
    (await layouts()).every((x) => !x.includes('portrait')) && (await layouts()).includes('[built-in] Room walk-in'));

  await sel('Rotation').selectOption('90');
  await pg.waitForFunction(() => document.querySelector('.tp-scr select[aria-label^="Layout"]')?.value === 't:room-portrait');
  t.check(`portrait mounting switched to the portrait twin; list is portrait only (${(await layouts()).join(', ')})`,
    (await layouts()).every((x) => x.includes('portrait')));

  await sel('Display type').selectOption('presenter');
  await pg.waitForFunction(() => document.querySelector('.tp-scr select[aria-label^="Layout"]')?.value === 't:stage-portrait');
  t.check('presenter type picked a presenter portrait layout, not blank', await sel('Layout').inputValue() === 't:stage-portrait');
  t.check('mounting kept', await sel('Rotation').inputValue() === '90');

  await sel('Layout').selectOption('t:timer-portrait');
  await pg.waitForTimeout(800);
  t.check('picking a layout keeps the mounting', await sel('Rotation').inputValue() === '90');

  // A real pick leaves focus on the select; the card must still redraw
  // with the new type's layouts (owner bug: the list kept the old type's).
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/template`, { data: { name: 'Plain', template: '' }, headers: H });
  await pg.reload();
  const pType = pg.locator('select[aria-label="Display type of Plain"]');
  const pLayout = 'select[aria-label="Layout of Plain"]';
  await pType.waitFor();
  // Change the type with the keyboard on the focused select, as a person
  // does: focus stays on it, and the card must still redraw.
  const want = { audience: 'Audience main', walkin: 'Room walk-in', presenter: 'Full timer' };
  const other = { audience: 'Room walk-in', walkin: 'Full timer', presenter: 'Audience main' };
  await pType.focus();
  for (let i = 0; i < 3; i++) {
    await pg.keyboard.press('ArrowDown');
    const kind = await pType.inputValue();
    await pg.waitForFunction(([sel, w]) => document.querySelector(sel)?.textContent.includes(w), [pLayout, want[kind]], { timeout: 5000 }).catch(() => {});
    const opts = (await pg.locator(`${pLayout} option`).allTextContents()).filter((x) => x.startsWith('[built-in]'));
    t.check(`type ${kind} lists only its layouts (${opts.join(', ')})`, opts.includes(`[built-in] ${want[kind]}`) && !opts.includes(`[built-in] ${other[kind]}`));
    await pType.focus();
  }

  if (S) {
    const keys = (await (await ctx.request.get(`${t.base}/api/board-templates`)).json()).catalog.map((c) => [c.key, c.layout.orientation]);
    const tv = await ctx.newPage();
    for (const [k, o] of keys) {
      await tv.setViewportSize(o === 'portrait' ? { width: 540, height: 960 } : { width: 1280, height: 720 });
      await tv.goto(`${t.base}/d/${room}?view=board&tpl=${k}&preview=1`);
      await tv.waitForTimeout(700);
      await tv.screenshot({ path: `${S}/tpl-${k}.png` });
    }
  }
  await ctx.close();
}
