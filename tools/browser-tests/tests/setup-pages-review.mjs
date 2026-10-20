// UI/UX review round (2026-10-20), event and setup pages: the layout
// editor makes room (swap on drop, trim on resize) instead of refusing every
// gesture on a full canvas (I13); the editor is a standard ftl modal (I11);
// waiting screens list live ones first with a one-tap "Show the timer here"
// (I8, simplification 3); capture starts on the type's built-in (S8); the
// Event Technician page patches room cards instead of reloading, the ⋯ menu
// is the theme's icon at full size (#10) and an idle card's clock is muted (#5).
export const name = 'event + setup pages: editor makes room, waiting list, room cards';

export async function run(t) {
  const { ctx, H, event, room } = await t.newEvent(['Main']);
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const errs = [];

  /* ---- I13: swap on drop, trim on resize ---- */
  const nb = await (await api('POST', `/api/shows/${room}/boards`, { name: 'Full' })).json();
  await api('PUT', `/api/shows/${room}/boards/${nb.id}`, { layout: { v: 1, rows: 8, orientation: 'landscape', widgets: [
    { id: 'a', type: 'countdown', x: 0, y: 0, w: 6, h: 8, opts: {} },
    { id: 'b', type: 'wallclock', x: 6, y: 0, w: 6, h: 8, opts: {} },
  ] } });
  const geo = async () => {
    const l = await (await api('GET', `/api/shows/${room}/boards`)).json();
    const b = (l.boards || l).find((x) => x.id === nb.id);
    const lay = typeof b.layout === 'string' ? JSON.parse(b.layout) : b.layout;
    return Object.fromEntries(lay.widgets.map((w) => [w.id, `${w.x},${w.w}`]));
  };
  const ed = await ctx.newPage();
  ed.on('pageerror', (e) => errs.push(e.message));
  await ed.goto(`${t.base}/d/${room}?view=board&board=${nb.id}&edit=1&preview=1`);
  await ed.click('#b-edit-toggle');
  const colW = await ed.$eval('#b-grid', (g) => g.clientWidth / 12);
  const drag = async (sel, dx) => {
    const bb = await (await ed.$(sel)).boundingBox();
    const x = sel.includes('resize') ? bb.x + bb.width / 2 : bb.x + 8;
    await ed.mouse.move(x, bb.y + bb.height / 2);
    await ed.mouse.down();
    await ed.mouse.move(x + dx, bb.y + bb.height / 2, { steps: 8 });
    await ed.mouse.up();
    await ed.waitForTimeout(1200);
  };
  await drag('[data-wid="a"] .b-w-chrome', colW * 6);
  let g = await geo();
  t.check(`a tile dropped on its neighbour swaps with it (${JSON.stringify(g)})`, g.a === '6,6' && g.b === '0,6');
  await drag('[data-wid="b"] .b-w-resize', colW * 2);
  g = await geo();
  t.check(`growing a tile trims the neighbour it grows into (${JSON.stringify(g)})`, g.b === '0,8' && g.a === '8,4');

  /* ---- I8 + simplification 3: waiting list, one-tap capture ---- */
  const tvCtx = await t.browser.newContext();
  const tv1 = await tvCtx.newPage();
  await tv1.goto(`${t.base}/d/`);
  await tv1.waitForTimeout(600);
  const tv2 = await (await t.browser.newContext()).newPage();
  await tv2.goto(`${t.base}/d/`);
  await tv2.waitForTimeout(600);
  const w = await (await api('GET', '/api/waiting')).json();
  const names = (w.waiting || []).map((x) => x.name);
  t.check(`waiting list: newest first, both live (${names.join(', ')})`, names.length >= 2 && w.waiting[0].id > w.waiting[1].id && w.waiting.slice(0, 2).every((x) => x.live === true));
  const pg = await ctx.newPage();
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.setViewportSize({ width: 1440, height: 900 });
  await pg.goto(`${t.base}/screens/${room}`);
  await pg.waitForSelector('#tp-waiting-list .tp-wait');
  t.check('waiting cards show a live lamp (ftl .connection)', await pg.$eval('#tp-waiting-list .tp-wait .connection', (e) => e.dataset.state === 'live'));
  // Options… opens capture on the type's own built-in, not the plain timer.
  await pg.locator('#tp-waiting-list .tp-wait').nth(1).locator('button:text-is("Options…")').click();
  await pg.waitForSelector('#tp-capture[open]');
  const tplA = await pg.inputValue('#tp-capture-template');
  await pg.click('#tp-capture-kind label:has(input[value="presenter"])');
  const tplP = await pg.inputValue('#tp-capture-template');
  t.check(`capture starts on the type's built-in (audience=${tplA}, presenter=${tplP})`, tplA === 'main' && tplP === 'stage');
  await pg.click('#tp-capture button[data-close].btn-secondary');
  // One tap on the newest screen: presenter + Full timer, no dialog.
  const newest = w.waiting[0].name;
  await pg.locator('#tp-waiting-list .tp-wait', { hasText: newest }).locator('button:text-is("Show the timer here")').click();
  await pg.waitForTimeout(500);
  const s = await (await api('GET', `/api/shows/${room}/screens`)).json();
  const made = (s.screens || []).find((x) => x.kind === 'presenter');
  t.check(`"Show the timer here" sets up a presenter screen on Full timer (${made?.name}, ${made?.template})`, !!made && made.template === 'stage' && !(await pg.$('#tp-capture[open]')));
  t.check(`the captured screen keeps the name the TV shows (${made?.name} = ${newest})`, made?.name === newest);

  /* ---- I11: the editor is a standard ftl modal ---- */
  await api('POST', `/api/shows/${room}/screens/config`, { name: made?.name || 'Presenter', boardId: nb.id, kind: 'presenter' });
  await pg.reload();
  await pg.waitForSelector('.tp-scr');
  await pg.locator('.tp-scr button:text-is("Edit layout")').first().click();
  await pg.waitForSelector('#tp-screen-edit[open]');
  const box = await (await pg.$('#tp-screen-edit')).boundingBox();
  t.check(`editor: header close + footer Done, fills the window (${Math.round(box.width)}x${Math.round(box.height)})`,
    !!(await pg.$('#tp-screen-edit .modal-header .btn-close[data-close]')) && !!(await pg.$('#tp-screen-edit .modal-footer #tp-screen-edit-close[data-close]')) && box.height > 850);
  const frame = pg.frameLocator('#tp-screen-edit-frame');
  await frame.locator('body[data-compose]').waitFor();
  t.check('editor frame has no developer chrome (top bar, EDIT MODE, compose-link note hidden)',
    !(await frame.locator('.b-topbar').isVisible()) && !(await frame.locator('#b-edit-badge').isVisible()));
  await pg.click('#tp-screen-edit .btn-close');

  /* ---- Event Technician page ---- */
  const ad = await ctx.newPage();
  ad.on('pageerror', (e) => errs.push(e.message));
  await ad.setViewportSize({ width: 1440, height: 900 });
  await ad.goto(`${t.base}/e/${event}/admin`);
  await ad.waitForSelector('#live-grid [data-room]');
  const more = await ad.$eval('#live-grid .tp-room-more', (b) => ({ w: b.getBoundingClientRect().width, icon: b.querySelector('use')?.getAttribute('href') || '' }));
  t.check(`⋯ room menu: theme "more" icon, ≥ 2rem (${Math.round(more.w)}px, ${more.icon})`, more.w >= 32 && more.icon.endsWith('#icon-more-horizontal'));
  const clock = await ad.$eval('#live-grid .tp-clock', (c) => ({ s: c.dataset.state, sh: getComputedStyle(c).textShadow }));
  t.check(`idle room clock is muted, no glow (${clock.s}, ${clock.sh})`, clock.s === 'idle' && clock.sh === 'none');
  await ad.evaluate(() => { window.__noReload = 1; });
  await ad.fill('#room-add input[name=name]', 'Annex');
  await ad.click('#room-add button[type=submit]');
  await ad.waitForFunction(() => [...document.querySelectorAll('#live-grid [data-room]')].length === 2, null, { timeout: 5000 }).catch(() => {});
  const after = await ad.evaluate(() => ({ kept: window.__noReload === 1, cards: document.querySelectorAll('#live-grid [data-room]').length,
    pair: [...document.querySelectorAll('#pair-room option')].map((o) => o.textContent) }));
  t.check(`Add room patches the page, no reload (${JSON.stringify(after)})`, after.kept && after.cards === 2 && after.pair.includes('Annex'));
  const primaries = await ad.$$eval('#live-grid .btn-primary', (b) => b.length);
  t.check(`room cards carry no primary button (${primaries})`, primaries === 0);

  /* ---- phone: 44px tile controls (#13), toasts clear the bar (#12) ---- */
  const phone = await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, storageState: await ctx.storageState() });
  const pe = await phone.newPage();
  pe.on('pageerror', (e) => errs.push(e.message));
  await pe.goto(`${t.base}/d/${room}?view=board&board=${nb.id}&edit=1&preview=1&compose=1`);
  // Round 2 (#16): on phones the tiles keep only their drag bar; every
  // control is in the tile list under the canvas, at touch size.
  await pe.waitForSelector('body[data-editing] .b-ed-row button');
  t.check('phone editor: tile chrome buttons give the tile its room', (await pe.$$eval('.b-w-chrome button', (bs) => bs.filter((b) => b.getBoundingClientRect().height > 0).length)) === 0);
  const minBtn = await pe.$$eval('.b-ed-row button', (bs) => Math.min(...bs.map((b) => b.getBoundingClientRect().height).filter((h) => h > 0))); // folded rows hide their steppers
  t.check(`phone editor: tile list controls are ≥ 44px (${Math.round(minBtn)})`, minBtn >= 43.5);
  const listShown = await pe.$eval('#b-editor', (e) => getComputedStyle(e).display !== 'none' && e.querySelectorAll('.b-ed-row').length > 0);
  t.check('phone editor: the X/Y/W/H tile list shows', listShown);
  const pa = await phone.newPage();
  pa.on('pageerror', (e) => errs.push(e.message));
  await pa.goto(`${t.base}/e/${event}/admin`);
  await pa.waitForSelector('#live-grid [data-room]');
  await pa.fill('#room-add input[name=name]', 'Foyer');
  await pa.click('#room-add button[type=submit]');
  await pa.waitForSelector('#toast-region .toast');
  const tb = await pa.evaluate(() => ({ bar: document.querySelector('.app-bar').getBoundingClientRect().bottom, toast: document.querySelector('#toast-region .toast').getBoundingClientRect().top }));
  t.check(`phone toast sits below the app bar (bar ${Math.round(tb.bar)}, toast ${Math.round(tb.toast)})`, tb.toast >= tb.bar);
  await phone.close();

  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await tvCtx.close();
  await ctx.close();
}
