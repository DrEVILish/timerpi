// E2E #19/#20/#21: idle tiles say something; the schedule tile lists from
// the running session on and never cuts a row; the plain views fit their
// digits on a phone; the clock view note follows the room.
export const name = 'display screens: empty states, schedule window, plain views fit';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const post = (path, data) => ctx.request.post(`${t.base}/api/shows/${room}${path}`, { data, headers: H });
  const linkOf = async (name) => (await (await post('/screens/link', { name })).json()).link;

  // A room with no sessions, presenter layout: one tile says so, calmly;
  // no tile repeats "Finished for today" (review2 N12), and the empty
  // messages tile paints no band and gives its room away (review2 N6).
  await post('/screens/template', { name: 'DSM', template: 'dsm' });
  const tvCtx = await t.browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const tv = await tvCtx.newPage();
  await tv.goto(await linkOf('DSM'));
  await tv.waitForSelector('.b-w-poll[hidden]', { timeout: 8000 }).catch(() => {});
  await tv.waitForTimeout(800);
  const says = await tv.$$eval('.b-widget:not([hidden])', (ws) => ws.map((w) => w.querySelector('.b-w-body').innerText.trim()));
  t.check(`one tile says "No sessions yet" (${JSON.stringify(says)})`, says.filter((x) => x.includes('No sessions yet')).length === 1 && !says.includes('NEXT'));
  t.check('no tile says "Finished for today"', !says.some((x) => /Finished for today/.test(x)));
  const msgs = await tv.evaluate(() => ({ hidden: document.querySelector('.b-w-messages').hidden, band: !!document.querySelector('.b-w-messages .lower-third:not(:empty)'), text: document.querySelector('.b-w-messages').innerText.trim() }));
  t.check(`no messages: no band, no text, tile given away (${JSON.stringify(msgs)})`, msgs.hidden && !msgs.band && !msgs.text);
  // Nothing on air for the presenter: no placeholder; the countdown takes the room (review #11).
  const room2 = await tv.evaluate(() => ({ item: document.querySelector('.b-w-poll').hidden, cd: getComputedStyle(document.querySelector('.b-w-countdown')).gridColumnEnd }));
  t.check(`empty presenter item gives its room to the countdown (${JSON.stringify(room2)})`, room2.item && /13|span 12/.test(room2.cd));

  // Schedule tile: 14 cues, start the 4th — the list starts there, rows fit whole.
  const cues = Array.from({ length: 14 }, (_, i) => ({ label: `Session ${i + 1}`, durationMS: 1_800_000 }));
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, { data: cues, headers: H });
  await post('/daystart', { hhmm: '09:00' });
  await post('/cmd/start', { pos: 4 });
  await post('/screens/template', { name: 'Clock', template: 'clockroom' });
  await tv.setViewportSize({ width: 960, height: 540 }); // too small for all 11: some hide
  await tv.goto(await linkOf('Clock'));
  await tv.waitForSelector('.b-js-sched li[data-pos]', { timeout: 8000 }).catch(() => {});
  await tv.waitForTimeout(1200);
  const sched = await tv.$eval('.b-js-sched', (ul) => {
    const box = ul.getBoundingClientRect();
    const shown = [...ul.children].filter((li) => !li.hidden);
    return { first: shown[0]?.textContent.replace(/\s+/g, ' ').trim(), n: shown.length, cut: shown.filter((li) => li.getBoundingClientRect().bottom > box.bottom + 1).length, hidden: ul.children.length - shown.length };
  });
  t.check(`schedule starts at the running session ("${sched.first}")`, /Session 4$/.test(sched.first || ''));
  t.check(`rows fit whole (${sched.n} shown, ${sched.hidden} hidden, ${sched.cut} cut)`, sched.cut === 0 && sched.n >= 2 && sched.hidden > 0);
  await tvCtx.close();

  // Plain views on a phone: nothing wider than the screen; clock note follows the room.
  const phone = await (await t.browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  for (const view of ['', '?view=clock', '?view=next', '?view=daysheet']) {
    await phone.goto(`${t.base}/d/${room}${view}`);
    await phone.waitForTimeout(900);
    const over = await phone.evaluate(() => {
      const big = document.querySelector('.tp-display-center .tp-clock, .tp-vclock');
      const r = big?.getBoundingClientRect();
      return { page: document.documentElement.scrollWidth > innerWidth + 1, big: !!r && (r.left < -1 || r.right > innerWidth + 1) };
    });
    t.check(`phone ${view || 'stage'}: fits the width`, !over.page && !over.big);
    if (view === '?view=clock') {
      const note = (await phone.textContent('#d-vclock-note')).trim();
      t.check(`clock note follows the room ("${note}")`, note === 'Now: Session 4');
    }
  }
  await ctx.close();
}
