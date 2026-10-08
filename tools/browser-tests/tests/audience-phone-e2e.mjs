// E2E audience findings on the phone page: Clear forgets this phone's vote
// and upvotes (#11), feedback stays readable, busy (429) votes retry, the
// word cloud lists the phone's own pending words, the footer, spotlight
// label and upvote badges fit at 390 px, and the upvoted badge looks it.
export const name = 'audience phone: clear forgets, feedback, busy retry, pending, layout';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const phone = await (await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })).newPage();
  const errs = [];
  phone.on('pageerror', (e) => errs.push(e.message));
  // No device cookie just for looking (E2E #2).
  const page = await phone.goto(`${t.base}/a/${room}`);
  t.check('page view sets no device cookie', !(page.headers()['set-cookie'] || '').includes('tp_aud'));

  // Busy vote: one 429 then through; the footer says it retries.
  const poll = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'poll', question: 'Lunch?', options: ['Pizza', 'Soup'] })).json();
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'audience', on: true });
  await phone.waitForSelector('.tp-aud-opt');
  let busy = 1;
  await phone.route('**/vote', (r) => (busy-- > 0
    ? r.fulfill({ status: 429, headers: { 'retry-after': '1' }, contentType: 'application/json', body: '{"ok":false,"error":"room is busy — try again in a moment"}' })
    : r.continue()));
  await phone.click('.tp-aud-opt >> nth=0');
  await phone.waitForTimeout(300);
  const mid = await phone.textContent('#tp-aud-note');
  t.check(`busy vote says it retries ("${mid}")`, /retrying/i.test(mid));
  await phone.waitForTimeout(1500);
  await phone.unroute('**/vote');
  const after = await (await api('GET', `/api/shows/${room}/polls`)).json();
  t.check('the retried vote counted', after.items.find((i) => i.id === poll.id).total === 1);
  // Feedback is still readable a moment later although live frames keep coming.
  await phone.waitForTimeout(1200);
  const fb = await phone.textContent('#tp-aud-note');
  t.check(`vote feedback stays visible ("${fb}")`, fb.startsWith('Vote received'));
  const foot = await phone.$eval('#tp-aud-note', (n) => [n.getBoundingClientRect().right, document.getElementById('tp-aud-card').getBoundingClientRect().right]);
  t.check(`footer note fits the card at 390px (${foot})`, foot[0] <= foot[1] + 0.5);

  // #11: Clear responses → the phone forgets its vote.
  await api('POST', `/api/shows/${room}/polls/${poll.id}/reset`, {});
  await phone.waitForFunction(() => document.querySelectorAll('.tp-aud-opt[aria-pressed="true"]').length === 0, null, { timeout: 5000 }).catch(() => {});
  t.check('after Clear no answer is marked chosen', (await phone.$$('.tp-aud-opt[aria-pressed="true"]')).length === 0);
  t.check('after Clear the hint asks for a vote', (await phone.textContent('.tp-aud-hint')).includes('Tap an answer'));

  // Q&A: upvote looks upvoted; Clear forgets it; spotlight label on one line.
  const qa = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'qa', question: 'Ask', autoApprove: true })).json();
  await api('POST', `/api/shows/${room}/polls/${poll.id}/hide`, {});
  await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target: 'audience', on: true });
  const other = await (await t.browser.newContext()).newPage();
  await other.goto(`${t.base}/a/${room}`);
  for (const q of ['First question?', 'A second, much longer question that wraps over a few lines on a phone?']) {
    await other.waitForSelector('textarea');
    await other.fill('textarea', q);
    await other.click('form.tp-aud-ask button[type=submit]');
    await other.waitForTimeout(3200);
  }
  await phone.waitForSelector('.tp-aud-up');
  await phone.click('.tp-aud-up >> nth=0');
  await phone.waitForTimeout(500);
  const up = await phone.$eval('.tp-aud-up[aria-pressed="true"]', (b) => ({ active: b.classList.contains('is-active'), op: getComputedStyle(b).opacity }));
  t.check(`the upvoted badge is the active chip (${JSON.stringify(up)})`, up.active && up.op === '1');
  const hs = await phone.$$eval('.tp-aud-up', (bs) => bs.map((b) => Math.round(b.getBoundingClientRect().height)));
  t.check(`upvote badges are one size (${hs})`, new Set(hs).size === 1);
  await api('POST', `/api/shows/${room}/polls/${qa.id}/reset`, {});
  await other.fill('textarea', 'After the clear?');
  await other.click('form.tp-aud-ask button[type=submit]');
  await phone.waitForFunction(() => document.querySelector('.tp-aud-wall')?.textContent.includes('After the clear?'), null, { timeout: 5000 }).catch(() => {});
  t.check('after Clear the new question is not marked upvoted', (await phone.$$('.tp-aud-up[aria-pressed="true"]')).length === 0);
  const items = (await (await api('GET', `/api/shows/${room}/polls`)).json()).items;
  const qid = items.find((i) => i.id === qa.id).children[0].id;
  await api('POST', `/api/shows/${room}/polls/${qa.id}/spotlight`, { entry: qid });
  await phone.waitForSelector('.tp-aud-spot .label');
  const lh = await phone.$eval('.tp-aud-spot .label', (l) => [l.getBoundingClientRect().height, parseFloat(getComputedStyle(l).lineHeight) || 20]);
  t.check(`"Now answering" is one line (${lh})`, lh[0] < lh[1] * 1.6);

  // Word cloud, Auto Approve off: the sender sees its pending word.
  const cloud = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'wordcloud', question: 'One word' })).json();
  await api('POST', `/api/shows/${room}/polls/${qa.id}/hide`, {});
  await api('POST', `/api/shows/${room}/polls/${cloud.id}/show`, { target: 'audience', on: true });
  await phone.waitForSelector('form.tp-aud-ask input');
  await phone.fill('form.tp-aud-ask input', 'serendipity');
  await phone.click('form.tp-aud-ask button[type=submit]');
  await phone.waitForTimeout(1500);
  t.check('a pending cloud word is listed as waiting', (await phone.textContent('.tp-aud-mine').catch(() => '')).includes('serendipity'));
  const word = (await (await api('GET', `/api/shows/${room}/polls`)).json()).items.find((i) => i.id === cloud.id).children[0];
  await api('POST', `/api/shows/${room}/polls/${word.id}/moderate`, { status: 'approved' });
  await phone.waitForTimeout(1500);
  t.check('approved word leaves "Waiting for review" at once', !(await phone.$('.tp-aud-mine')));
  t.check('no page errors', errs.length === 0, errs.join('; '));
  await ctx.close();
}
