// Screens page (owner 2026-10-07): you pick a layout, not a shape. Each
// display type lists its four built-ins once; Mounted picks the version
// and never changes the layout; changing the type moves a built-in to the
// new type's layouts, and the card redraws even though the select keeps
// focus.
export const name = 'screen card: one layout list per type; Mounted picks the version';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent(['Stark']);
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/template`, { data: { name: 'Door', template: 'room' }, headers: H });
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/screens/${room}`);
  const sel = (what) => pg.locator(`select[aria-label="${what} of Door"]`);
  const builtins = async () => (await sel('Layout').locator('option').allTextContents()).filter((x) => x.startsWith('[built-in]'));
  await sel('Display type').waitFor();
  const walkin = await builtins();
  t.check(`walk-in lists its four layouts once (${walkin.join(', ')})`,
    walkin.join('|') === '[built-in] Event walk-in|[built-in] Room walk-in|[built-in] Room lobby|[built-in] Clock');

  await sel('Rotation').selectOption('90');
  await pg.waitForFunction(() => document.querySelector('.tp-scr-prev')?.classList.contains('is-portrait'), null, { timeout: 5000 }).catch(() => {});
  t.check('mounted portrait: same layout, preview shows the portrait version',
    await sel('Layout').inputValue() === 't:room' && await pg.$eval('.tp-scr-prev', (e) => e.classList.contains('is-portrait')));
  t.check('the layout list is unchanged', (await builtins()).join('|') === walkin.join('|'));

  // Change the type with the keyboard on the focused select, as a person
  // does: focus stays on it, and the card must still redraw.
  const want = { audience: ['Audience main', 'Q&A wall', 'Holding slide', 'Break'], walkin: ['Event walk-in', 'Room walk-in', 'Room lobby', 'Clock'], presenter: ['Presenter (DSM)', 'Full timer', 'Countdown only', 'Speaker support'] };
  await sel('Display type').focus();
  for (let i = 0; i < 3; i++) {
    await pg.keyboard.press(i === 0 ? 'ArrowUp' : 'ArrowDown');
    const kind = await sel('Display type').inputValue();
    if (!want[kind]) continue;
    const expect = want[kind].map((n) => `[built-in] ${n}`).join('|');
    await pg.waitForFunction(([e]) => [...document.querySelectorAll('select[aria-label="Layout of Door"] option')].map((o) => o.text).filter((x) => x.startsWith('[built-in]')).join('|') === e, [expect], { timeout: 5000 }).catch(() => {});
    t.check(`type ${kind} lists its four layouts (${(await builtins()).join(', ')})`, (await builtins()).join('|') === expect);
    await sel('Display type').focus();
  }
  t.check('Mounted kept through the type changes', await sel('Rotation').inputValue() === '90');
  await ctx.close();
}
