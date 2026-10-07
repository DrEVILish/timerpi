// STATUS U18 + U17: the Audience tab is a table (one row per item) built
// from ftl-themes parts: Presenter / Audience / Results Show/Hide buttons,
// Type, Title, Auto Approve switch, clear + edit + delete icon buttons.
export const name = 'Audience tab is a table of items with Show/Hide buttons';

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
  t.check(`column headers: ${heads.join(' | ')}`, heads.join('|') === 'Type|Title / Question|Presenter|Audience|Results|Auto Approve|Actions');
  t.check('one row per item', (await pg.$$('.tp-aud-row')).length === 2);
  t.check('show toggles are two-state buttons', (await pg.$$('.tp-aud-row button.tp-aud-showbtn[aria-pressed]')).length === 5);
  t.check('Auto Approve is an ftl .switch', (await pg.$$('.tp-aud-row .switch input[type=checkbox]')).length === 1);

  const row = (q) => pg.locator('.tp-aud-row', { has: pg.locator('.tp-aud-title', { hasText: q }) });
  t.check('Results is locked until the item is shown', await row('2+2?').locator('button[aria-label^="Show: results"]').isDisabled());
  const aud = row('2+2?').locator('button[aria-label$="to the audience"]');
  t.check('hidden item reads "Show"', (await aud.textContent()).trim() === 'Show');
  await aud.click();
  await pg.waitForTimeout(900);
  t.check('Show puts it on air', (await item(poll.id)).toAudience === true);
  t.check('the button now reads "Hide", pressed', (await aud.textContent()).trim() === 'Hide' && (await aud.getAttribute('aria-pressed')) === 'true');
  await row('2+2?').locator('button[aria-label^="Show: results"]').click();
  await pg.waitForTimeout(900);
  t.check('Results button shows results', (await item(poll.id)).state === 'results');
  const detail = pg.locator(`.tp-aud-detail[data-id="${poll.id}"]`);
  t.check('the detail row starts closed', !(await detail.isVisible()));
  await row('2+2?').locator('.table-toggle').click();
  t.check('one click on the title opens the tally', await detail.locator('progress.progress').first().isVisible());
  t.check('the title says it is expanded', (await row('2+2?').locator('.table-toggle').getAttribute('aria-expanded')) === 'true');
  await pg.waitForTimeout(1200); // live re-renders keep it open
  t.check('it stays open through live updates', await detail.isVisible());
  await row('2+2?').locator('td').first().click();
  t.check('a click elsewhere on the row closes it', !(await detail.isVisible()));
  await row('2+2?').locator('td').first().click();
  t.check('and opens it again', await detail.isVisible());
  await row('2+2?').locator('button[aria-label$="on the presenter screen"]').click();
  await pg.waitForTimeout(900);
  t.check('Presenter button', (await item(poll.id)).toPresenter === true);

  // Run and Screens carry the same tab badge, so the tabs never shift.
  const tabX = async () => pg.$eval('.tp-room-tabs .tab:last-child', (e) => Math.round(e.getBoundingClientRect().left));
  const runX = await tabX();
  await pg.goto(`${t.base}/screens/${room}`);
  await pg.waitForTimeout(1200);
  t.check('Screens tab sits in the same place on Run and Screens', (await tabX()) === runX);
  await pg.goto(`${t.base}/c/${room}#audience`);
  await pg.waitForSelector('.tp-aud-table');

  if (process.env.SHOTS) await pg.screenshot({ path: `${process.env.SHOTS}/audience-table.png` });
  await row('Ask the panel').locator('label.switch:has(input[aria-label^="Auto Approve"])').click();
  await pg.waitForTimeout(900);
  const qaNow = await item(qa.id);
  t.check('Auto Approve switch (RS14: question kept)', qaNow.autoApprove === true && qaNow.question === 'Ask the panel');
  t.check('quiz answer survived the other edits (RS14)', (await item(poll.id)).correct === 0);

  t.check('clear + edit + delete are icon buttons with names',
    !!(await pg.$('.tp-aud-row button.btn-icon[aria-label="Clear the responses to \\"Ask the panel\\""]')) &&
    !!(await pg.$('.tp-aud-row button.btn-icon[aria-label="Edit \\"Ask the panel\\""]')) &&
    !!(await pg.$('.tp-aud-row button.btn-icon.btn-danger[aria-label="Delete \\"Ask the panel\\""]')));
  await pg.click('.tp-aud-row button[aria-label="Delete \\"Ask the panel\\""]');
  await pg.click('dialog.modal .modal-footer .btn-danger');
  await pg.waitForTimeout(900);
  t.check('delete removes the row', (await pg.$$('.tp-aud-row')).length === 1);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
