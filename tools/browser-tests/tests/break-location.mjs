// STATUS U14: add a Break with where it's served; the running order shows
// the place, and the walk-in schedule reads "hh:mm  Coffee — Great Hall".
export const name = 'add a break with a location; it shows on the schedule';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.post(`${t.base}/api/shows/${room}/daystart`, { data: { hhmm: '09:00' }, headers: H });
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/c/${room}`);
  const f = (n) => `[form="tp-add-form"][name="${n}"]`;
  await pg.waitForSelector(f('label'));
  // A session first, then a break, from the running order's add row (U39).
  await pg.fill(f('label'), 'Keynote');
  await pg.fill(f('who'), 'Ada Lovelace');
  await pg.fill(f('mss'), '30');
  await pg.click('#cuelist tfoot button[type=submit]');
  await pg.waitForTimeout(700);
  await pg.selectOption(f('kind'), 'break');
  t.check('Break turns the Speaker box into "Where"',
    (await pg.getAttribute(f('who'), 'placeholder')).startsWith('Where'));
  await pg.fill(f('label'), 'Coffee');
  await pg.fill(f('who'), 'Great Hall');
  await pg.fill(f('mss'), '15');
  await pg.press(f('mss'), 'Enter');
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
