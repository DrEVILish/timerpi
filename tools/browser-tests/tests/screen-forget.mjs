// STATUS U12 + U44: "Forget" is the theme's close button (ftl .btn-close),
// red, in the top-right corner of each screen card; it asks first, then
// removes (releases) the screen.
export const name = 'Forget is a red X in the screen card corner';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/config`, { data: { name: 'Old TV', theme: '', boardId: 0, room: '' }, headers: H });
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/screens/${room}`);
  const x = await pg.waitForSelector('button[aria-label="Forget screen Old TV"]');
  const card = await (await x.evaluateHandle((b) => b.closest('.tp-scr'))).asElement().boundingBox();
  const bb = await x.boundingBox();
  t.check('the X sits in the card\'s top-right corner', bb.x + bb.width > card.x + card.width - 24 && bb.y < card.y + 24);
  t.check('the X is ftl\'s .btn-close', await x.evaluate((b) => b.classList.contains('btn-close') && getComputedStyle(b, '::before').content.includes('×')));
  const red = await x.evaluate((b) => {
    const probe = document.createElement('span');
    probe.style.color = 'var(--danger-text, var(--danger))';
    b.parentElement.appendChild(probe);
    const want = getComputedStyle(probe).color;
    probe.remove();
    return getComputedStyle(b).color === want;
  });
  t.check('the X is red (danger)', red);
  t.check('no separate "Forget" text button remains', (await pg.$$('.tp-scr button')).length > 0 &&
    !(await pg.$$eval('.tp-scr button', (bs) => bs.some((b) => b.textContent.trim() === 'Forget'))));
  await x.click();
  await pg.click('.tp-dlg-actions .btn-danger');
  await pg.waitForTimeout(1200);
  const list = await (await ctx.request.get(`${t.base}/api/shows/${room}/screens`)).json();
  t.check('the screen is gone after confirming', !list.screens.some((s) => s.name === 'Old TV'));
  await ctx.close();
}
