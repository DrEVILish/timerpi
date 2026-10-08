// STATUS U17: the phone page and the audience screen tiles use ftl-themes
// components: .panel card, .empty-state wait, .btn options (chosen =
// aria-pressed, ftl's toggle lamp), <progress class="progress"> tallies, .alert verdict and
// spotlight on the phone, .list wall.
export const name = 'phone page and audience tiles use ftl components';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const phoneCtx = await t.browser.newContext({ viewport: { width: 390, height: 780 }, isMobile: true, hasTouch: true });
  const phone = await phoneCtx.newPage();
  const errs = [];
  phone.on('pageerror', (e) => errs.push(e.message));
  await phone.goto(`${t.base}/a/${room}`);
  await phone.waitForSelector('.tp-aud-card');
  t.check('the card is an ftl .panel', await phone.$eval('.tp-aud-card', (c) => c.classList.contains('panel')));
  t.check('waiting is an ftl .empty-state', !!(await phone.$('.empty-state .empty-state-title')));

  const quiz = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'quiz', question: '2+2?', options: ['4', '5'], correct: 0 })).json();
  await api('POST', `/api/shows/${room}/polls/${quiz.id}/show`, { target: 'audience', on: true });
  await phone.waitForSelector('.tp-aud-opt');
  t.check('options are ftl buttons', await phone.$$eval('.tp-aud-opt', (bs) => bs.every((b) => b.classList.contains('btn'))));
  await phone.click('.tp-aud-opt >> nth=0');
  await phone.waitForTimeout(600);
  t.check('the chosen answer is aria-pressed', await phone.$eval('.tp-aud-opt >> nth=0', (b) => b.getAttribute('aria-pressed') === 'true'));
  // E2E: the ftl lamp is a round dot, not a squashed pill (needs a flex button).
  const lamp = await phone.$eval('.tp-aud-opt >> nth=0', (b) => { const s = getComputedStyle(b, '::before'); return [parseFloat(s.width), parseFloat(s.height)]; });
  t.check(`the option lamp is round (${lamp})`, lamp[0] > 4 && Math.abs(lamp[0] - lamp[1]) < 1);

  // Audience screen with the main template, results shown.
  await api('POST', `/api/shows/${room}/screens/template`, { name: 'Hall', template: 'main' });
  const link = (await (await api('POST', `/api/shows/${room}/screens/link`, { name: 'Hall' })).json()).link;
  const tv = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
  await tv.goto(link);
  await api('POST', `/api/shows/${room}/polls/${quiz.id}/results`, { on: true });
  await phone.waitForSelector('progress.progress');
  t.check('phone tallies are ftl <progress class="progress">', (await phone.$$('progress.progress.tp-aud-bar')).length === 2);
  t.check('the quiz verdict is an ftl .alert', await phone.$eval('.tp-aud-verdict', (v) => v.classList.contains('alert') && v.classList.contains('alert-success')));
  await tv.waitForSelector('.b-js-poll progress.progress', { timeout: 8000 }).catch(() => {});
  t.check('screen tallies are ftl <progress class="progress">', (await tv.$$('.b-js-poll progress.progress')).length === 2);

  // Q&A: list wall and spotlight alert, on the phone and the screen.
  const qa = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'qa', question: 'Ask', autoApprove: true })).json();
  await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target: 'audience', on: true });
  await phone.waitForSelector('textarea');
  for (const q of ['First?', 'Second?']) {
    await phone.fill('textarea', q);
    await phone.click('form.tp-aud-ask button[type=submit]');
    await phone.waitForTimeout(3200);
  }
  const items = (await (await api('GET', `/api/shows/${room}/polls`)).json()).items;
  const first = items.find((i) => i.id === qa.id).children.find((c) => c.question === 'First?');
  await api('POST', `/api/shows/${room}/polls/${qa.id}/spotlight`, { entry: first.id });
  await phone.waitForSelector('.tp-aud-spot');
  t.check('phone wall is an ftl .list of .list-item', await phone.$eval('.tp-aud-wall', (l) => l.classList.contains('list') && [...l.children].every((li) => li.classList.contains('list-item'))));
  t.check('phone spotlight is an ftl .alert', await phone.$eval('.tp-aud-spot', (s) => s.classList.contains('alert')));
  await tv.waitForSelector('.b-qa-item.is-spot', { timeout: 8000 }).catch(() => {});
  t.check('screen spotlight is a card of the wall', await tv.$eval('.b-qa-item.is-spot', (s) => s.classList.contains('list-item')).catch(() => false));
  t.check('screen wall is an ftl .list', await tv.$eval('.b-qa-wall', (l) => l.classList.contains('list')).catch(() => false));
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
