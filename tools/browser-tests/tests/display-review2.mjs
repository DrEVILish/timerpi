// Review round 2 (ui-displays): held reads "due now" for next (#7), the
// held cue bar is muted (#13), the stage day bar is ftl .timeline-bar (#3),
// the next title wraps (#4), the Break notice reads only during a break
// (#12), the event walk-in keeps its time column and gives the map's room
// to the schedule (#8), and the READY wordmark reads at size (#14).
export const name = 'display screens: held/next, day bar, wraps, break notice, walk-in, READY mark';

export async function run(t) {
  const { ctx, H, rooms } = await t.newEvent(['Main', 'Side']);
  const [room, side] = rooms;
  const post = (r, path, data) => ctx.request.post(`${t.base}/api/shows/${r}${path}`, { data, headers: H });
  const linkOf = async (r, name) => (await (await post(r, '/screens/link', { name })).json()).link;
  const longNext = 'Coffee and networking with our sponsors in the Great Hall and the lounge';
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, { data: [
    { label: 'Short talk', durationMS: 2000 },
    { label: longNext, kind: 'break', durationMS: 900_000 },
    { label: 'Closing', durationMS: 600_000 },
  ], headers: H });
  await post(room, '/daystart', { hhmm: '09:00' });
  await post(room, '/cmd/start', { pos: 1 });
  const open = async (r, name, template, size = { width: 1920, height: 1080 }) => {
    await post(r, '/screens/template', { name, template });
    const pg = await (await t.browser.newContext({ viewport: size })).newPage();
    await pg.goto(await linkOf(r, name));
    await pg.waitForSelector('.b-widget');
    return pg;
  };
  const stage = await open(room, 'Stage', 'stage');
  await stage.waitForFunction(() => document.querySelector('.b-js-clock')?.dataset.state === 'held', null, { timeout: 10000 }).catch(() => {});
  await stage.waitForTimeout(600);
  const s = await stage.evaluate(() => {
    const prog = document.querySelector('.b-w-progress');
    const bar = document.querySelector('.b-w-dayprogress .timeline-bar');
    const segs = [...(bar?.querySelectorAll('.timeline-seg') || [])];
    const next = document.querySelector('.b-js-nextlabel');
    const lh = parseFloat(getComputedStyle(next).lineHeight);
    return {
      clock: document.querySelector('.b-js-clock').dataset.state,
      nextIn: document.querySelector('.b-js-nextin')?.textContent.trim(),
      progState: prog.dataset.state,
      progFill: getComputedStyle(prog).getPropertyValue('--progress-fill').trim(),
      muted: getComputedStyle(prog).getPropertyValue('--muted').trim(),
      segs: segs.length, brk: segs.filter((x) => x.dataset.kind === 'break').length, active: segs.findIndex((x) => x.classList.contains('is-active')),
      needle: !!bar?.querySelector('.timeline-now'), scale: document.querySelectorAll('.b-w-dayprogress .timeline-scale time').length,
      gauge: !!document.querySelector('.b-w-dayprogress .gauge-linear'),
      nextLines: Math.round(next.getBoundingClientRect().height / lh), nextWrap: getComputedStyle(next).whiteSpace,
    };
  });
  t.check(`held: next reads "due now" (${s.nextIn})`, s.clock === 'held' && s.nextIn === 'due now');
  t.check(`held: the cue bar goes muted (${s.progState} ${s.progFill})`, s.progState === 'held' && !!s.muted && s.progFill === s.muted);
  t.check(`day bar is ftl .timeline-bar with segments, a striped break, the live one, a needle and a scale (${JSON.stringify(s)})`,
    !s.gauge && s.segs === 3 && s.brk === 1 && s.active === 0 && s.needle && s.scale === 2);
  t.check(`a long next title wraps instead of an ellipsis (${s.nextWrap}, ${s.nextLines} lines)`, s.nextWrap === 'normal' && s.nextLines >= 2);
  await stage.context().close();

  // Break layout outside a break: the notice names what is next.
  const brk = await open(room, 'Foyer', 'break');
  await brk.waitForTimeout(800);
  const notice = (await brk.textContent('.b-js-notice')).trim();
  t.check(`break notice outside a break names the next session ("${notice}")`, !/enjoy the break/.test(notice) && notice.startsWith('Next: Coffee'));
  await brk.context().close();

  // Event walk-in: the side room has untimed rows (no day start); every
  // column keeps its time cell, "you are here" stays on one line, and
  // with no map the schedule takes its room.
  await ctx.request.put(`${t.base}/api/shows/${side}/cues`, { data: [{ label: 'Workshop', durationMS: 1_800_000 }, { label: 'Lab', durationMS: 1_800_000 }], headers: H });
  const ev = await open(room, 'Door', 'event');
  await ev.waitForSelector('.b-js-evsched .schedule-item', { timeout: 8000 }).catch(() => {});
  await ev.waitForTimeout(1200);
  const w = await ev.evaluate(() => {
    const items = [...document.querySelectorAll('.b-js-evsched .schedule-item')];
    const here = document.querySelector('.b-room.is-here .b-room-name');
    const after = here && getComputedStyle(here, '::after');
    return {
      items: items.length, timed: items.filter((li) => li.querySelector('time.schedule')).length,
      nowrap: after?.whiteSpace, map: document.querySelector('.b-w-map')?.hidden,
      sched: getComputedStyle(document.querySelector('.b-w-eventschedule')).gridColumnEnd,
    };
  });
  t.check(`event walk-in: every row keeps its time column (${w.timed}/${w.items})`, w.items >= 4 && w.timed === w.items);
  t.check(`"you are here" stays on one line (${w.nowrap})`, w.nowrap === 'nowrap');
  t.check(`no map: the schedule takes its room, not the clock (${JSON.stringify(w)})`, w.map === true && /13|span 12/.test(w.sched));
  await ev.context().close();

  // READY card: the wide wordmark reads at about 12vh, not 50 px.
  const ready = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
  await ready.goto(`${t.base}/d/`);
  await ready.waitForSelector('.splash-art');
  const art = await ready.$eval('.splash-art', (a) => { const r = a.getBoundingClientRect(); return { w: Math.round(r.width), h: Math.round(r.height) }; });
  t.check(`READY wordmark is wide and readable (${JSON.stringify(art)})`, art.h >= 100 && art.w > art.h * 3);
  await ready.context().close();
  await ctx.close();
}
