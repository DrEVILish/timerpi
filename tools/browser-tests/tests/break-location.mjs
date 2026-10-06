// STATUS U14: add a Break with where it's served; the running order shows
// the place, and the walk-in schedule reads "hh:mm  Coffee — Great Hall".
export const name = 'add a break with a location; it shows on the schedule';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.post(`${t.base}/api/shows/${room}/daystart`, { data: { hhmm: '09:00' }, headers: H });
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('.tp-quick-add');
  // A session first, then a break.
  await pg.fill('.tp-quick-add input[name="label"]', 'Keynote');
  await pg.fill('.tp-quick-add input[name="speaker"]', 'Ada Lovelace');
  await pg.fill('.tp-quick-add input[name="mss"]', '30');
  await pg.click('.tp-quick-add button[type=submit]');
  await pg.waitForTimeout(700);
  await pg.check('.tp-quick-add input[name="kind"][value="break"]');
  t.check('Break shows the "Where" box instead of Speaker',
    await pg.isVisible('.tp-quick-add input[name="location"]') && !(await pg.isVisible('.tp-quick-add input[name="speaker"]')));
  await pg.fill('.tp-quick-add input[name="label"]', 'Coffee');
  await pg.fill('.tp-quick-add input[name="location"]', 'Great Hall');
  await pg.fill('.tp-quick-add input[name="mss"]', '15');
  await pg.click('.tp-quick-add button[type=submit]');
  await pg.waitForTimeout(900);
  const snap = await (await ctx.request.get(`${t.base}/api/shows/${room}`)).json();
  const coffee = snap.cues.find((c) => c.label === 'Coffee');
  t.check(`stored as a break at "Great Hall" (${coffee?.kind}, ${coffee?.location})`, coffee?.kind === 'break' && coffee?.location === 'Great Hall' && !coffee?.speaker);
  const keynote = snap.cues.find((c) => c.label === 'Keynote');
  t.check('the session kept its speaker', keynote?.speaker === 'Ada Lovelace' && keynote?.kind === 'session');
  const row = (await pg.textContent('#cuelist tbody tr:nth-child(2)')).replace(/\s+/g, ' ');
  t.check(`running order shows the place (${row.slice(0, 80)})`, row.includes('Great Hall'));

  await ctx.request.post(`${t.base}/api/shows/${room}/screens/template`, { data: { name: 'Door', template: 'room' }, headers: H });
  const link = (await (await ctx.request.post(`${t.base}/api/shows/${room}/screens/link`, { data: { name: 'Door' }, headers: H })).json()).link;
  const tv = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
  await tv.goto(link);
  await tv.waitForSelector('.b-js-sched li');
  await tv.waitForTimeout(1200);
  const lines = await tv.$$eval('.b-js-sched li', (lis) => lis.map((li) => li.textContent.replace(/[\s ]+/g, ' ').trim()));
  t.check(`walk-in schedule ${JSON.stringify(lines)}`, lines[0] === '09:00 Keynote - Ada Lovelace' && lines[1] === '09:30 Coffee — Great Hall');
  await ctx.close();
}
