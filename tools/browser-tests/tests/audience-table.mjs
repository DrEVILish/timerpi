// STATUS U18 + U17: the Audience tab is a table (one row per item) built
// from ftl-themes parts: Presenter / Audience / Results switches, Type,
// Title, Approve automatically, edit + delete icon buttons.
export const name = 'Audience tab is a table of items with switches';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const poll = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'quiz', question: '2+2?', options: ['4', '5'], correct: 0 })).json();
  const qa = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'qa', question: 'Ask the panel' })).json();
  const item = async (id) => (await (await api('GET', `/api/shows/${room}/polls`)).json()).items.find((i) => i.id === id);

  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/c/${room}#audience`);
  await pg.waitForSelector('.tp-aud-table');
  const heads = await pg.$$eval('.tp-aud-table thead th', (ths) => ths.map((th) => th.textContent.trim()));
  t.check(`column headers: ${heads.join(' | ')}`, heads.join('|') === 'Presenter|Audience|Results|Type|Title / Question|Approve automatically|Actions');
  t.check('one row per item', (await pg.$$('.tp-aud-row')).length === 2);
  t.check('switches are ftl .switch controls', (await pg.$$('.tp-aud-row .switch input[type=checkbox]')).length >= 5);

  const row = (q) => pg.locator('.tp-aud-row', { has: pg.locator('.tp-aud-q', { hasText: q }) });
  t.check('Results is locked until the item is shown', await row('2+2?').locator('input[aria-label^="Show results"]').isDisabled());
  await row('2+2?').locator('label.switch:has(input[aria-label^="Show \\"2+2?\\" to the audience"])').click();
  await pg.waitForTimeout(900);
  t.check('Audience switch puts it on air', (await item(poll.id)).toAudience === true);
  await row('2+2?').locator('label.switch:has(input[aria-label^="Show results"])').click();
  await pg.waitForTimeout(900);
  t.check('Results switch shows results', (await item(poll.id)).state === 'results');
  t.check('a detail row shows the tally under it', !!(await pg.$(`.tp-aud-detail[data-id="${poll.id}"] progress.progress`)));
  await row('2+2?').locator('label.switch:has(input[aria-label^="Show \\"2+2?\\" to the presenter"])').click();
  await pg.waitForTimeout(900);
  t.check('Presenter switch', (await item(poll.id)).toPresenter === true);

  if (process.env.SHOTS) await pg.screenshot({ path: `${process.env.SHOTS}/audience-table.png` });
  await row('Ask the panel').locator('label.switch:has(input[aria-label^="Approve submissions"])').click();
  await pg.waitForTimeout(900);
  const qaNow = await item(qa.id);
  t.check('Approve automatically switch (RS14: question kept)', qaNow.autoApprove === true && qaNow.question === 'Ask the panel');
  t.check('quiz answer survived the other edits (RS14)', (await item(poll.id)).correct === 0);

  t.check('edit + delete are icon buttons with names',
    !!(await pg.$('.tp-aud-row button.btn-icon[aria-label="Edit \\"Ask the panel\\""]')) &&
    !!(await pg.$('.tp-aud-row button.btn-icon.btn-danger[aria-label="Delete \\"Ask the panel\\""]')));
  await pg.click('.tp-aud-row button[aria-label="Delete \\"Ask the panel\\""]');
  await pg.click('.tp-dlg-actions .btn-danger');
  await pg.waitForTimeout(900);
  t.check('delete removes the row', (await pg.$$('.tp-aud-row')).length === 1);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
