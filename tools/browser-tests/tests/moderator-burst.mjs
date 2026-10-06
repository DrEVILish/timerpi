// BUGLOG RW58: a burst of votes doesn't make the moderator's Audience tab
// re-read the item list on every frame (reads coalesce to one per 1.5 s),
// and an unchanged list isn't rebuilt.
export const name = 'moderator panel coalesces live reads during a vote burst';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const poll = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'poll', question: 'Lunch?', options: ['Pizza', 'Soup'] })).json();
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'audience', on: true });

  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  let reads = 0;
  pg.on('request', (r) => { if (r.method() === 'GET' && /\/api\/shows\/[^/]+\/polls$/.test(r.url())) reads++; });
  await pg.goto(`${t.base}/c/${room}#audience`);
  await pg.waitForSelector('.tp-aud-table');
  await pg.waitForTimeout(2000);
  await pg.$eval('.tp-aud-table', (tbl) => { tbl.__mark = 1; });

  const before = reads;
  const phones = await Promise.all(Array.from({ length: 12 }, () => t.browser.newContext()));
  const start = Date.now();
  await Promise.all(phones.map((p, i) => p.request.post(`${t.base}/api/audience/${room}/vote`, { data: { pollId: poll.id, choice: String(i % 2) }, headers: H })));
  await pg.waitForTimeout(2500);
  const burst = reads - before;
  const secs = (Date.now() - start) / 1000;
  t.check(`12 votes caused ${burst} list reads in ${secs.toFixed(1)} s (≤ 3)`, burst <= 3);

  // Nothing changes now: the next periodic read must not rebuild the table.
  await pg.waitForTimeout(500);
  await pg.$eval('.tp-aud-table', (tbl) => { tbl.__mark = 2; });
  await pg.waitForTimeout(9000);
  t.check('an unchanged list is not re-rendered', await pg.$eval('.tp-aud-table', (tbl) => tbl.__mark === 2));
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  for (const p of phones) await p.close();
  await ctx.close();
}
