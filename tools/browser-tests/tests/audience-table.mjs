// STATUS U18 + U17, UX review audience simplifications 3 + 4: the Audience
// tab is an On air strip (one card per target with its controls) over a
// library table (ftl .table.is-stacked: cards on phones) with Audience /
// Presenter toggles, Auto Approve and a ⋯ menu (Edit, Clear, Delete).
export const name = 'Audience tab: On air strip over a library table';

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
  t.check(`column headers: ${heads.join(' | ')}`, heads.join('|') === 'Type|Title / Question|Audience|Presenter|Auto Approve|Actions');
  t.check('one row per item', (await pg.$$('.tp-aud-row')).length === 2);
  t.check('show toggles are server-confirmed ftl toggles, none primary',
    (await pg.$$('.tp-aud-row button.tp-aud-showbtn[aria-pressed]')).length === 4 && (await pg.$$('.tp-aud-row .btn-primary')).length === 0);
  t.check('Auto Approve is an ftl .switch', (await pg.$$('.tp-aud-row .switch input[type=checkbox]')).length === 1);
  t.check('both On air cards say off air', (await pg.$$('#tp-aud-onair .tp-air-card.is-empty')).length === 2);

  const row = (q) => pg.locator('.tp-aud-row', { has: pg.locator('.tp-aud-title', { hasText: q }) });
  const aud = row('2+2?').locator('button[aria-label$="to the audience"]');
  t.check('hidden item reads "Show"', (await aud.textContent()).trim() === 'Show');
  await aud.click();
  await pg.waitForTimeout(900);
  t.check('Show puts it on air', (await item(poll.id)).toAudience === true);
  t.check('the button now reads "Hide", pressed', (await aud.textContent()).trim() === 'Hide' && (await aud.getAttribute('aria-pressed')) === 'true');
  const card = pg.locator('#tp-aud-onair .tp-air-card[data-target="audience"]');
  t.check('the On Audience card holds the item', (await card.locator('.tp-air-q').textContent()) === '2+2?');
  const live = card.locator('.tp-live');
  t.check('"Live" is the lit ftl lamp, not error red', await live.locator('.lamp.is-on').count() === 1 &&
    !(await pg.$('#tp-aud-panel .status-error')));
  t.check('the card shows the live tally', await card.locator('progress.progress').first().isVisible());
  await card.getByRole('button', { name: 'Show results' }).click();
  await pg.waitForTimeout(900);
  t.check('Show results shows results', (await item(poll.id)).state === 'results');
  t.check('the card says voting is closed, with no Hide-results button',
    (await card.textContent()).includes('Voting closed') && (await card.getByRole('button', { name: /results/i }).count()) === 0);

  await row('Ask the panel').locator('button[aria-label$="on the presenter screen"]').click();
  await pg.waitForTimeout(900);
  t.check('Presenter toggle', (await item(qa.id)).toPresenter === true);
  t.check('the On Presenter card holds the Q&A', (await pg.locator('#tp-aud-onair .tp-air-card[data-target="presenter"] .tp-air-q').textContent()) === 'Ask the panel');
  await pg.locator('#tp-aud-onair .tp-air-card[data-target="presenter"]').getByRole('button', { name: 'Take off air' }).click();
  await pg.waitForTimeout(900);
  t.check('Take off air takes it off that target', !(await item(qa.id)).toPresenter);

  // Run and Screens carry the same tab badge, so the tabs never shift.
  const tabX = async () => pg.$eval('.tp-room-tabs .tab[href^="/screens/"]', (e) => Math.round(e.getBoundingClientRect().left));
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

  // Clear, Edit and Delete live in the ⋯ menu; Delete is a menu item, not
  // the loudest button in the row.
  t.check('no delete button in the rows', (await pg.$$('.tp-aud-row .btn-danger')).length === 0);
  await row('Ask the panel').getByRole('button', { name: 'More actions for "Ask the panel"' }).click();
  const menu = pg.locator('#tp-aud-menu');
  t.check('the ⋯ menu opens with Edit, Clear, Delete', await menu.isVisible() &&
    (await menu.locator('.dropdown-item').allTextContents()).join('|') === 'Edit…|Clear responses…|Delete…');
  await menu.locator('[data-aud-act="edit"]').click();
  t.check('Edit opens the editor', await pg.locator('#tp-aud-editor').isVisible());
  await pg.click('#tp-aud-editor [data-close].btn-secondary');
  await row('Ask the panel').getByRole('button', { name: 'More actions for "Ask the panel"' }).click();
  await menu.locator('[data-aud-act="delete"]').click();
  await pg.click('dialog.modal[open] .modal-footer .btn-danger');
  await pg.waitForTimeout(900);
  t.check('delete removes the row', (await pg.$$('.tp-aud-row')).length === 1);

  // Phone width: rows become cards (no sideways scroll), toggles reachable.
  await pg.setViewportSize({ width: 390, height: 844 });
  await pg.waitForTimeout(300);
  const fit = await pg.$eval('#tp-aud-items .table-wrap', (w) => [w.scrollWidth, w.clientWidth]);
  t.check(`library fits a phone without sideways scroll (${fit})`, fit[0] <= fit[1] + 1);
  const btn = await pg.$eval('.tp-aud-row button[aria-label$="to the audience"]', (b) => b.getBoundingClientRect().right);
  t.check(`the Audience toggle is on screen (${btn})`, btn <= 390);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
