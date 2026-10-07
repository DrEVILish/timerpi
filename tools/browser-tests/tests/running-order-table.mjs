// STATUS U30, U39–U41, U43: the running order is one table. The footer row
// adds a cue with every column; cells edit inline (inputs and selects);
// alert colours use ftl swatches; Duplicate is in the right-click /
// long-press menu; no arrows or duplicate icon; breaks have their own
// accent; typing in the add row survives live updates.
export const name = 'running order table: add row, inline cells, swatches, row menu';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: [{ label: 'Opening', durationMS: 600_000, speaker: 'Ada' }, { label: 'Coffee', kind: 'break', durationMS: 900_000, location: 'Foyer' }], headers: H,
  });
  const snap = async () => (await (await ctx.request.get(`${t.base}/api/shows/${room}`)).json());
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tbody tr[data-pos="2"]');

  const heads = await pg.$$eval('#cuelist thead th', (n) => n.map((th) => th.textContent.replace(/Drag to reorder, |Actions/, '').trim()));
  t.check(`columns: ${heads.join(' | ')}`, heads.join('|') === '#|Type|Title|Speaker / Where|Duration|Start|End|Timer|At zero|Alert 1|Alert 2|Notes|');
  t.check('no up/down arrows or duplicate icon on rows', !(await pg.$('#cuelist [data-dir], #cuelist [data-cmd="cueDup"]')));

  // Add row: every column.
  const f = (n) => `[form="tp-add-form"][name="${n}"]`;
  await pg.fill(f('label'), 'Keynote');
  await pg.fill(f('who'), 'Grace Hopper');
  await pg.fill(f('mss'), '45');
  await pg.selectOption(f('timerKind'), 'COUNTSTOP');
  await pg.selectOption(f('endAction'), 'OVERTIME');
  await pg.fill(f('alert1'), '5:00');
  await pg.click('#cuelist tfoot [data-pick="alertColor1"]');
  await pg.waitForSelector('#tp-color-pop:popover-open .swatches');
  await pg.click('#tp-color-pop label.swatch[title="Purple"]');
  await pg.fill(f('notes'), 'Lights down');
  await pg.press(f('notes'), 'Enter');
  await pg.waitForTimeout(900);
  const k = (await snap()).cues.find((c) => c.label === 'Keynote');
  t.check(`add row stores every column (${JSON.stringify(k && [k.speaker, k.durationMS, k.timerKind, k.endAction, k.alert1MS, k.alertColor1, k.notes])})`,
    k && k.speaker === 'Grace Hopper' && k.durationMS === 2_700_000 && k.timerKind === 'COUNTSTOP' && k.endAction === 'OVERTIME'
    && k.alert1MS === 300_000 && k.alertColor1 === '#a855f7' && k.notes === 'Lights down');
  t.check('the title field is ready for the next cue', await pg.evaluate(() => document.activeElement?.name === 'label' && document.activeElement.value === ''));

  // Typing in the add row survives a live update from elsewhere.
  await pg.type(f('label'), 'Half typ');
  // Another operator adds a cue: the table is swapped under us.
  const pg2 = await ctx.newPage();
  await pg2.goto(`${t.base}/c/${room}`);
  await pg2.waitForSelector(f('label'));
  await pg2.fill(f('label'), 'Extra');
  await pg2.press(f('label'), 'Enter');
  await pg.waitForFunction(() => document.querySelectorAll('#cuelist tbody tr[data-pos]').length === 4, null, { timeout: 5000 });
  await pg2.close();
  await pg.waitForTimeout(300);
  await pg.keyboard.type('ed');
  const kept = await pg.evaluate(() => ({ v: document.activeElement?.value, n: document.activeElement?.name }));
  t.check(`the add row keeps typing and focus through updates (${kept.v})`, kept.n === 'label' && kept.v === 'Half typed');
  await pg.fill(f('label'), '');

  // Inline cells are ftl editable cells; a bad value on Enter keeps the
  // editor open with aria-invalid, Escape reverts.
  t.check('cells are td.is-editable', (await pg.$$('#cuelist tbody tr[data-pos="1"] td.is-editable')).length === 9);
  await pg.dblclick('#cuelist tbody tr[data-pos="1"] td.tp-cue-dur');
  await pg.keyboard.press('Control+a');
  await pg.keyboard.type('zz');
  await pg.keyboard.press('Enter');
  t.check('a bad duration stays in the editor, marked invalid',
    !!(await pg.$('#cuelist tbody tr[data-pos="1"] td.tp-cue-dur.is-editing > input[aria-invalid="true"]')));
  await pg.keyboard.press('Escape');
  t.check('Escape closes the editor', !(await pg.$('#cuelist tbody td.is-editing')));
  // Inline cells: a select and an alert.
  await pg.dblclick('#cuelist tbody tr[data-pos="1"] td.tp-cue-timer');
  await pg.selectOption('#cuelist tbody tr[data-pos="1"] td.tp-cue-timer select', 'CLOCK');
  await pg.waitForTimeout(700);
  await pg.dblclick('#cuelist tbody tr[data-pos="1"] td.tp-cue-alert2');
  await pg.keyboard.press('Control+a');
  await pg.keyboard.type('2:00');
  await pg.keyboard.press('Enter');
  await pg.waitForSelector('#cuelist tbody tr[data-pos="1"] td.tp-cue-alert2.is-saved', { timeout: 2000 }).catch(() => {});
  t.check('the saved cell flashes .is-saved', !!(await pg.$('#cuelist tbody tr[data-pos="1"] td.tp-cue-alert2.is-saved')));
  await pg.waitForTimeout(700);
  // Row alert dot: pick a colour.
  await pg.click('#cuelist tbody tr[data-pos="1"] td.tp-cue-alert2 [data-color]');
  await pg.click('#tp-color-pop label.swatch[title="Green"]');
  await pg.waitForTimeout(700);
  const o = (await snap()).cues[0];
  t.check(`inline select, alert and colour saved (${o.timerKind}, ${o.alert2MS}, ${o.alertColor2})`, o.timerKind === 'CLOCK' && o.alert2MS === 120_000 && o.alertColor2 === '#16a34a');
  t.check('the cell shows the new timer', (await pg.textContent('#cuelist tbody tr[data-pos="1"] td.tp-cue-timer')).trim() === 'Clock');

  // Right-click → Duplicate.
  await pg.click('#cuelist tbody tr[data-pos="2"] td.tp-cue-title', { button: 'right' });
  await pg.waitForSelector('#tp-row-menu:popover-open');
  await pg.click('#tp-row-menu [data-row-act="dup"]');
  await pg.waitForTimeout(900);
  let labels = (await snap()).cues.map((c) => c.label);
  t.check(`right-click Duplicate copies after the row (${labels.join(',')})`, labels.join(',') === 'Opening,Coffee,Coffee,Keynote,Extra');
  // Long press (touch) opens the same menu.
  const box = await (await pg.$('#cuelist tbody tr[data-pos="4"] td.tp-cue-notes')).boundingBox();
  await pg.dispatchEvent('#cuelist tbody tr[data-pos="4"] td.tp-cue-notes', 'pointerdown', { pointerType: 'touch', clientX: box.x + 5, clientY: box.y + 5, isPrimary: true });
  await pg.waitForTimeout(800);
  t.check('a long press opens the row menu', await pg.$eval('#tp-row-menu', (m) => m.matches(':popover-open')));
  await pg.keyboard.press('Escape');

  // Breaks have their own accent.
  const accent = await pg.evaluate(() => {
    const cs = (sel) => getComputedStyle(document.querySelector(sel));
    return { brk: cs('#cuelist tr.tp-row-break td:first-child').boxShadow, ses: cs('#cuelist tr.tp-row-session td:first-child').boxShadow,
      bb: cs('#cuelist tr.tp-row-break .tp-cue-kind .badge').color, sb: cs('#cuelist tr.tp-row-session .tp-cue-kind .badge').color };
  });
  t.check(`breaks wear a different accent (${accent.bb} vs ${accent.sb})`, accent.brk !== 'none' && accent.brk !== accent.ses && accent.bb !== accent.sb);

  // Details panel: swatches, no Hold after / auto-continue / auto-start.
  await pg.click('#cuelist tbody tr[data-pos="1"] [data-insp]');
  await pg.waitForSelector('#tp-inspector[open]');
  t.check('details panel has three swatch pickers', (await pg.$$('#tp-inspector fieldset.swatches')).length === 3);
  t.check('no Hold after, auto-continue or auto-start', !(await pg.$('#tp-insp-hold, #tp-insp-autoContinue, #tp-insp-startAt')));
  await pg.click('[data-swatches-for="tp-insp-alert1Color"] label.swatch[title="Red"]');
  await pg.click('#tp-insp-save');
  await pg.waitForTimeout(700);
  t.check('a swatch picked in the details panel saves', (await snap()).cues[0].alertColor1 === '#ff4444');
  // The whole table fits a 1440 px screen beside the Current cue pane,
  // with the day running (planned times read HH:MM).
  await ctx.request.post(`${t.base}/api/shows/${room}/cmd/start`, { data: { pos: 1 }, headers: H });
  await pg.setViewportSize({ width: 1440, height: 900 });
  await pg.waitForTimeout(800);
  const fit = await pg.$eval('#cuelist', (x) => ({ sw: x.scrollWidth, cw: x.clientWidth }));
  // BUGLOG RW60: once the day has a start, the rows show clock times.
  const starts = await pg.$$eval('#cuelist tbody td.tp-cue-start', (n) => n.map((x) => x.textContent.trim()));
  t.check(`planned starts are clock times after GO (${starts.join(', ')})`, starts.every((x) => /^\d\d:\d\d$/.test(x)));
  t.check(`the table fits at 1440 px (${fit.sw} ≤ ${fit.cw})`, fit.sw <= fit.cw + 1);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
