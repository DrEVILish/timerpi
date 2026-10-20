// UI/UX review round (2026-10-20) on the Run tab: it fits at 1440×900, 1024
// touch and 390 phone; the phone's slim transport keeps GO, FLASH and BLANK
// tappable; durations read as typed (h:mm); ↑/↓ scroll instead of changing
// cue, Alt+↑/↓ and Move up/down reorder; ftl row states and two-tap delete;
// Import adds by default and asks before replacing; a bare CSV number is
// minutes; the cue editor's footer is on screen.
export const name = 'Run tab review fixes: fit, phone transport, durations, reorder keys, import';

const CUES = [
  { label: 'Doors', durationMS: 600_000 },
  { label: 'Keynote', speaker: 'Ada', durationMS: 2_700_000 },
  { label: 'Coffee', kind: 'break', durationMS: 900_000 },
  { label: 'Panel', durationMS: 2_400_000 },
  { label: 'Lightning', durationMS: 1_800_000 },
  { label: 'Lunch', kind: 'break', durationMS: 3_600_000 },
];

export async function run(t) {
  const { ctx, H, room, event } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  await api('PUT', `/api/shows/${room}/cues`, CUES);
  await api('POST', `/api/shows/${room}/cmd/start`, { pos: 2 });
  const snap = async () => (await api('GET', `/api/shows/${room}`)).json();
  const cookies = await ctx.cookies();

  const open = async (opts) => {
    const c = await t.browser.newContext(opts);
    await c.addCookies(cookies);
    const pg = await c.newPage();
    const errs = [];
    pg.on('pageerror', (e) => errs.push(e.message));
    await pg.goto(`${t.base}/c/${room}`);
    await pg.waitForSelector('#cuelist tbody tr[data-pos="6"]');
    await pg.waitForTimeout(600);
    return { c, pg, errs };
  };
  const tappable = (pg, sel) => pg.$eval(sel, (e) => {
    const b = e.getBoundingClientRect();
    return [[b.left + b.width / 2, b.top + b.height / 2], [b.left + b.width * .3, b.top + b.height * .75], [b.left + b.width * .7, b.top + b.height * .75]]
      .every(([x, y]) => { const h = document.elementFromPoint(x, y); return !!h && (h === e || e.contains(h)); });
  });
  const overflow = (pg) => pg.evaluate(() => {
    const w = document.querySelector('.tp-cuelist-wrap');
    return { doc: document.documentElement.scrollWidth - innerWidth, wrap: w.scrollWidth - w.clientWidth };
  });

  // --- Phone 390×844 ---------------------------------------------------
  {
    const { c, pg, errs } = await open({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });
    const o = await overflow(pg);
    t.check(`phone: nothing overflows sideways (page ${o.doc}, table ${o.wrap})`, o.doc <= 0 && o.wrap <= 0);
    for (const s of ['.tp-transport-bar [data-cmd="go"]', '.tp-transport-bar [data-cmd="flash"]', '#tp-blank', '.tp-transport-bar [data-cmd="prev"]', '.tp-transport-bar [data-cmd="next"]']) {
      t.check(`phone: ${s} is tappable where it shows`, await tappable(pg, s));
    }
    const bar = await pg.$eval('.tp-transport-bar', (e) => e.getBoundingClientRect().height);
    t.check(`phone: the sticky transport is slim (${Math.round(bar)} px ≤ 130)`, bar <= 130);
    t.check('phone: the bar shows the time left', /\d:\d\d/.test(await pg.textContent('#tp-clock-mini')));
    const appBar = await pg.$eval('.app-bar', (e) => e.getBoundingClientRect().height);
    t.check(`phone: the app bar is one row (${Math.round(appBar)} px ≤ 72)`, appBar <= 72);
    // Focusing the message field keeps Send clear of the sticky bar.
    await pg.focus('form[data-cmd="addMsg"] input[name=text]');
    await pg.waitForTimeout(300);
    t.check('phone: Send is not parked under the transport', await tappable(pg, 'form[data-cmd="addMsg"] button[type=submit]'));
    // Each row has a visible ⋯ that opens the row menu.
    await pg.tap('#cuelist tbody tr[data-pos="3"] [data-row-menu]');
    await pg.waitForTimeout(300);
    t.check('phone: the row ⋯ opens the row menu', await pg.$eval('#tp-row-menu', (m) => m.matches(':popover-open')));
    t.check(`phone: no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
    await c.close();
  }

  // --- Tablet 1024×768 touch ------------------------------------------
  {
    const { c, pg, errs } = await open({ viewport: { width: 1024, height: 768 }, hasTouch: true });
    const o = await overflow(pg);
    t.check(`tablet: the running order fits (page ${o.doc}, table ${o.wrap})`, o.doc <= 0 && o.wrap <= 0);
    t.check('tablet: every row action, ⋯ included, is on screen', await pg.$$eval('#cuelist tbody [data-row-menu]', (n) => n.length > 0 && n.every((b) => { const r = b.getBoundingClientRect(); return r.width > 0 && r.right <= innerWidth; })));
    const dot = await pg.$eval('#cuelist tfoot [data-pick]', (b) => { b.scrollIntoView({ block: 'center' }); const r = b.getBoundingClientRect(); const h = document.elementFromPoint(r.left + r.width / 2, r.top - 10); return h === b; });
    t.check('tablet: the alert colour dot has a touch-sized hit area', dot);
    t.check(`tablet: no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
    await c.close();
  }

  // --- Desktop 1440×900 ------------------------------------------------
  const { c, pg, errs } = await open({ viewport: { width: 1440, height: 900 } });
  const fit = await pg.evaluate(() => ({ doc: document.documentElement.scrollHeight, bar: document.querySelector('#tp-daybar').getBoundingClientRect().bottom, foot: document.querySelector('.app-status').getBoundingClientRect().top }));
  t.check(`desktop: the page fits the window (${fit.doc} ≤ 900) and the day bar is above the footer (${Math.round(fit.bar)} ≤ ${Math.round(fit.foot)})`, fit.doc <= 900 && fit.bar <= fit.foot);
  t.check('desktop: the day bar is the ftl timeline', !!(await pg.$('#tp-daybar .timeline-bar[role="img"] .timeline-seg')));
  t.check('desktop: the clock is an ftl countdown with a state', await pg.$eval('#tp-clock', (e) => e.classList.contains('countdown') && e.dataset.state === 'running'));
  t.check('desktop: one loud GO — row GOs are tertiary', await pg.$$eval('#cuelist tbody [data-cmd="go"]', (n) => n.every((b) => b.classList.contains('btn-tertiary') && !b.classList.contains('btn-go'))));
  const states = await pg.$$eval('#cuelist tbody tr[data-pos]', (rs) => rs.map((r) => (r.classList.contains('is-past') ? 'past' : '') + (r.getAttribute('aria-current') === 'time' ? 'now' : '') + (r.classList.contains('is-next') ? 'next' : '')));
  t.check(`desktop: ftl row states (${states.join(',')})`, states[0] === 'past' && states[1] === 'now' && states[2] === 'next');
  t.check('desktop: the next row has no bright rule', await pg.$eval('#cuelist tr.is-next td', (td) => getComputedStyle(td).borderTopStyle !== 'solid' || getComputedStyle(td).borderTopColor !== getComputedStyle(td).color));

  // Durations: what the table shows is what you type.
  t.check('a 10-minute cue shows 0:10', (await pg.textContent('#cuelist tbody tr[data-pos="1"] .tp-cue-dur')).trim() === '0:10');
  await pg.fill('#cuelist tfoot input[name=label]', 'Typed');
  await pg.fill('#cuelist tfoot input[name=mss]', '10:00');
  await pg.press('#cuelist tfoot input[name=mss]', 'Enter');
  // Over 4 h asks first (UX N4); keep it here.
  await pg.waitForSelector('dialog.modal[open]');
  await pg.click('dialog.modal[open] .modal-footer .btn-primary');
  await pg.waitForSelector('#cuelist tbody tr[data-pos="7"]');
  const toastText = await pg.evaluate(() => [...document.querySelectorAll('.toast')].map((x) => x.textContent).join(' | '));
  t.check(`adding echoes the parsed duration (${toastText})`, /Typed.*10:00 \(10 h\)/.test(toastText));
  t.check('the new row shows 10:00 (ten hours, as typed)', (await pg.textContent('#cuelist tbody tr[data-pos="7"] .tp-cue-dur')).trim() === '10:00');

  // ↓ scrolls; it no longer changes the cue. Alt+↓ on the handle moves a row.
  await pg.click('.tp-cuelist-title');
  const before = (await snap()).runtime;
  await pg.keyboard.press('ArrowDown');
  await pg.keyboard.press('ArrowUp');
  await pg.waitForTimeout(500);
  const after = (await snap()).runtime;
  t.check('↑/↓ leave the running cue alone', after.activePos === before.activePos && after.anchorTS === before.anchorTS);
  await pg.focus('#cuelist tbody tr[data-pos="4"] td.drag-handle button');
  await pg.keyboard.press('Alt+ArrowDown');
  await pg.waitForTimeout(800);
  let order = (await snap()).cues.map((c) => c.label).join(',');
  t.check(`Alt+↓ moves the row down (${order})`, order.startsWith('Doors,Keynote,Coffee,Lightning,Panel'));
  t.check('the live region says where it went', /Moved Panel to position 5/.test(await pg.textContent('#tp-del-help')));
  t.check('focus follows the moved row', await pg.evaluate(() => document.activeElement?.closest('tr')?.dataset.pos === '5'));
  await pg.click('#cuelist tbody tr[data-pos="5"] td.tp-cue-notes', { button: 'right' });
  await pg.click('#tp-row-menu [data-row-act="up"]');
  await pg.waitForTimeout(800);
  order = (await snap()).cues.map((c) => c.label).join(',');
  t.check(`row menu Move up moves it back (${order})`, order.startsWith('Doors,Keynote,Coffee,Panel,Lightning'));
  const rt = (await snap()).runtime;
  t.check('reordering kept the running cue', rt.activePos === 2 && rt.anchorTS === before.anchorTS);

  // ftl two-tap delete.
  const del = '#cuelist tbody tr[data-pos="7"] [data-cmd="cueDel"]';
  await pg.click(del);
  t.check('the first tap arms the bin (.is-armed, "Delete?")', await pg.$eval(del, (b) => b.classList.contains('is-armed') && getComputedStyle(b.querySelector('.btn-armed-label')).display !== 'none'));
  await pg.click(del);
  await pg.waitForTimeout(800);
  t.check('the second tap deletes', (await snap()).cues.length === 6);

  // Import: Append by default (bare number = minutes); Replace asks first.
  await pg.click('.tp-import > summary');
  t.check('Import defaults to adding to the end', (await pg.inputValue('#import-mode')) === 'append');
  await pg.setInputFiles('#import-file', { name: 'more.csv', mimeType: 'text/csv', buffer: Buffer.from('Title,Speaker,Duration\nClosing,Ada,30\n') });
  await pg.click('.tp-import button[type=submit]');
  await pg.waitForTimeout(1200);
  let cues = (await snap()).cues;
  t.check(`append adds; "30" is 30 minutes (${cues.length}, ${cues.at(-1)?.durationMS})`, cues.length === 7 && cues.at(-1).label === 'Closing' && cues.at(-1).durationMS === 1_800_000);
  await pg.selectOption('#import-mode', 'replace');
  await pg.click('.tp-import button[type=submit]');
  await pg.waitForSelector('dialog.modal[open]');
  t.check('Replace asks in an ftl modal', !!(await pg.$('dialog.modal[open] .modal-footer .btn-secondary + .btn-danger')));
  await pg.click('dialog.modal[open] .modal-footer .btn-secondary');
  await pg.waitForTimeout(800);
  t.check('Cancel keeps the running order', (await snap()).cues.length === 7);

  // The cue editor fits: its Save button is on screen at 900 px.
  await pg.click('#cuelist tbody tr[data-pos="2"] [data-insp]');
  await pg.waitForSelector('#tp-inspector[open]');
  const save = await pg.$eval('#tp-insp-save', (b) => b.getBoundingClientRect().bottom);
  t.check(`the cue editor's Save is on screen (${Math.round(save)} ≤ 900)`, save <= 900);
  t.check(`desktop: no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  void event;
  await c.close();
  await ctx.close();
}
