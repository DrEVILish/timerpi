// UX review audience #3 #4 #6 #7 #10 #13 #15: on air is separate from the
// voting phase (switching items keeps shown results; only Clear reopens),
// phones show count and %, a plain-text question and a footer that matches
// the state; Q&A moderation can be undone and restored; word clouds have
// Approve all and an open list of removable chips.
export const name = 'audience: results survive going off air, undo/restore, bulk approve, phone results';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const mk = async (data) => (await api('POST', `/api/shows/${room}/polls`, data)).json();
  const items = async () => (await (await api('GET', `/api/shows/${room}/polls`)).json()).items;
  const poll = await mk({ kind: 'poll', question: 'Lunch?', options: ['Pizza', 'Soup'] });
  const qa = await mk({ kind: 'qa', question: 'Ask the panel' });
  const wc = await mk({ kind: 'wordcloud', question: 'One word' });

  const phoneCtx = await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
  const phone = await phoneCtx.newPage();
  const errs = [];
  phone.on('pageerror', (e) => errs.push(e.message));
  await phone.goto(`${t.base}/a/${room}`);
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'audience', on: true });
  await phone.waitForSelector('.tp-aud-opt');
  const q = await phone.$eval('.tp-aud-q', (e) => [e.tagName, getComputedStyle(e).textTransform]);
  t.check(`the phone question is plain content, not a heading (${q})`, q[0] === 'P' && q[1] === 'none');
  await phone.click('.tp-aud-opt >> nth=1');
  await phone.waitForTimeout(500);
  await api('POST', `/api/shows/${room}/polls/${poll.id}/results`, { on: true });
  await phone.waitForSelector('.tp-aud-res');
  const res = await phone.$$eval('.tp-aud-res-head .mono', (n) => n.map((e) => e.textContent));
  t.check(`phone results show count and % (${res})`, res[0] === '0 · 0%' && res[1] === '1 · 100%');
  t.check('the phone footer is the link state, not stale vote feedback', (await phone.textContent('#tp-aud-note')) === 'Connected');
  const off = await api('POST', `/api/shows/${room}/polls/${poll.id}/results`, { on: false });
  t.check(`results cannot be hidden again (${off.status()})`, off.status() === 400);

  // Switch the audience to the Q&A, then back to the poll.
  await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target: 'audience', on: true });
  await phone.waitForSelector('textarea');
  t.check('the poll kept its results off air', (await items()).find((i) => i.id === poll.id).state === 'results');
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'audience', on: true });
  await phone.waitForSelector('.tp-aud-res');
  t.check('showing it again shows results, no vote buttons', (await phone.$$('.tp-aud-opt')).length === 0);
  await api('POST', `/api/shows/${room}/polls/${poll.id}/reset`, {});
  await phone.waitForSelector('.tp-aud-opt');
  t.check('only Clear responses reopens voting', (await items()).find((i) => i.id === poll.id).state === 'open');

  // Q&A moderation with undo and restore, in the On air card.
  await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target: 'audience', on: true });
  for (const [i, text] of ['First?', 'Second?', 'Third?'].entries()) {
    const c = await t.browser.newContext();
    await c.request.post(`${t.base}/api/audience/${room}/ask`, { data: { item: qa.id, text }, headers: { Origin: t.base, 'X-Test': String(i) } });
    await c.close();
  }
  const pg = await ctx.newPage();
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/c/${room}#audience`);
  const card = pg.locator('#tp-aud-onair .tp-air-card[data-target="audience"]');
  await card.getByRole('button', { name: 'Approve all (3)' }).click();
  await pg.waitForTimeout(900);
  t.check('Approve all approves every pending question', (await items()).find((i) => i.id === qa.id).children.every((c) => c.state === 'open'));
  const entry = (text) => card.locator('.tp-entry', { hasText: text });
  await entry('First?').getByRole('button', { name: 'Answered' }).click();
  await pg.locator('.toast button', { hasText: 'Undo' }).click();
  await pg.waitForTimeout(900);
  t.check('Undo takes back Answered', (await items()).find((i) => i.id === qa.id).children.find((c) => c.question === 'First?').state === 'open');
  await entry('Second?').getByRole('button', { name: 'Answered' }).click();
  await pg.waitForTimeout(900);
  await entry('Second?').getByRole('button', { name: 'Reopen' }).click();
  await pg.waitForTimeout(900);
  t.check('Reopen takes an answered question back', (await items()).find((i) => i.id === qa.id).children.find((c) => c.question === 'Second?').state === 'open');
  await entry('Third?').getByRole('button', { name: 'Dismiss' }).click();
  await pg.waitForTimeout(900);
  await card.locator('summary', { hasText: 'Dismissed (1)' }).click();
  await card.locator('.tp-entry', { hasText: 'Third?' }).getByRole('button', { name: 'Restore' }).click();
  await pg.waitForTimeout(900);
  t.check('Restore brings a dismissed question back', (await items()).find((i) => i.id === qa.id).children.find((c) => c.question === 'Third?').state === 'open');

  // Word cloud: Approve all, the cloud list is open, chips remove on tap.
  await api('POST', `/api/shows/${room}/polls/${wc.id}/show`, { target: 'audience', on: true });
  for (const w of ['coffee', 'Coffee', 'tea', 'jam']) {
    const c = await t.browser.newContext();
    await c.request.post(`${t.base}/api/audience/${room}/ask`, { data: { item: wc.id, text: w }, headers: { Origin: t.base } });
    await c.close();
  }
  await pg.waitForTimeout(2500);
  await card.getByRole('button', { name: /^Approve all/ }).click();
  await pg.waitForTimeout(1200);
  t.check('the cloud list is open with word chips', await card.locator('details:has(summary:has-text("In the cloud")) .tag').first().isVisible());
  const coffee = card.locator('.tag', { hasText: 'coffee' });
  t.check(`identical words show their count (${await coffee.textContent()})`, (await coffee.textContent()).includes('×2'));
  await card.getByRole('button', { name: 'Remove tea' }).click();
  await pg.waitForTimeout(900);
  const words = (await items()).find((i) => i.id === wc.id).children;
  t.check('a chip × removes the word', words.find((w) => w.question === 'tea').state === 'dismissed' && words.find((w) => w.question === 'jam').state === 'open');
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await phoneCtx.close();
  await ctx.close();
}
