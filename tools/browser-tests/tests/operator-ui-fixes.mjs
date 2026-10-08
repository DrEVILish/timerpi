// E2E 2026-10-08 fixes (REPORT #3, #13, #14, #24, #25 + minors): durations
// refuse "45:00", the running-order badges update live, row colour shows,
// the cue editor checks alerts and clears a Break's speaker, "Day starts
// now" asks in an ftl modal, messages delete one by one, #audience works on
// hashchange, liquid-glass keeps the page clear of its bar, phones get a
// two-row header, one-line footer and an unclipped transport.
export const name = 'operator UI fixes from the E2E pass';

export async function run(t) {
  const { ctx, H, room, event } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const snap = async () => { const j = await (await api('GET', `/api/shows/${room}`)).json(); return j.snapshot || j; };
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.setViewportSize({ width: 1440, height: 900 });
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tfoot');
  const toastText = () => pg.$$eval('.toast', (ts) => ts.map((x) => x.textContent).join(' | '));

  // #13: "45:00" is 45 hours — refused with the hint; "45m" works.
  await pg.fill('#cuelist tfoot input[name=label]', 'Keynote');
  await pg.fill('#cuelist tfoot input[name=mss]', '45:00');
  await pg.click('#cuelist tfoot button[type=submit]');
  await pg.waitForTimeout(400);
  t.check('"45:00" is refused with the 45-hours hint', /45:00 is 45 hours — use 0:45 for 45 minutes or 45m/.test(await toastText()));
  t.check('no cue was added', (await snap()).cues.length === 0);
  await pg.fill('#cuelist tfoot input[name=mss]', '45m');
  await pg.click('#cuelist tfoot button[type=submit]');
  await pg.waitForSelector('#cuelist tbody tr[data-pos="1"]');
  t.check('"45m" adds a 45-minute cue', (await snap()).cues[0]?.durationMS === 2_700_000);

  // #14: header badges follow the cue list without a reload.
  await pg.fill('#cuelist tfoot input[name=label]', 'Panel');
  await pg.fill('#cuelist tfoot input[name=mss]', '1:30');
  await pg.click('#cuelist tfoot button[type=submit]');
  await pg.waitForSelector('#cuelist tbody tr[data-pos="2"]');
  await pg.waitForTimeout(300);
  t.check(`cue count badge is live (${await pg.textContent('#tp-cue-count')})`, (await pg.textContent('#tp-cue-count')).trim() === '2 cues');
  t.check(`total badge is live (${await pg.textContent('#tp-cue-total')})`, (await pg.textContent('#tp-cue-total')).trim() === 'Total 2:15:00');

  // Cue editor: an alert longer than the cue is refused; a Break drops its speaker.
  await pg.click('#cuelist tbody tr[data-pos="1"] [data-insp]');
  await pg.waitForSelector('#tp-inspector[open]');
  await pg.fill('#tp-insp-speaker', 'Ada');
  await pg.fill('#tp-insp-alert1', '50:00');
  await pg.click('#tp-insp-save');
  t.check('an alert longer than the cue is refused', /longer than the cue/.test(await pg.textContent('#tp-insp-error')) && await pg.$('#tp-inspector[open]'));
  await pg.fill('#tp-insp-alert1', '5:00');
  await pg.click('#tp-insp-save');
  await pg.waitForTimeout(500);
  t.check('the speaker saves on a session', (await snap()).cues[0].speaker === 'Ada');
  await pg.click('#cuelist tbody tr[data-pos="1"] [data-insp]');
  await pg.selectOption('#tp-insp-kind', 'break');
  t.check('Speaker hides for a Break', await pg.$eval('#tp-insp-speaker', (e) => e.closest('.field').hidden));
  await pg.click('#tp-inspector .tp-insp-rowcolor [role=radio], #tp-inspector .tp-insp-rowcolor .swatch >> nth=1').catch(() => {});
  await pg.$eval('#tp-insp-color', (e) => { e.value = '#ff4444'; });
  await pg.click('#tp-insp-save');
  await pg.waitForTimeout(600);
  const c1 = (await snap()).cues[0];
  t.check('a Break keeps no hidden speaker', c1.kind === 'break' && !c1.speaker);
  t.check('row colour shows in the running order', await pg.$eval('#cuelist tbody tr[data-pos="1"]', (r) => r.classList.contains('tp-row-color') && r.style.getPropertyValue('--tp-row-accent').trim() === '#ff4444'));

  // "Day starts now" on an anchored day asks in an ftl modal, not confirm().
  let native = false;
  pg.on('dialog', (d) => { native = true; d.dismiss(); });
  await pg.click('#tp-day-start');
  await pg.waitForTimeout(400);
  await pg.click('#tp-day-start');
  await pg.waitForTimeout(400);
  t.check('re-anchoring asks in an ftl modal', !native && !!(await pg.$('dialog.modal[open] .modal-footer')));
  await pg.keyboard.press('Escape');

  // Day-start field marks a bad value.
  await pg.fill('#tp-day-begins', '25:99');
  await pg.click('#tp-day-start-form button[type=submit]');
  await pg.waitForTimeout(400);
  t.check('a bad day start is marked invalid', (await pg.getAttribute('#tp-day-begins', 'aria-invalid')) === 'true');

  // Messages: one delete per message.
  await pg.fill('#messages-panel input[name=text]', 'Mic 2 is live');
  await pg.uncheck('#messages-panel input[name=show]');
  await pg.click('#messages-panel button[type=submit]');
  await pg.waitForSelector('[data-msg-del]');
  await pg.click('[data-msg-del]');
  await pg.waitForTimeout(600);
  t.check('a message deletes on its own', (await snap()).messages.length === 0);

  // #audience switches tabs on a hash change too.
  await pg.evaluate(() => { location.hash = 'audience'; });
  await pg.waitForTimeout(200);
  t.check('#audience switches tabs on hashchange', await pg.$eval('[data-panel=audience]', (e) => !e.hidden));
  await pg.evaluate(() => { location.hash = ''; });

  // #3: liquid-glass — the bar covers nothing, no decorative sidebar, all columns fit.
  await pg.evaluate(() => localStorage.setItem('timerpi.theme', 'liquid-glass'));
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tfoot');
  await pg.waitForTimeout(500);
  const lg = await pg.evaluate(() => {
    const tab = document.querySelector('.tp-room-tabs .tab');
    const r = tab.getBoundingClientRect();
    const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
    const bar = document.querySelector('.app-bar').getBoundingClientRect();
    const wrap = document.querySelector('.tp-cuelist-wrap');
    return { tabHit: tab.contains(hit), barBottom: bar.bottom, tabTop: r.top, rail: document.querySelector('.app-rail').getBoundingClientRect().width,
      over: wrap.scrollWidth - wrap.clientWidth };
  });
  t.check(`liquid-glass: the tabs sit below the bar and take clicks (${Math.round(lg.barBottom)} ≤ ${Math.round(lg.tabTop)})`, lg.tabHit && lg.barBottom <= lg.tabTop);
  t.check('liquid-glass: no sidebar', lg.rail === 0);
  t.check(`liquid-glass: every running-order column is in view (${lg.over}px over)`, lg.over <= 1);
  await pg.click('#cuelist tbody tr[data-pos="2"] [data-insp]');
  const lab = await pg.$eval('#tp-inspector .label', (l) => getComputedStyle(l).color);
  t.check(`liquid-glass: dialog labels are dark ink (${lab})`, lab !== 'rgb(255, 255, 255)');
  await pg.keyboard.press('Escape');
  await pg.evaluate(() => localStorage.removeItem('timerpi.theme'));

  // #24: phone — two-row header, one-line footer, transport inside the screen.
  const phone = await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, storageState: await ctx.storageState() });
  const pp = await phone.newPage();
  await pp.goto(`${t.base}/c/${room}`);
  await pp.waitForSelector('#cuelist tfoot');
  await pp.waitForTimeout(500);
  const ph = await pp.evaluate(() => {
    const bar = document.querySelector('.app-bar');
    const boxes = [...bar.querySelectorAll(':scope > *')].filter((e) => e.offsetParent).map((e) => e.getBoundingClientRect()).sort((a, b) => a.top - b.top);
    let rows = 0, bottom = -1;
    for (const r of boxes) { if (r.top >= bottom - 2) { rows++; bottom = r.bottom; } else bottom = Math.max(bottom, r.bottom); }
    const tb = [...document.querySelectorAll('.tp-transport-bar > *')].map((e) => e.getBoundingClientRect().right);
    return { rows, foot: document.querySelector('.app-status').getBoundingClientRect().height, right: Math.max(...tb), vw: innerWidth, scroll: scrollY };
  });
  t.check(`phone: header has at most 2 rows (${ph.rows})`, ph.rows <= 2);
  t.check(`phone: footer is one line (${Math.round(ph.foot)}px)`, ph.foot < 48);
  t.check(`phone: FLASH and BLANK are inside the screen (${Math.round(ph.right)} ≤ ${ph.vw})`, ph.right <= ph.vw);
  t.check('phone: the page does not jump on load', ph.scroll === 0);

  // Screens page footer reports the link.
  await pg.goto(`${t.base}/screens/${room}`);
  await pg.waitForFunction(() => document.getElementById('conn-label')?.dataset.state === 'live', null, { timeout: 5000 }).catch(() => {});
  t.check('Screens page footer shows the server link', (await pg.textContent('#conn-label')).includes('Server link up'));

  // Event end in the past asks first.
  await pg.goto(`${t.base}/e/${event}/admin`);
  await pg.fill('#ev-end-input', '2020-01-01T10:00');
  await pg.click('#ev-end button[type=submit]');
  await pg.waitForTimeout(300);
  t.check('a past event end asks for confirmation', !!(await pg.$('dialog.modal[open]')));
  await pg.keyboard.press('Escape');

  t.check(`no page errors (${errs.join('; ')})`, errs.length === 0);
  await phone.close();
  await ctx.close();
}
