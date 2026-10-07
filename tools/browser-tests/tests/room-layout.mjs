// STATUS U31–U38: the room page layout. Transport above the running order
// (no Stop/Reset), adjust under the readout, the readout fits with tenths,
// time of day centred in the header, no key hint / filter / undo button /
// Next up, Messages then Import under the Current cue. Ctrl+Z still undoes.
export const name = 'room page layout (U31–U38)';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: [{ label: 'Short', durationMS: 12_000 }, { label: 'Talk', durationMS: 600_000 }], headers: H,
  });
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tbody tr');
  const box = async (sel) => pg.$eval(sel, (e) => { const r = e.getBoundingClientRect(); return { x: r.x, y: r.y, w: r.width, h: r.height, r: r.right, b: r.bottom }; });

  // U31: transport above the running order, five controls, no reset.
  const bar = await box('.tp-transport-bar');
  const order = await box('.tp-runorder');
  t.check('the transport sits above the running order', bar.b <= order.y + 1 && Math.abs(bar.x - order.x) < 2);
  const cmds = await pg.$$eval('.tp-transport-bar [data-cmd], .tp-transport-bar #tp-blank', (n) => n.map((b) => b.dataset.cmd || b.id));
  t.check(`transport: ${cmds.join(',')}`, cmds.join(',') === 'prev,go,pause,next,flash,tp-blank');
  t.check('no Stop/Reset button anywhere', (await pg.$$('[data-cmd="reset"]')).length === 0);

  // U32, revised 2026-10-06: readout, then its progress bar, then adjust.
  const after = await pg.$eval('#tp-clock', (c) => [c.nextElementSibling?.className, c.nextElementSibling?.nextElementSibling?.className].join(' '));
  t.check(`progress, then the adjust row, follow the readout (${after})`, /^tp-progress .*tp-adjust-row/.test(after));

  // U34: time of day at the top centre of the header bar.
  const tod = await box('.app-bar #tp-tod');
  const vw = await pg.evaluate(() => document.documentElement.clientWidth);
  t.check(`the clock is centred in the header (${Math.round(tod.x + tod.w / 2)} of ${vw})`, Math.abs(tod.x + tod.w / 2 - vw / 2) < 4);
  await pg.waitForTimeout(1200);
  t.check('the header clock ticks', /\d:\d\d/.test(await pg.textContent('#tp-tod')));

  // U35, U36, U38: gone.
  const text = await pg.evaluate(() => document.body.innerText);
  t.check('no key hint line', !/Space GO/.test(text) && !(await pg.$('.tp-kbd-hint')));
  t.check('no filter box', !(await pg.$('#tp-cue-filter')));
  t.check('no undo button', !(await pg.$('#tp-undo')));
  t.check('no Next up in the Current cue pane', !/next up/i.test(await pg.textContent('#tp-now')));

  // U37: Current cue, Messages, Import stacked in the left column.
  const now = await box('#tp-now');
  const msgs = await box('#messages-panel');
  const imp = await box('.tp-import');
  t.check('Messages sit under the Current cue', Math.abs(msgs.x - now.x) < 2 && msgs.y >= now.b);
  t.check('Import sits under Messages', Math.abs(imp.x - now.x) < 2 && imp.y >= msgs.b);

  // U33: under 10 s the tenths show and stay inside the pane.
  await pg.keyboard.press(' ');
  await pg.waitForFunction(() => /\.\d$/.test(document.getElementById('tp-clock')?.textContent || ''), null, { timeout: 8000 });
  const clock = await box('#tp-clock');
  const nowBox = await box('#tp-now');
  const reading = await pg.textContent('#tp-clock');
  t.check(`"${reading}" fits inside the Current cue pane (${Math.round(clock.r)} ≤ ${Math.round(nowBox.r)})`, clock.r <= nowBox.r);
  // Narrow window: still fits.
  await pg.setViewportSize({ width: 1000, height: 800 });
  await pg.waitForTimeout(300);
  const c2 = await box('#tp-clock'); const n2 = await box('#tp-now');
  t.check('still fits at 1000 px wide', c2.r <= n2.r);
  await pg.setViewportSize({ width: 1280, height: 900 });

  // R no longer resets.
  const before = await (await ctx.request.get(`${t.base}/api/shows/${room}`)).json();
  await pg.keyboard.press('r');
  await pg.waitForTimeout(500);
  const afterR = await (await ctx.request.get(`${t.base}/api/shows/${room}`)).json();
  t.check('R does not reset the running cue', afterR.runtime.anchorTS === before.runtime.anchorTS);

  // Ctrl+Z still undoes an edit made on this page.
  await pg.dblclick('#cuelist tbody tr:nth-child(2) .tp-cue-label');
  await pg.keyboard.press('Control+a');
  await pg.keyboard.type('Renamed');
  await pg.keyboard.press('Enter');
  await pg.waitForTimeout(800);
  await pg.click('.tp-cuelist-title');
  await pg.keyboard.press('Control+z');
  await pg.waitForTimeout(800);
  const s3 = await (await ctx.request.get(`${t.base}/api/shows/${room}`)).json();
  t.check(`Ctrl+Z undoes the rename (${s3.cues[1].label})`, s3.cues[1].label === 'Talk');
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
