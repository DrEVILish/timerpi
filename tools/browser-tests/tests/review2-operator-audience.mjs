// Round-2 review (2026-10-20), Event Technician / Screens / layout editor /
// moderator Audience tab / audience phone: Pair sits on the Screen name row
// (#4), imports are real buttons (#14), the theme saves on change (#8), no
// phone sign-in QR on the phone (N10), the Screens page has the one-row
// phone bar with an Event tab (#9, N10), tiles carry their palette names
// (N6), the editor offers "Add a tile" as a dropdown on tablets (#16) and
// says why a gesture was refused on the tile (N12), the quiz editor
// catches a missing answer (N7), the phone gets its vote back from the
// server (N8), shows one upvote mark (N9) and stops saying "Connected"
// when the link is silent (N4).
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

export const name = 'review 2: operator pages and the audience phone';

export async function run(t) {
  const { ctx, H, event, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const errs = [];
  const watch = (p) => p.on('pageerror', (e) => errs.push(e.message));

  // --- Event Technician page (desktop) ---------------------------------
  const pg = await ctx.newPage();
  watch(pg);
  await pg.setViewportSize({ width: 1440, height: 900 });
  await pg.goto(`${t.base}/e/${event}/admin`);
  const pb = await pg.$eval('#pair-form button[type=submit]', (b) => b.getBoundingClientRect());
  const pi = await pg.$eval('#pair-name', (b) => b.getBoundingClientRect());
  t.check(`Pair rides on the Screen name row (button ${Math.round(pb.top)}-${Math.round(pb.bottom)}, input ${Math.round(pi.top)}-${Math.round(pi.bottom)})`,
    pb.top < pi.bottom && pb.bottom > pi.top && Math.abs((pb.top + pb.bottom) / 2 - (pi.top + pi.bottom) / 2) < 4);
  t.check('the import labels are bordered buttons, not ghost text',
    await pg.$$eval('label.btn:has(#room-import), label.btn:has(#ev-import)', (ls) => ls.length === 2 && ls.every((l) => !l.classList.contains('btn-ghost'))));
  t.check('the Event ID value is plain mono text, not a muted badge', !!(await pg.$('.tp-event-id > .mono')) && !(await pg.$('.tp-event-id .badge')));
  const themes = await pg.$$eval('#ev-theme-select option', (os) => os.map((o) => o.value).filter(Boolean));
  await pg.selectOption('#ev-theme-select', themes[0]);
  await pg.waitForSelector('.toast', { timeout: 3000 }).catch(() => {});
  t.check('the default theme saves as soon as it is picked', /theme saved/i.test(await pg.textContent('#toast-region')));

  // --- Phone: no "Sign in on your phone" on the phone ------------------
  const phoneOp = await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
  await phoneOp.request.post(`${t.base}/api/events/${event}/login`, { data: { pw: 'test-pw1' }, headers: H });
  const ph = await phoneOp.newPage();
  watch(ph);
  await ph.goto(`${t.base}/e/${event}/admin`);
  t.check('no phone sign-in QR button on a phone', !(await ph.isVisible('#phone-link-btn')));

  // --- Screens page on the phone: one-row bar, Event tab, tile names ----
  await api('POST', `/api/shows/${room}/screens/config`, { name: 'Stage', kind: 'presenter' });
  await ph.goto(`${t.base}/screens/${room}`);
  await ph.waitForSelector('.tp-scr-tile');
  const barH = await ph.$eval('body > .app-bar', (b) => b.getBoundingClientRect().height);
  t.check(`the Screens page phone bar is one row (${Math.round(barH)}px)`, barH < 72);
  t.check('the room tabs reach the Event Technician on a phone', await ph.isVisible(`.tp-room-tabs a.tp-tab-et[href="/e/${event}/admin"]`));
  const tiles = await ph.$$eval('.tp-scr-tile', (ts) => ts.map((x) => x.textContent));
  t.check(`card previews use the palette's tile names (${tiles.join(', ')})`, tiles.includes('Cue label') && !tiles.some((x) => /^[a-z]+$/.test(x)));
  await pg.goto(`${t.base}/screens/${room}`);
  t.check('no Event tab on a desktop', !(await pg.isVisible('.tp-tab-et')));

  // --- Layout editor ----------------------------------------------------
  const nb = await (await api('POST', `/api/shows/${room}/boards`, { name: 'Two' })).json();
  const two = { v: 1, rows: 8, orientation: 'landscape', widgets: [
    { id: 'a', type: 'countdown', x: 0, y: 0, w: 12, h: 4 }, { id: 'b', type: 'cuelabel', x: 0, y: 4, w: 12, h: 4 }] };
  await api('PUT', `/api/shows/${room}/boards/${nb.id}`, { layout: two });
  const edURL = `${t.base}/d/${room}?view=board&board=${nb.id}&edit=1&preview=1&compose=1`;
  const tab = await ctx.newPage();
  await tab.setViewportSize({ width: 1024, height: 768 });
  watch(tab);
  await tab.goto(edURL);
  await tab.waitForSelector('#b-add-select');
  t.check('tablet: "Add a tile" is one dropdown, the 20 buttons are folded away',
    await tab.isVisible('#b-add-select') && !(await tab.isVisible('#b-palette-list')));
  const glyph = await tab.$eval('.b-w-chrome [data-gear] svg.icon', (s) => s.getBoundingClientRect().width);
  t.check(`tile settings is a sprite icon at a readable size (${glyph}px)`, glyph >= 14);
  // Growing tile a over b would squash b to nothing: refused, on the tile.
  const rs = await tab.$eval('#b-w-a .b-w-resize', (r) => { const b = r.getBoundingClientRect(); return [b.x + b.width / 2, b.y + b.height / 2]; });
  await tab.mouse.move(rs[0], rs[1]);
  await tab.mouse.down();
  await tab.mouse.move(rs[0], rs[1] + 600, { steps: 6 });
  await tab.mouse.up();
  t.check('a refused resize says why on the tile itself', /No room/.test(await tab.$eval('#b-w-a', (x) => x.querySelector('.b-w-refused')?.textContent || '')));
  await tab.selectOption('#b-add-select', 'notice');
  await tab.waitForTimeout(1500);
  const lay = (await (await api('GET', `/api/shows/${room}/boards`)).json());
  const board = (lay.boards || lay).find((x) => x.id === nb.id);
  const widgets = (typeof board.layout === 'string' ? JSON.parse(board.layout) : board.layout).widgets;
  t.check(`the dropdown adds the tile (${widgets.map((w) => w.type)})`, widgets.some((w) => w.type === 'notice'));
  const edPh = await phoneOp.newPage();
  watch(edPh);
  await edPh.goto(edURL);
  await edPh.waitForSelector('.b-ed-name');
  const names = await edPh.$$eval('.b-ed-name', (ns) => ns.map((n) => n.textContent));
  t.check(`phone tile list uses tile names (${names})`, names[0] === 'Countdown' && names.includes('Cue label'));

  // --- Quiz editor: a missing answer is caught at the options -----------
  await pg.goto(`${t.base}/c/${room}#audience`);
  await pg.click('#tp-aud-new');
  await pg.click('#tp-aud-editor [data-kind="quiz"]');
  await pg.fill('#tp-aud-question', 'Capital of France?');
  const opts = pg.locator('#tp-aud-opt-list input[type=text]');
  await opts.nth(0).fill('Paris');
  await opts.nth(1).fill('Lyon');
  await pg.click('#tp-aud-editor .modal-footer .btn-primary');
  t.check('a quiz without its answer says "Tick the correct answer."', (await pg.textContent('#tp-aud-editor .field-error')) === 'Tick the correct answer.');
  t.check('the answer radios are marked aria-invalid', await pg.$$eval('#tp-aud-opt-list input[type=radio]', (rs) => rs.every((r) => r.getAttribute('aria-invalid') === 'true')));
  t.check('nothing was created', (await (await api('GET', `/api/shows/${room}/polls`)).json()).items.length === 0);

  // --- Audience phone ---------------------------------------------------
  const poll = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'poll', question: 'Lunch?', options: ['Pizza', 'Soup'] })).json();
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'audience', on: true });
  const audCtx = await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });
  const aud = await audCtx.newPage();
  watch(aud);
  await aud.goto(`${t.base}/a/${room}`);
  await aud.waitForSelector('.tp-aud-opt');
  await aud.click('.tp-aud-opt >> nth=1');
  await aud.waitForTimeout(600);
  await aud.evaluate(() => localStorage.clear());
  await aud.reload();
  await aud.waitForSelector('.tp-aud-opt[aria-pressed="true"]', { timeout: 4000 }).catch(() => {});
  t.check('with storage cleared the phone gets its vote back from the server',
    await aud.$eval('.tp-aud-opt >> nth=1', (b) => b.getAttribute('aria-pressed') === 'true'));

  const qa = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'qa', question: 'Ask', autoApprove: true })).json();
  await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target: 'audience', on: true });
  await aud.waitForSelector('textarea');
  await aud.fill('textarea', 'Why?');
  await aud.click('.tp-aud-ask button[type=submit]');
  await aud.waitForSelector('.tp-aud-up');
  t.check('the upvote shows "▲ n" without a second lamp mark',
    await aud.$eval('.tp-aud-up', (b) => getComputedStyle(b, '::before').content === 'none' && b.textContent.startsWith('▲')));

  // Network gone: a send fails with "Reconnecting…", and a silent socket is
  // given up after 35 s (time moved on in the page).
  await audCtx.setOffline(true);
  await aud.click('.tp-aud-up');
  await aud.waitForTimeout(300);
  t.check('a send without a network shows the reconnecting state', (await aud.$eval('#tp-aud-note', (n) => n.dataset.state)) === 'reconnecting');
  await aud.reload().catch(() => {});
  await audCtx.setOffline(false);
  await aud.goto(`${t.base}/a/${room}`);
  await aud.waitForFunction(() => document.getElementById('tp-aud-note')?.dataset.state === 'live', null, { timeout: 5000 }).catch(() => {});
  await audCtx.setOffline(true);
  await aud.evaluate(() => { const now = Date.now; Date.now = () => now() + 60000; });
  await aud.waitForTimeout(6000);
  t.check(`a silent link stops saying "Connected" (${await aud.textContent('#tp-aud-note')})`, (await aud.$eval('#tp-aud-note', (n) => n.dataset.state)) !== 'live');
  await audCtx.setOffline(false);

  // Importing an event file lands on the new event's page, which says so.
  const file = join(mkdtempSync(join(tmpdir(), 'tp-ev-')), 'event.json');
  writeFileSync(file, await (await api('GET', `/api/events/${event}/export`)).body());
  await pg.goto(`${t.base}/e/${event}/admin`);
  await pg.setInputFiles('#ev-import', file);
  await pg.fill('dialog.modal[open] input', 'import-pw1');
  await Promise.all([pg.waitForURL((u) => !u.pathname.startsWith(`/e/${event}/`), { timeout: 8000 }).catch(() => {}),
    pg.click('dialog.modal[open] .modal-footer .btn-primary')]);
  await pg.waitForSelector('.toast', { timeout: 4000 }).catch(() => {});
  t.check('an imported event says so on its new page', /Imported as a new event/.test(await pg.textContent('#toast-region')) && !pg.url().includes('imported='));

  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await audCtx.close();
  await phoneOp.close();
  await ctx.close();
}
