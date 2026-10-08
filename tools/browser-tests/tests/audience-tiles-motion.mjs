// 2026-10-07 audience notes: the screen tiles are painted in place, so
// nothing jumps. Word cloud: plain words (no badge), rotated within ±90°,
// a word keeps its element and spot when another arrives. Wall: the
// spotlight card goes to the top, an answered card turns green and leaves.
export const name = 'audience screen tiles animate in place';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  // Each submission from its own phone (one phone is rate-limited).
  const ask = async (item, text) => {
    const phone = await t.browser.newContext();
    await phone.request.fetch(`${t.base}/api/audience/${room}/ask`, { method: 'POST', data: { item, text }, headers: H });
    await phone.close();
  };

  await api('POST', `/api/shows/${room}/screens/template`, { name: 'Hall', template: 'main' });
  const link = (await (await api('POST', `/api/shows/${room}/screens/link`, { name: 'Hall' })).json()).link;
  const tv = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
  const errs = [];
  tv.on('pageerror', (e) => errs.push(e.message));
  await tv.goto(link);

  const cloud = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'wordcloud', question: 'One word', autoApprove: true })).json();
  await api('POST', `/api/shows/${room}/polls/${cloud.id}/show`, { target: 'audience', on: true });
  await ask(cloud.id, 'alpha');
  await tv.waitForSelector('.b-cloud-word', { timeout: 8000 }).catch(() => {});
  await tv.waitForTimeout(1800);
  const before = await tv.$eval('.b-cloud-word', (w) => { w.__mark = 1; return w.style.transform; }).catch(() => '');
  for (const w of ['beta', 'gamma', 'delta']) await ask(cloud.id, w);
  await tv.waitForFunction(() => document.querySelectorAll('.b-cloud-word').length === 4, null, { timeout: 8000 }).catch(() => {});
  const words = await tv.$$eval('.b-cloud-word', (ws) => ws.map((w) => ({
    text: w.textContent, mark: w.__mark, t: w.style.transform, badge: w.classList.contains('badge'),
    angle: Number((w.style.transform.match(/rotate\((-?[\d.]+)deg\)/) || [])[1]),
  })));
  t.check(`four words on screen (${words.length})`, words.length === 4);
  t.check('words are not badges', words.every((w) => !w.badge));
  t.check('rotation within ±90°', words.every((w) => Math.abs(w.angle) <= 90));
  t.check('the first word kept its element', words.find((w) => w.text === 'alpha')?.mark === 1);
  // Long words (some turned ±90° by their hash) must fit whole (E2E #22).
  for (const w of ['extraordinarily', 'internationalisation', 'collaboration', 'interoperability']) await ask(cloud.id, w);
  await tv.waitForFunction(() => document.querySelectorAll('.b-cloud-word').length === 8, null, { timeout: 8000 }).catch(() => {});
  const boxes = async () => tv.$$eval('.b-cloud-word', (ws) => ws.map((w) => { const r = w.getBoundingClientRect(); return { l: r.left, r: r.right, t: r.top, b: r.bottom, tf: w.style.transform }; }));
  // At rest = two looks 600 ms apart show the same transforms (up to 20 s).
  let settled = await boxes();
  let rested = false;
  for (let i = 0; i < 40 && !rested; i++) {
    await tv.waitForTimeout(600);
    const now = await boxes();
    rested = JSON.stringify(now.map((b) => b.tf)) === JSON.stringify(settled.map((b) => b.tf));
    settled = now;
  }
  const tile = await tv.$eval('.b-cloud', (c) => { const r = c.getBoundingClientRect(); return { l: r.left, r: r.right, t: r.top, b: r.bottom }; });
  const out = settled.filter((b) => b.l < tile.l - 1 || b.r > tile.r + 1 || b.t < tile.t - 1 || b.b > tile.b + 1).length;
  t.check(`no word is clipped by the tile (${out} outside)`, out === 0);
  let overlaps = 0;
  for (let i = 0; i < settled.length; i++) for (let j = i + 1; j < settled.length; j++) {
    const a = settled[i], b = settled[j];
    if (Math.min(a.r, b.r) - Math.max(a.l, b.l) > -2 && Math.min(a.b, b.b) - Math.max(a.t, b.t) > -2) overlaps++; // touching counts
  }
  t.check(`words never overlap (${overlaps})`, overlaps === 0);
  const cx = (b) => (b.l + b.r) / 2;
  const area = await tv.$eval('.b-cloud', (c) => { const r = c.getBoundingClientRect(); return (r.left + r.right) / 2; });
  t.check('words gather round the centre', Math.abs(settled.reduce((n, b) => n + cx(b), 0) / settled.length - area) < 200);
  t.check('the cloud comes to rest (no endless loop)', rested);
  const pct = await tv.evaluate(async () => (await import('/src/audtiles.js')).percents([1, 1, 1], 3));
  t.check(`poll percentages add up to 100 (${pct})`, pct.reduce((a, b) => a + b, 0) === 100);

  const qa = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'qa', question: 'Ask', autoApprove: true })).json();
  await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target: 'audience', on: true });
  for (const q of ['First?', 'Second?', 'Third?']) await ask(qa.id, q);
  await tv.waitForFunction(() => document.querySelectorAll('.b-qa-item').length === 3, null, { timeout: 8000 }).catch(() => {});
  const kids = (await (await api('GET', `/api/shows/${room}/polls`)).json()).items.find((i) => i.id === qa.id).children;
  const third = kids.find((c) => c.question === 'Third?');
  await api('POST', `/api/shows/${room}/polls/${qa.id}/spotlight`, { entry: third.id });
  await tv.waitForSelector('.b-qa-item.is-spot', { timeout: 8000 }).catch(() => {});
  await tv.waitForTimeout(1200);
  const top = await tv.$$eval('.b-qa-item', (ls) => ls.map((l) => ({ text: l.querySelector('.b-qa-text').textContent, y: l.getBoundingClientRect().top }))
    .sort((a, b) => a.y - b.y)[0]?.text);
  t.check(`spotlight card is on top (${top})`, top === 'Third?');

  const first = kids.find((c) => c.question === 'First?');
  await api('POST', `/api/shows/${room}/polls/${first.id}/moderate`, { status: 'answered' });
  await tv.waitForSelector('.b-qa-item.is-answered', { timeout: 8000 }).catch(() => {});
  t.check('answered card turns green first', !!(await tv.$('.b-qa-item.is-answered')));
  const yOf = async (q) => tv.$$eval('.b-qa-item', (ls, q) => { const l = ls.find((x) => x.querySelector('.b-qa-text').textContent === q); return l ? Math.round(l.getBoundingClientRect().top) : null; }, q);
  const answeredY = await yOf('First?');
  const belowY = await yOf('Second?');
  const aboveFirst = belowY > answeredY;
  await tv.waitForFunction(() => document.querySelectorAll('.b-qa-item').length === 2, null, { timeout: 8000 }).catch(() => {});
  t.check('then it leaves the wall', (await tv.$$('.b-qa-item')).length === 2);
  await tv.waitForTimeout(900);
  t.check('the card below moves up into its place', !aboveFirst || (await yOf('Second?')) === answeredY);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
