// STATUS U3 + U4: the room walk-in shows the schedule as
// "hh:mm  Title - Speaker" (24 h, no duration) and a Current/Next block
// with Start Time, Duration and Speaker (an ftl dl.props of key/value).
export const name = 'room walk-in schedule and current/next format';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent(['Stark', 'Banner']);
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: [
      { label: 'Keynote', durationMS: 1_800_000, speaker: 'Ada Lovelace' },
      { label: 'Coffee', durationMS: 900_000, kind: 'break' },
      { label: 'Panel', durationMS: 4_500_000, speaker: 'Grace Hopper' },
    ], headers: H,
  });
  await ctx.request.post(`${t.base}/api/shows/${room}/daystart`, { data: { hhmm: '09:00' }, headers: H });
  const tr = await ctx.request.post(`${t.base}/api/shows/${room}/screens/template`, { data: { name: 'Door', template: 'room' }, headers: H });
  if (!tr.ok()) throw new Error('template: ' + tr.status() + ' ' + await tr.text());
  await ctx.request.post(`${t.base}/api/shows/${room}/cmd/start`, { data: { pos: 1 }, headers: H });
  const link = (await (await ctx.request.post(`${t.base}/api/shows/${room}/screens/link`, { data: { name: 'Door' }, headers: H })).json()).link;

  const tv = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
  await tv.goto(link);
  await tv.waitForSelector('.b-js-sched li', { timeout: 8000 }).catch(async () => { throw new Error('no schedule on ' + tv.url() + ' :: ' + (await tv.content()).slice(0, 300)); });
  await tv.waitForTimeout(1500);
  const lines = await tv.$$eval('.b-js-sched li', (lis) => lis.map((li) => li.textContent.replace(/[\s\u2002]+/g, ' ').trim()));
  t.check(`schedule lines ${JSON.stringify(lines)}`,
    lines[0] === '09:00 Keynote - Ada Lovelace' && lines[1] === '09:30 Coffee' && lines[2] === '09:45 Panel - Grace Hopper');
  const now = (await tv.textContent('.b-js-nn-now')).replace(/\s+/g, ' ').trim();
  const next = (await tv.textContent('.b-js-nn-next')).replace(/\s+/g, ' ').trim();
  t.check(`current: "${now}"`, now === 'Current Session: Keynote Start Time 09:00 Duration 30 min Speaker Ada Lovelace');
  t.check(`next: "${next}"`, next === 'Next Session: Coffee Start Time 09:30 Duration 15 min');
  if (process.env.SHOTS) await tv.screenshot({ path: `${process.env.SHOTS}/walkin-room.png` });
  const title = await tv.textContent('.b-js-showtitle');
  t.check(`title "${title}"`, title.trim() === 'Room: Stark');
  await ctx.close();
}
