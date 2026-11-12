// Event Technician page as Live · Setup tabs (2026-11-12): Live is one ftl
// table row per room with a live time left and the room transport in the ⋯
// menu; Setup holds pair, settings, file and danger zone; settings save on
// change (no Save button but the password's); #setup survives a reload; and
// a 390px phone never scrolls sideways.
export const name = 'event admin: Live rooms table, ⋯ transport, Setup tab saves on change, phone fits';

export async function run(t) {
  const { ctx, H, event, room } = await t.newEvent(['Stark', 'Banner']);
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: ['One', 'Two'].map((label) => ({ label, durationMS: 1_200_000 })), headers: H,
  });
  const live = async () => (await (await ctx.request.get(`${t.base}/api/events/${event}/live`)).json()).rooms.find((r) => r.code === room);
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.setViewportSize({ width: 1440, height: 900 });
  await pg.goto(`${t.base}/e/${event}/admin`);
  const row = `#live-grid tr[data-room="${room}"]`;
  await pg.waitForSelector(row);

  t.check('Live is the default tab', await pg.$eval('.tabs [data-tab="live"]', (b) => b.classList.contains('is-active')) && await pg.isVisible('.tp-ev-rooms'));
  t.check('rooms are one ftl .table.is-stacked in a .table-wrap',
    await pg.$eval('#live-grid', (b) => b.closest('table').matches('.table.is-stacked') && !!b.closest('.table-wrap')));
  t.check('one row per room', (await pg.$$('#live-grid tr[data-room]')).length === 2);
  t.check('each row has Open (the room page) and a ⋯ menu',
    !!(await pg.$(`${row} a[href="/c/${room}"]`)) && !!(await pg.$(`${row} [data-room-menu]`)));
  t.check('Blackout all, Restore all sit on the Live tab', await pg.isVisible('[data-verb-all="blank"]') && await pg.isVisible('[data-verb-all="unblank"]'));

  // GO from the ⋯ menu; the time left counts down without a reload.
  const menu = async (act) => { await pg.click(`${row} [data-room-menu]`); await pg.click(`#room-menu [data-act="${act}"]`); };
  await menu('go');
  await pg.waitForFunction((r) => document.querySelector(`${r} td[data-label="Now"]`)?.textContent === 'One', row, { timeout: 5000 }).catch(() => {});
  t.check('GO in the ⋯ menu starts the room', (await live()).running);
  t.check('the row shows the running session', (await pg.textContent(`${row} td[data-label="Now"]`)) === 'One');
  t.check('a running room with no screens is flagged', await pg.$eval(`${row} td[data-label="Screens"]`, (c) => c.classList.contains('text-warning')));
  const t1 = await pg.textContent(`${row} .tp-clock`);
  await pg.waitForTimeout(1600);
  const t2 = await pg.textContent(`${row} .tp-clock`);
  t.check(`time left is mono and live (${t1} → ${t2})`, /\d:\d\d/.test(t1) && t1 !== t2 && await pg.$eval(`${row} .tp-clock`, (c) => c.classList.contains('mono')));
  t.check('the lamp says Running', /Running/.test(await pg.textContent(`${row} .status`)));

  await menu('pause');
  await pg.waitForTimeout(800);
  t.check('Pause in the ⋯ menu pauses', (await live()).paused);
  await menu('blank');
  await pg.waitForFunction((r) => /Blacked out/.test(document.querySelector(`${r} .status`)?.textContent), row, { timeout: 5000 }).catch(() => {});
  t.check('Blackout in the ⋯ menu blanks the room', (await live()).blanked);
  t.check('the lamp says Blacked out', /Blacked out/.test(await pg.textContent(`${row} .status`)));
  await pg.click(`${row} [data-room-menu]`);
  t.check('the menu then offers Restore', (await pg.textContent('#room-menu [data-act="blank"]')) === 'Restore');
  await pg.click('#room-menu [data-act="blank"]');
  await pg.waitForTimeout(800);
  t.check('Restore brings it back', !(await live()).blanked);

  // Setup tab: pair, settings, file and danger zone, none of them on Live.
  await pg.click('.tabs [data-tab="setup"]');
  t.check('Setup holds pair, settings, file, danger zone',
    await pg.isVisible('#pair-form') && await pg.isVisible('#settings') && await pg.isVisible('#ev-export') && await pg.isVisible('#ev-delete'));
  t.check('the rooms table is hidden on Setup', !(await pg.isVisible('.tp-ev-rooms')));
  t.check('the URL carries #setup', new URL(pg.url()).hash === '#setup');
  t.check('event name and end have no Save button; the password keeps one',
    !(await pg.$('#ev-rename button')) && !(await pg.$('#ev-end button')) && !!(await pg.$('#ev-pw button[type=submit]')));
  await pg.evaluate(() => { window.__noReload = 1; });
  await pg.fill('#ev-name-input', 'Renamed live');
  await pg.press('#ev-name-input', 'Enter');
  await pg.waitForSelector('#toast-region .toast', { timeout: 3000 }).catch(() => {});
  const stored = (await (await ctx.request.get(`${t.base}/api/events/${event}`)).json()).event.name;
  t.check(`event name saves on Enter (${stored})`, stored === 'Renamed live');
  t.check('a saved confirmation shows', /saved/i.test(await pg.textContent('#toast-region')));
  t.check('no reload, and the bar title follows', await pg.evaluate(() => window.__noReload === 1 && document.querySelector('.tp-bar-title').textContent === 'Renamed live'));
  await pg.fill('#ev-name-input', 'Renamed on blur');
  await pg.click('#settings-h');
  await pg.waitForTimeout(800);
  t.check('event name saves when the field is left',
    (await (await ctx.request.get(`${t.base}/api/events/${event}`)).json()).event.name === 'Renamed on blur');

  await pg.reload();
  await pg.waitForSelector('#pair-form');
  t.check('a reload on #setup lands on Setup', await pg.isVisible('#pair-form') && !(await pg.isVisible('.tp-ev-rooms')));
  await pg.click('.tabs [data-tab="live"]');
  t.check('back on Live the hash is gone', new URL(pg.url()).hash === '' && await pg.isVisible('.tp-ev-rooms'));

  // Phone: no sideways scroll on either tab.
  const phone = await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, storageState: await ctx.storageState() });
  const ph = await phone.newPage();
  ph.on('pageerror', (e) => errs.push(e.message));
  await ph.goto(`${t.base}/e/${event}/admin`);
  await ph.waitForSelector(row);
  const fits = () => ph.evaluate(() => ({ sw: document.documentElement.scrollWidth, vw: innerWidth }));
  const a = await fits();
  t.check(`phone Live: no horizontal overflow (${a.sw} ≤ ${a.vw})`, a.sw <= a.vw);
  t.check('phone Live: rows are stacked cards', await ph.$eval(row, (r) => getComputedStyle(r).display !== 'table-row'));
  await ph.click('.tabs [data-tab="setup"]');
  const b = await fits();
  t.check(`phone Setup: no horizontal overflow (${b.sw} ≤ ${b.vw})`, b.sw <= b.vw);
  await phone.close();

  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
