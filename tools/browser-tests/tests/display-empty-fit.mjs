// E2E #19/#20/#21: idle tiles say something; the schedule tile lists from
// the running session on and never cuts a row; the plain views fit their
// digits on a phone; the clock view note follows the room.
export const name = 'display screens: empty states, schedule window, plain views fit';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const post = (path, data) => ctx.request.post(`${t.base}/api/shows/${room}${path}`, { data, headers: H });
  const linkOf = async (name) => (await (await post('/screens/link', { name })).json()).link;

  // Idle room, presenter layout: nothing is a blank box.
  await post('/screens/template', { name: 'DSM', template: 'dsm' });
  const tvCtx = await t.browser.newContext({ viewport: { width: 1920, height: 1080 } });
  const tv = await tvCtx.newPage();
  await tv.goto(await linkOf('DSM'));
  await tv.waitForSelector('.b-tile-empty', { timeout: 8000 }).catch(() => {});
  await tv.waitForTimeout(800);
  const blank = await tv.$$eval('.b-widget', (ws) => ws.filter((w) => !w.querySelector('.b-w-body').innerText.trim()).map((w) => w.dataset.widget));
  t.check(`idle presenter tiles all say something (blank: ${blank.join(', ') || 'none'})`, blank.length === 0);
  const aud = await tv.textContent('.b-js-poll .b-tile-empty').catch(() => '');
  t.check(`audience tile empty state ("${aud}")`, /appear here/.test(aud));

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
