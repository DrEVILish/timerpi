// The room page as one live workspace (owner 2026-11-12, review R2 + N1):
// the clock and the transport stay on every tab, the "On air now" column
// (on-air items + messages) shows on Run and Audience alike, the join QR and
// Import open in ftl drawers, row actions are Go · pencil · ⋯ with Cue up in
// the menu, and the room name in the bar switches rooms (keeping the view).
export const name = 'room workspace: clock on every tab, On air column, drawers, room switcher';

export async function run(t) {
  const { ctx, H, event, rooms } = await t.newEvent(['Main', 'Side']);
  const [room, side] = rooms;
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const snap = async (r = room) => (await api('GET', `/api/shows/${r}`)).json();
  for (const r of rooms) await api('PUT', `/api/shows/${r}/cues`, [{ label: 'Opening', durationMS: 600_000 }, { label: 'Keynote', durationMS: 1_800_000 }, { label: 'Coffee', durationMS: 900_000 }]);
  await api('POST', `/api/shows/${side}/cmd/start`, { pos: 1 });
  const poll = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'poll', question: 'Lunch?', options: ['Pizza', 'Soup'] })).json();
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'audience', on: true });

  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.setViewportSize({ width: 1440, height: 900 });
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tbody tr[data-pos="3"]');

  // The clock, GO and the On air column on Run and on Audience.
  for (const tab of ['run', 'audience']) {
    await pg.click(`.tp-room-tabs [data-tab="${tab}"]`);
    // Run leads the column with Messages; Audience with what is on air.
    const lead = tab === 'run' ? '#messages-panel' : '#tp-aud-onair';
    const seen = await pg.evaluate((lead) => ['#tp-clock', '.tp-transport-bar [data-cmd="go"]', '.tp-transport-bar .tp-bar-wrapup', '#tp-blank', lead]
      .map((s) => { const e = document.querySelector(s); const r = e?.getBoundingClientRect(); return !!r && r.width > 0 && r.top < innerHeight && r.top >= 0; }), lead);
    t.check(`${tab}: clock, GO, WRAP UP, BLANK and ${lead} are on screen (${seen})`, seen.every(Boolean));
    t.check(`${tab}: Messages are in the On air column`, await pg.$eval('#messages-panel', (e) => !!e.closest('.tp-side') && e.getBoundingClientRect().width > 0));
  }
  t.check('the On air column holds the poll on the Audience tab', /Lunch\?/.test(await pg.textContent('#tp-aud-onair')));
  // Join QR is a drawer on the Audience tab.
  t.check('the join QR is closed until asked for', !(await pg.isVisible('#share-qr')));
  await pg.click('#tp-aud-panel [popovertarget="tp-share-drawer"]');
  t.check('Join QR opens a drawer with the QR and the link', await pg.isVisible('#tp-share-drawer #share-qr') && await pg.isVisible('#share-copy'));
  await pg.keyboard.press('Escape');
  t.check('Esc closes the drawer', !(await pg.isVisible('#share-qr')));
  // A per-item ⋯ in the library still opens its menu (it moved out of the panel).
  await pg.click('#tp-aud-items [aria-haspopup="menu"]');
  t.check('the library item menu opens', await pg.$eval('#tp-aud-menu', (m) => m.matches(':popover-open')));
  await pg.keyboard.press('Escape');

  // Every row's actions are on screen, the table fits its panel and the
  // adjust buttons fit the now band at laptop and desktop widths (review).
  for (const w of [1280, 1440, 1920]) {
    await pg.setViewportSize({ width: w, height: 900 });
    await pg.click('.tp-room-tabs [data-tab="run"]');
    await pg.waitForTimeout(300);
    const fit = await pg.evaluate(() => {
      const wrap = document.querySelector('#cuelist').getBoundingClientRect();
      const table = document.querySelector('#cuelist table').getBoundingClientRect();
      const more = [...document.querySelectorAll('#cuelist tbody [data-row-menu]')].every((b) => { const r = b.getBoundingClientRect(); return r.width > 0 && r.right <= wrap.right + 1; });
      const band = document.querySelector('#tp-now').getBoundingClientRect();
      const adj = [...document.querySelectorAll('.tp-adjust-row .btn, #tp-rate')].every((b) => b.getBoundingClientRect().right <= band.right + 1 && b.getBoundingClientRect().width > 20);
      return { more, table: Math.round(table.width), wrap: Math.round(wrap.width), adj, doc: document.documentElement.scrollHeight, vh: innerHeight };
    });
    t.check(`${w}px: every row's ⋯ is on screen and the table fits (${fit.table} ≤ ${fit.wrap})`, fit.more && fit.table <= fit.wrap + 1);
    t.check(`${w}px: adjust and rate fit the now band`, fit.adj);
    t.check(`${w}px: the window does not scroll (${fit.doc} ≤ ${fit.vh})`, fit.doc <= fit.vh);
  }
  await pg.setViewportSize({ width: 1440, height: 900 });

  // Rows: Go · pencil · ⋯; Cue up is in the menu and only arms the cue.
  await pg.click('.tp-room-tabs [data-tab="run"]');
  const btns = await pg.$$eval('#cuelist tbody tr[data-pos="2"] .tp-cue-rowbtns .btn', (n) => n.filter((b) => b.offsetParent).map((b) => b.dataset.cmd || (b.dataset.insp ? 'edit' : b.dataset.rowMenu ? 'more' : '?')));
  t.check(`a row is Go · edit · ⋯ (${btns})`, btns.join(',') === 'go,edit,more');
  await pg.click('#cuelist tbody tr[data-pos="3"] [data-row-menu]');
  await pg.click('#tp-row-menu [data-row-act="cue"]');
  await pg.waitForTimeout(700);
  const rt = (await snap()).runtime;
  t.check(`Cue up in the row menu arms cue 3 without starting it (${rt.activePos}, running ${rt.running})`, rt.activePos === 3 && !rt.running);

  // Room switcher (N1): both rooms, this one current, live state on open.
  await pg.click('.tp-room-switch');
  await pg.waitForSelector('#tp-room-pop:popover-open');
  await pg.waitForFunction(() => document.querySelectorAll('#tp-room-pop .tp-rs-state.status').length === 2, null, { timeout: 5000 }).catch(() => {});
  const menu = await pg.$$eval('#tp-room-pop [data-room-i]', (n) => n.map((a) => ({ name: a.querySelector('.tp-rs-name').textContent, here: a.hasAttribute('aria-current'), state: a.querySelector('.tp-rs-state').textContent, href: a.getAttribute('href') })));
  t.check(`the switcher lists both rooms, this one current (${JSON.stringify(menu)})`, menu.length === 2 && menu[0].here && !menu[1].here && menu[0].href === `/c/${room}`);
  t.check(`each room shows its state (${menu.map((m) => m.state)})`, menu[0].state === 'Idle' && /left$/.test(menu[1].state));
  // A paused room reads Paused (its cue still counts as running).
  await pg.keyboard.press('Escape');
  await api('POST', `/api/shows/${side}/cmd/pause`, {});
  await pg.waitForTimeout(2300); // the walk-in feed is cached for 2 s
  await pg.click('.tp-room-switch');
  await pg.waitForFunction(() => document.querySelector('#tp-room-pop [data-room-i="1"] .tp-rs-state')?.textContent === 'Paused', null, { timeout: 5000 }).catch(() => {});
  t.check('a paused room reads Paused', (await pg.textContent('#tp-room-pop [data-room-i="1"] .tp-rs-state')) === 'Paused');
  await api('POST', `/api/shows/${side}/cmd/pause`, {});
  t.check('the Event Technician is one click away', await pg.isVisible(`#tp-room-pop a[href="/e/${event}/admin"]`));
  await pg.click(`#tp-room-pop a[href="/c/${side}"]`);
  await pg.waitForURL(`**/c/${side}`);
  t.check('picking a room opens its Run page', /Side/.test(await pg.textContent('.tp-room-switch')));
  // On the Screens page the switcher keeps the view.
  await pg.goto(`${t.base}/screens/${room}`);
  t.check('on Screens, the switcher links to the other room\'s Screens', !!(await pg.$(`#tp-room-pop a[href="/screens/${side}"]`)));
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await pg.close();

  // A moderator of Main only: Side links to the event's Rooms page to sign
  // in, not to a "Moderator access needed" page.
  const modCtx = await t.browser.newContext({ viewport: { width: 1440, height: 900 } });
  await modCtx.request.post(`${t.base}/api/events/${event}/rooms/${room}/login`, { data: { pw: '' }, headers: H });
  const mp = await modCtx.newPage();
  await mp.goto(`${t.base}/c/${room}`);
  const lock = await mp.$eval('#tp-room-pop [data-room-i="1"]', (a) => ({ href: a.getAttribute('href'), state: a.textContent }));
  t.check(`a moderator's switcher sends other rooms to sign in (${JSON.stringify(lock)})`, lock.href === `/e/${event}` && /Sign in/.test(lock.state));
  t.check('a moderator\'s own room links to its Run page', (await mp.getAttribute('#tp-room-pop [data-room-i="0"]', 'href')) === `/c/${room}`);
  await modCtx.close();

  // One room: the bar keeps the plain room link.
  const solo = await t.newEvent(['Only']);
  const sp = await solo.ctx.newPage();
  await sp.goto(`${t.base}/c/${solo.room}`);
  t.check('a one-room event has no switcher', !(await sp.$('.tp-room-switch')) && !!(await sp.$(`.app-bar a[href="/c/${solo.room}"]`)));
  await solo.ctx.close();

  // Phone, Audience tab: the transport sticks to the bottom and nothing
  // overflows sideways.
  const phone = await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, storageState: await ctx.storageState() });
  const ph = await phone.newPage();
  await ph.goto(`${t.base}/c/${room}#audience`);
  await ph.waitForSelector('#tp-aud-onair .tp-air-card');
  const go = await ph.$eval('.tp-transport-bar [data-cmd="go"]', (b) => { const r = b.getBoundingClientRect(); return r.bottom <= innerHeight && r.top > innerHeight / 2; });
  t.check('phone Audience tab: GO is in the sticky bar at the bottom', go);
  const air = await ph.$eval('#tp-aud-onair', (e) => e.getBoundingClientRect().top);
  const lib = await ph.$eval('#tp-aud-panel', (e) => e.getBoundingClientRect().top);
  t.check('phone Audience tab: what is on air comes before the library', air < lib);
  t.check('phone: no sideways scroll', await ph.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
  await phone.close();
}
