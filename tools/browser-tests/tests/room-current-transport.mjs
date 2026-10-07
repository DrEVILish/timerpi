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
    e.querySelector('#tp-cue-label') ? 'title' : e.id === 'tp-clock' ? 'clock' : e.classList.contains('tp-progress') ? 'progress'
      : e.classList.contains('tp-adjust-row') ? 'adjust' : e.classList.contains('tp-rate') ? 'rate' : e.className));
  t.check(`current cue order: ${order.join(', ')}`, order.join(',') === 'title,clock,progress,adjust,rate');
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

  // Messages: a queued message stays listed; show and hide work.
  const items = () => pg.$$eval('#messages-panel ul.list .list-item', (n) => n.map((li) => li.innerText.replace(/\s+/g, ' ').trim()));
  await pg.fill('form[data-cmd="addMsg"] input[name=text]', 'On now');
  await pg.click('form[data-cmd="addMsg"] button[type=submit]');
  await pg.waitForTimeout(600);
  await pg.fill('form[data-cmd="addMsg"] input[name=text]', 'Later');
  await pg.uncheck('form[data-cmd="addMsg"] input[name=show]');
  // Colour is an ftl swatch picker (re-mounted after the panel's swap).
  await pg.click('#messages-panel .swatches label.swatch[title="Red"]');
  await pg.click('form[data-cmd="addMsg"] button[type=submit]');
  await pg.waitForTimeout(900);
  t.check('the colour picker is back after the swap', !!(await pg.$('#messages-panel .swatches input:checked[value="#7C3AED"]')));
  let list = await items();
  t.check(`queued message is listed (${list.join(' / ')})`, list.length === 2 && list.some((s) => s.startsWith('Later') && /queued/.test(s)));
  await pg.click('#messages-panel .list-item:has-text("Later") [data-cmd="showMsg"]');
  await pg.waitForTimeout(900);
  list = await items();
  t.check(`showing a queued message keeps the list (${list.join(' / ')})`, list.length === 2 && list.some((s) => s.startsWith('Later') && /ON STAGE/.test(s)));
  t.check('the badge counts both', (await pg.textContent('#messages-panel .badge')).trim() === '2');
  const sj = await snap();
  const cols = Object.fromEntries((sj.messages ?? sj.snapshot?.messages ?? []).map((m) => [m.text, m.color]));
  t.check(`message colours: Brand by default, Red when picked (${JSON.stringify(cols)})`, cols['On now'] === '#7C3AED' && cols.Later === '#ff4444');
  t.check('footer connection is an ftl .connection, live', await pg.$eval('#conn-label', (e) => e.classList.contains('connection') && e.dataset.state === 'live'));
  await pg.click('[popovertarget="tp-theme-pop"]');
  t.check('Change Theme opens the ftl dropdown popover', await pg.$eval('#tp-theme-pop', (e) => e.matches(':popover-open') && !!e.querySelector('#theme-select option')));
  await pg.keyboard.press('Escape');
  t.check('no page errors', errs.length === 0);
}
