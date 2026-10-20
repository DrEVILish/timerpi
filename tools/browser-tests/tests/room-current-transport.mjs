// The room page after the 2026-10-06 notes: Current cue reads title,
// clock, progress, adjust (with +5m), no state lamp/chip/delta; GO is 25%
// taller than its neighbours; Prev/Next are bordered ftl buttons; BLANK is
// danger. Cell edits redraw the row at once and queued messages stay in
// the Messages list (BUGLOG RW61).
export const name = 'room page: current cue, transport, live row and message updates';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: [{ label: 'Opening', durationMS: 600_000, speaker: 'Ada' }, { label: 'Coffee', kind: 'break', durationMS: 900_000 }], headers: H,
  });
  const snap = async () => (await (await ctx.request.get(`${t.base}/api/shows/${room}`)).json());
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.setViewportSize({ width: 1440, height: 900 });
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tbody tr[data-pos="2"]');

  // Current cue order.
  const order = await pg.$$eval('#tp-now > .stack > *', (n) => n.map((e) =>
    e.querySelector('#tp-cue-label') ? 'title' : e.id === 'tp-clock' ? 'clock' : e.id === 'tp-clock-status' ? 'status' : e.classList.contains('tp-progress') ? 'progress'
      : e.classList.contains('tp-adjust-row') ? 'adjust' : e.classList.contains('tp-rate') ? 'rate' : e.className));
  t.check(`current cue order: ${order.join(', ')}`, order.join(',') === 'title,clock,progress,adjust,rate,status');
  t.check('no state lamp, chip or vs-plan line', !(await pg.$('#tp-lamp-run, #tp-state-chip, #tp-delta')));
  const adj = await pg.$$eval('.tp-adjust-row [data-adjust]', (n) => n.map((b) => b.textContent.trim()));
  t.check(`adjust buttons: ${adj.join(' ')}`, adj.join(' ') === '−1m −30s +30s +1m +5m');

  // Transport.
  const box = async (s) => pg.$eval(s, (e) => { const r = e.getBoundingClientRect(); const cs = getComputedStyle(e); return { h: r.height, bw: parseFloat(cs.borderTopWidth), bc: cs.borderTopColor, cls: e.className }; });
  const prev = await box('.tp-transport-bar [data-cmd="prev"]');
  const next = await box('.tp-transport-bar [data-cmd="next"]');
  const go = await box('.tp-transport-bar [data-cmd="go"]');
  const blank = await box('#tp-blank');
  t.check(`GO is 25% taller than Prev (${go.h} vs ${prev.h})`, Math.abs(go.h / prev.h - 1.25) < 0.03);
  t.check('Prev and Next have a visible border', [prev, next].every((b) => b.bw >= 1 && !/rgba\(\d+, \d+, \d+, 0\)|transparent/.test(b.bc) && !/btn-ghost/.test(b.cls)));
  t.check('BLANK is a danger button', /\bbtn-danger\b/.test(blank.cls));

  // +5m adjusts the cue on the clock.
  await pg.click('.tp-transport-bar [data-cmd="go"]');
  await pg.waitForFunction(() => !document.querySelector('.tp-adjust-row [data-adjust="300000"]').disabled);
  await pg.click('.tp-adjust-row [data-adjust="300000"]');
  await pg.waitForTimeout(900);
  { const s = await snap(); t.check(`+5m lengthens the cue on the clock by five minutes (${s.runtime.activePos}: ${s.cues.map((c) => c.durationMS)})`, s.cues[0].durationMS === 900_000); }

  // Cell edits redraw the row without a reload.
  const cell = (c) => `#cuelist tbody tr[data-pos="2"] td.${c}`;
  const editText = async (c, v) => {
    await pg.dblclick(cell(c));
    await pg.fill(`${cell(c)} input`, v);
    await pg.press(`${cell(c)} input`, 'Enter');
  };
  await editText('tp-cue-who', 'Foyer');
  await editText('tp-cue-alert1', '1:00');
  await editText('tp-cue-notes', 'Urns on');
  await pg.dblclick(cell('tp-cue-atzero'));
  await pg.selectOption(`${cell('tp-cue-atzero')} select`, 'OVERTIME');
  await pg.waitForTimeout(900);
  const shown = await pg.$eval('#cuelist tbody tr[data-pos="2"]', (tr) => ['tp-cue-who', 'tp-cue-alert1', 'tp-cue-notes', 'tp-cue-atzero'].map((c) => tr.querySelector(`td.${c}`).textContent.trim()));
  t.check(`edited cells show the new values (${shown.join(' | ')})`, shown[0] === 'Foyer' && shown[1].startsWith('1:00') && shown[2] === 'Urns on' && shown[3] === 'Overtime');

  // Messages: Send puts each one on stage at once; Hide and Show work.
  const items = () => pg.$$eval('#messages-panel ul.list .list-item', (n) => n.map((li) => li.innerText.replace(/\s+/g, ' ').trim()));
  t.check('the message button says Send', (await pg.textContent('form[data-cmd="addMsg"] button[type=submit]')).trim() === 'Send');
  await pg.fill('form[data-cmd="addMsg"] input[name=text]', 'On now');
  await pg.click('form[data-cmd="addMsg"] button[type=submit]');
  await pg.waitForTimeout(600);
  await pg.fill('form[data-cmd="addMsg"] input[name=text]', 'Later');
  // Colour is an ftl swatch picker (re-mounted after the panel's swap).
  await pg.click('#messages-panel .swatches label.swatch[title="Red"]');
  await pg.click('form[data-cmd="addMsg"] button[type=submit]');
  await pg.waitForTimeout(900);
  t.check('the colour picker is back after the swap, keeping the picked colour (I12)', !!(await pg.$('#messages-panel .swatches input:checked[value="#ff4444"]')));
  let list = await items();
  // UX I9: one message on stage at a time; Send replaces it, the rest is history.
  t.check(`only the newest message is on stage (${list.join(' / ')})`, list.length === 2 && /^Later On stage/.test(list[0]) && /^On now Sent/.test(list[1]));
  await pg.click('#messages-panel .list-item:has-text("Later") [data-cmd="hideMsg"]');
  await pg.waitForTimeout(900);
  list = await items();
  t.check(`a hidden message stays listed (${list.join(' / ')})`, list.length === 2 && list.every((s) => /Sent/.test(s)));
  await pg.click('#messages-panel .list-item:has-text("On now") [data-cmd="showMsg"]');
  await pg.waitForTimeout(900);
  list = await items();
  t.check(`Show again puts it back, alone (${list.join(' / ')})`, list.length === 2 && /^On now On stage/.test(list[0]) && list.filter((s) => /On stage/.test(s)).length === 1);
  t.check('the first message has the theme colour', ((await snap()).messages ?? []).find((m) => m.text === 'On now')?.color === '');
  t.check('the badge counts both', (await pg.textContent('#messages-panel .badge')).trim() === '2');
  await pg.click('#messages-panel .list-item:has-text("Later") [data-cmd="showMsg"]');
  await pg.waitForTimeout(900);
  const later = ((await snap()).messages ?? []).map((m) => [m.text, m.color]);
  t.check(`message colours: theme by default, Red when picked (${JSON.stringify(later)})`, later.length === 1 && later[0][0] === 'Later' && later[0][1] === '#ff4444');
  t.check('footer connection is an ftl .connection, live', await pg.$eval('#conn-label', (e) => e.classList.contains('connection') && e.dataset.state === 'live'));
  await pg.click('[popovertarget="tp-theme-pop"]');
  t.check('Change Theme opens the ftl dropdown popover', await pg.$eval('#tp-theme-pop', (e) => e.matches(':popover-open') && !!e.querySelector('#theme-select option')));
  await pg.keyboard.press('Escape');
  t.check('no page errors', errs.length === 0);
}
