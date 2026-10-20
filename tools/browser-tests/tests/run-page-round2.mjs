// Run tab + cue editor, review round 2 (STATUS U50): inline edit keeps the
// table width, quiet delete with Undo, Session/Break in the add row, ask
// before a cue over 4 h, alerts in units, the custom colour is an ftl
// swatch, the import picker is an ftl dropzone, the cue editor's Save is on
// screen at 1024×768 and 390×844, and the phone page scrolls as one with
// WRAP UP in the sticky bar.
export const name = 'Run tab review round 2: fit, delete + Undo, add row, long cues, phone bar';

const CUES = [
  { label: 'Doors', durationMS: 600_000 },
  { label: 'Opening keynote: The next decade of live events', speaker: 'Dr Ada Byron', durationMS: 2_700_000, alert1MS: 300_000, alert2MS: 60_000 },
  { label: 'Coffee', kind: 'break', location: 'Great Hall', durationMS: 900_000 },
  { label: 'Panel: Running hybrid shows', speaker: 'M. Chen', durationMS: 2_400_000 },
  { label: 'Lightning talks', speaker: 'Various', durationMS: 90_000 },
  { label: 'Lunch', kind: 'break', location: 'Atrium', durationMS: 3_600_000 },
];

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
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
  // Every point of the element is the top hit (on screen, not covered).
  const tappable = (pg, sel) => pg.$eval(sel, (e) => {
    const b = e.getBoundingClientRect();
    return b.width > 0 && [[.5, .5], [.15, .5], [.85, .5]].every(([fx, fy]) => { const h = document.elementFromPoint(b.left + b.width * fx, b.top + b.height * fy); return !!h && (h === e || e.contains(h)); });
  });
  const saveOnScreen = (pg) => pg.$eval('#tp-insp-save', (b) => { const r = b.getBoundingClientRect(); const h = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); return r.bottom <= innerHeight && (h === b || b.contains(h)); });

  // --- Desktop 1440×900 -----------------------------------------------
  {
    const { c, pg, errs } = await open({ viewport: { width: 1440, height: 900 } });
    const width = () => pg.$eval('#cuelist', (x) => x.scrollWidth);
    const w0 = await width();
    await pg.dblclick('#cuelist tbody tr[data-pos="4"] td.tp-cue-title');
    await pg.fill('#cuelist tbody tr[data-pos="4"] td.tp-cue-title input', 'A much longer panel title that keeps going and going');
    const w1 = await width();
    t.check(`inline edit keeps the table width (${w0} → ${w1}) (UI #1)`, w1 <= w0 + 1);
    t.check('inline edit: the row actions stay on screen', await tappable(pg, '#cuelist tbody tr[data-pos="4"] [data-cmd="go"]'));
    await pg.keyboard.press('Escape');

    const th = await pg.$$eval('#cuelist thead th', (n) => [...new Set(n.filter((x) => x.offsetParent).map((x) => getComputedStyle(x).fontSize))]);
    t.check(`every header is the same size (${th.join(', ')}) (UI #11)`, th.length === 1);
    const borders = await pg.evaluate(() => ['input[name=label]', 'input[name=alert1]'].map((s) => getComputedStyle(document.querySelector(`#cuelist tfoot ${s}`)).borderTopColor));
    t.check(`add-row alert inputs wear the editing border (${borders.join(' / ')}) (UI #12)`, borders[0] === borders[1]);
    t.check('alerts read in units ("5m", "1m") (UX N4)', (await pg.textContent('#cuelist tbody tr[data-pos="2"] .tp-cue-alert1')).trim() === '5m' && (await pg.textContent('#cuelist tbody tr[data-pos="2"] .tp-cue-alert2')).trim() === '1m');
    t.check('90 s reads "1m 30s", not 0:01:30 (UX N4)', (await pg.textContent('#cuelist tbody tr[data-pos="5"] .tp-cue-dur')).trim() === '1m 30s');
    t.check('the add row says alerts are minutes ("5m")', (await pg.getAttribute('#cuelist tfoot input[name=alert1]', 'placeholder')) === '5m');
    t.check('row delete is a quiet ghost icon, not a red button (UI #15)', await pg.$$eval('#cuelist tbody [data-cmd="cueDel"]', (n) => n.every((b) => !b.classList.contains('btn-danger') && b.classList.contains('btn-ghost'))));

    // Right-click opens the row menu without lighting its first item (UI #13).
    await pg.click('#cuelist tbody tr[data-pos="3"] td.tp-cue-pos', { button: 'right' });
    await pg.waitForTimeout(200);
    t.check('a pointer-opened row menu leaves its first item unlit', await pg.evaluate(() => !document.querySelector('#tp-row-menu').contains(document.activeElement)));
    await pg.keyboard.press('Escape');

    // Over 4 h asks first; Cancel adds nothing (UX N4).
    await pg.fill('#cuelist tfoot input[name=label]', 'Doors again');
    await pg.fill('#cuelist tfoot input[name=mss]', '10:00');
    await pg.press('#cuelist tfoot input[name=mss]', 'Enter');
    await pg.waitForSelector('dialog.modal[open]');
    const ask = await pg.textContent('dialog.modal[open]');
    t.check(`a 10-hour cue asks first (${ask.replace(/\s+/g, ' ').trim().slice(0, 90)}…)`, /10 h/.test(ask) && /type 10 or 10m/.test(ask));
    await pg.click('dialog.modal[open] .modal-footer .btn-secondary');
    await pg.waitForTimeout(500);
    t.check('Cancel adds nothing and keeps what was typed', (await snap()).cues.length === 6 && (await pg.inputValue('#cuelist tfoot input[name=mss]')) === '10:00');
    // The inline duration editor asks too.
    await pg.dblclick('#cuelist tbody tr[data-pos="1"] td.tp-cue-dur');
    await pg.fill('#cuelist tbody tr[data-pos="1"] td.tp-cue-dur input', '6:00');
    await pg.press('#cuelist tbody tr[data-pos="1"] td.tp-cue-dur input', 'Enter');
    await pg.waitForSelector('dialog.modal[open]');
    await pg.click('dialog.modal[open] .modal-footer .btn-secondary');
    await pg.waitForTimeout(500);
    t.check('the inline editor asks before 6 h; Cancel keeps 10 min', (await snap()).cues[0].durationMS === 600_000);

    // A Break, then the type goes back to Session (UX N5).
    await pg.fill('#cuelist tfoot input[name=mss]', '15');
    await pg.selectOption('#cuelist tfoot select[name=kind]', 'break');
    await pg.press('#cuelist tfoot input[name=mss]', 'Enter');
    await pg.waitForSelector('#cuelist tbody tr[data-pos="7"]');
    t.check('the break was added as a break', (await snap()).cues[6].kind === 'break');
    t.check('the add row is back to Session', (await pg.inputValue('#cuelist tfoot select[name=kind]')) === 'session');

    // Two-tap delete offers Undo (UX N11).
    const del = '#cuelist tbody tr[data-pos="7"] [data-cmd="cueDel"]';
    await pg.click(del);
    await pg.click(del);
    await pg.waitForTimeout(700);
    t.check('the second tap deletes', (await snap()).cues.length === 6);
    await pg.click('.toast button:text("Undo")');
    await pg.waitForTimeout(800);
    t.check('Undo in the toast brings the cue back', (await snap()).cues.length === 7);

    // Import: ftl dropzone + button, no native file button (UI #7); closed,
    // the panel is just its header (UI #6).
    const imp = await pg.evaluate(() => { const d = document.querySelector('.tp-import'); return { d: d.getBoundingClientRect().height, h: d.querySelector('summary').getBoundingClientRect().height }; });
    t.check(`the closed Import panel is its header (${Math.round(imp.d)} vs ${Math.round(imp.h)})`, imp.d - imp.h <= 4);
    await pg.click('.tp-import > summary');
    t.check('Import uses an ftl dropzone with a Choose file button', !!(await pg.$('.tp-import .dropzone label.btn #import-file[hidden]')));

    // Cue editor: the custom colour is the last ftl swatch (UI #3), alert
    // colours are ftl .color-dot pickers, Save is on screen.
    await pg.click('#cuelist tbody tr[data-pos="2"] [data-insp]');
    await pg.waitForSelector('#tp-inspector[open]');
    t.check('the row colour picker ends with an ftl swatch for any colour (no bare colour input)', await pg.$eval('#tp-inspector .tp-insp-rowcolor fieldset.swatches', (f) => f.lastElementChild.matches('label.swatch') && !!f.lastElementChild.querySelector('input[type=color]') && !f.querySelector(':scope > input[type=color]')));
    t.check('alert 1 shows "5m"', (await pg.inputValue('#tp-insp-alert1')) === '5m');
    t.check('desktop: Save is on screen', await saveOnScreen(pg));
    await pg.fill('#tp-insp-duration', '5:00');
    await pg.click('#tp-insp-save');
    await pg.waitForSelector('dialog.modal[open]:not(#tp-inspector)');
    await pg.click('dialog.modal[open]:not(#tp-inspector) .modal-footer .btn-primary');
    await pg.waitForTimeout(700);
    t.check('the cue editor asks before 5 h, then saves it', (await snap()).cues[1].durationMS === 5 * 3_600_000);
    t.check(`desktop: no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
    await c.close();
  }
  await api('PUT', `/api/shows/${room}/cues`, CUES);
  await api('POST', `/api/shows/${room}/cmd/start`, { pos: 2 });

  // --- Tablet 1024×768 touch --------------------------------------------
  {
    const { c, pg, errs } = await open({ viewport: { width: 1024, height: 768 }, hasTouch: true });
    const visible = await pg.$$eval('#cuelist tbody tr[data-pos="2"] .tp-cue-rowbtns .btn', (n) => n.filter((b) => b.offsetParent).map((b) => b.dataset.cmd || (b.dataset.rowMenu ? 'more' : '?')));
    t.check(`tablet rows keep only Go and ⋯ (${visible.join(',')}) (UI #15)`, visible.join(',') === 'go,more');
    const rows = await pg.$$eval('#cuelist tbody tr[data-pos]', (n) => n.filter((r) => r.getBoundingClientRect().bottom <= innerHeight - 28).length);
    t.check(`tablet: at least 4 rows show on load (${rows})`, rows >= 4);
    // Break toggle in the add row (UX N5).
    await pg.$eval('#cuelist tfoot', (f) => f.scrollIntoView({ block: 'center' }));
    t.check('tablet: the add row has a Break toggle', await tappable(pg, '#cuelist tfoot .tp-add-break'));
    await pg.fill('#cuelist tfoot input[name=label]', 'Tea');
    await pg.tap('#cuelist tfoot .tp-add-break');
    await pg.tap('#cuelist tfoot button[type=submit]');
    await pg.waitForSelector('#cuelist tbody tr[data-pos="7"]');
    t.check('tablet: the toggle adds a break', (await snap()).cues[6].kind === 'break');
    t.check('tablet: the toggle resets after the add', !(await pg.isChecked('#cuelist tfoot [name=isBreak]')));
    // Cue editor fits: Save on screen without scrolling (UX N3, UI #18).
    await pg.tap('#cuelist tbody tr[data-pos="2"] [data-row-menu]');
    await pg.tap('#tp-row-menu [data-row-act="details"]');
    await pg.waitForSelector('#tp-inspector[open]');
    await pg.waitForTimeout(200);
    t.check('tablet: the cue editor Save is on screen', await saveOnScreen(pg));
    t.check('tablet: the editor does not scroll', await pg.$eval('#tp-inspector', (d) => d.scrollHeight <= d.clientHeight + 1));
    await pg.keyboard.press('Escape');
    t.check(`tablet: no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
    await c.close();
  }

  await api('PUT', `/api/shows/${room}/cues`, CUES);
  await api('POST', `/api/shows/${room}/cmd/start`, { pos: 2 });

  // --- Phone 390×844 ------------------------------------------------------
  {
    const { c, pg, errs } = await open({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });
    const o = await pg.evaluate(() => { const w = document.querySelector('#cuelist'); return { sw: w.scrollWidth, cw: w.clientWidth, inner: getComputedStyle(w).overflowY }; });
    t.check(`phone: the table fits (${o.sw} ≤ ${o.cw})`, o.sw <= o.cw);
    const rows = await pg.$$eval('#cuelist tbody tr[data-pos]', (n) => { const top = document.querySelector('.tp-transport-bar').getBoundingClientRect().top; return n.filter((r) => r.getBoundingClientRect().bottom <= top).length; });
    t.check(`phone: 4+ running-order rows show above the sticky bar on load (${rows})`, rows >= 4);
    t.check(`phone: the running order has no scroller of its own (${o.inner}) (UX N8)`, o.inner !== 'auto' && o.inner !== 'scroll');
    const plus = await pg.$eval('#cuelist tfoot button[type=submit]', (b) => { const r = b.getBoundingClientRect(), w = document.querySelector('#cuelist').getBoundingClientRect(); return r.right <= w.right + .5 && r.left >= w.left; });
    t.check('phone: the add button is whole (UI #5)', plus);
    t.check('phone: the add row has a Break toggle', await pg.$eval('#cuelist tfoot .tp-add-break', (e) => { const r = e.getBoundingClientRect(), w = document.querySelector('#cuelist').getBoundingClientRect(); return r.width > 0 && r.right <= w.right + .5; }));
    // WRAP UP rides the sticky bar (UX N9).
    t.check('phone: WRAP UP is in the sticky bar and tappable', await tappable(pg, '.tp-transport-bar .tp-bar-wrapup'));
    for (const s of ['[data-cmd="go"]', '[data-cmd="flash"]', '#tp-blank', '[data-cmd="prev"]', '[data-cmd="next"]', '[data-cmd="pause"]']) {
      t.check(`phone: ${s} is tappable`, await tappable(pg, `.tp-transport-bar ${s}`));
    }
    const bar = await pg.$eval('.tp-transport-bar', (e) => e.getBoundingClientRect().height);
    t.check(`phone: the bar stays slim (${Math.round(bar)} ≤ 130)`, bar <= 130);
    await pg.tap('.tp-transport-bar .tp-bar-wrapup');
    await pg.waitForTimeout(700);
    t.check('phone: the bar WRAP UP puts the message on stage', ((await snap()).messages || []).some((m) => /WRAP UP/.test(m.text) && m.shownAt > 0));
    // Scrolled: the table header sticks under the tabs, not under them.
    await pg.evaluate(() => { const r = document.querySelector('#cuelist tbody tr[data-pos="4"]').getBoundingClientRect(); scrollBy(0, r.top - 140); });
    await pg.waitForTimeout(300);
    const st = await pg.evaluate(() => ({ tabs: document.querySelector('.tp-room-tabs').getBoundingClientRect().bottom, th: document.querySelector('#cuelist thead th.tp-cue-title').getBoundingClientRect().top }));
    t.check(`phone: the sticky header sits under the tabs (${Math.round(st.th)} ≥ ${Math.round(st.tabs)} - 1, ≤ +6)`, st.th >= st.tabs - 1 && st.th <= st.tabs + 6);
    // Delete from the row menu, then Undo; the toast is clear of the tabs.
    await pg.tap('#cuelist tbody tr[data-pos="5"] [data-row-menu]');
    await pg.waitForTimeout(200);
    t.check('phone: the row menu opens without lighting an item (UI #13)', await pg.evaluate(() => !document.querySelector('#tp-row-menu').contains(document.activeElement)));
    await pg.tap('#tp-row-menu [data-row-act="del"]');
    await pg.waitForSelector('.toast button');
    const tr = await pg.evaluate(() => ({ toast: document.querySelector('.toast').getBoundingClientRect(), tabs: document.querySelector('.tp-room-tabs').getBoundingClientRect(), bar: document.querySelector('.tp-transport-bar').getBoundingClientRect() }));
    t.check(`phone: the toast sits above the transport, clear of the tabs (${Math.round(tr.toast.top)}–${Math.round(tr.toast.bottom)}, bar ${Math.round(tr.bar.top)}) (UI #10)`, tr.toast.top > tr.tabs.bottom && tr.toast.bottom <= tr.bar.top);
    t.check('phone: the menu delete deleted', (await snap()).cues.length === 5);
    await pg.tap('.toast button');
    await pg.waitForTimeout(800);
    t.check('phone: Undo brings it back (UX N11)', (await snap()).cues.length === 6);
    // Cue editor: Save on screen; selects are not cut under the chevron.
    await pg.evaluate(() => scrollTo(0, 0));
    await pg.tap('#cuelist tbody tr[data-pos="2"] [data-row-menu]');
    await pg.tap('#tp-row-menu [data-row-act="details"]');
    await pg.waitForSelector('#tp-inspector[open]');
    await pg.waitForTimeout(200);
    t.check('phone: the cue editor Save is on screen', await saveOnScreen(pg));
    const clip = await pg.$$eval('#tp-inspector select', (n) => n.filter((s) => s.offsetParent).map((s) => { const cs = getComputedStyle(s); const m = document.createElement('span'); m.style.font = cs.font; m.style.letterSpacing = cs.letterSpacing; m.style.position = 'absolute'; m.textContent = s.selectedOptions[0].text; document.body.appendChild(m); const need = m.getBoundingClientRect().width + parseFloat(cs.paddingLeft) + parseFloat(cs.paddingRight); m.remove(); return need <= s.clientWidth + 1 ? '' : s.id; }).filter(Boolean));
    t.check(`phone: no cue-editor select is clipped (${clip.join(',') || 'none'}) (UI #2)`, clip.length === 0);
    t.check(`phone: no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
    await c.close();
  }
  await ctx.close();
}
