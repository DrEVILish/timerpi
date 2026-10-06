// STATUS U16: editing the running cue (its title inline, or its alert in
// the details panel) never resets or advances its countdown.
export const name = 'editing the running cue keeps its countdown';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: [{ label: 'Keynote', durationMS: 1_800_000 }, { label: 'Next up', durationMS: 600_000 }], headers: H,
  });
  const snap = async () => (await (await ctx.request.get(`${t.base}/api/shows/${room}`)).json());
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tbody tr');
  await pg.keyboard.press(' '); // GO
  await pg.waitForTimeout(1500);
  const before = (await snap()).runtime;
  t.check('the first cue is running', before.running && before.activePos === 1);

  // Inline title edit with letters that are also shortcuts (r, p, space).
  await pg.dblclick('#cuelist tbody tr:nth-child(1) .tp-cue-label');
  await pg.keyboard.press('Control+A');
  await pg.keyboard.type('Keynote: rapid progress review');
  await pg.keyboard.press('Enter');
  await pg.waitForTimeout(800);

  // Alert edit in the details panel.
  await pg.hover('#cuelist tbody tr:nth-child(1)'); // row buttons show on hover
  await pg.click('#cuelist tbody tr:nth-child(1) [data-insp]');
  await pg.fill('#tp-insp-alert1', '5:00');
  await pg.click('#tp-insp-save');
  await pg.waitForTimeout(800);

  const after = await snap();
  const rt = after.runtime;
  t.check('title saved', after.cues[0].label === 'Keynote: rapid progress review');
  t.check(`alert "5:00" saved as 5 minutes (${after.cues[0].alert1MS} ms)`, after.cues[0].alert1MS === 300_000);
  t.check(`saving the details panel keeps the 30-minute duration (${after.cues[0].durationMS} ms)`, after.cues[0].durationMS === 1_800_000);
  t.check('still the same cue, still running', rt.running && rt.activePos === 1 && !rt.paused);
  t.check(`countdown not restarted (anchor ${before.anchorTS} → ${rt.anchorTS})`, rt.anchorTS === before.anchorTS && rt.pausedElapsedMS === before.pausedElapsedMS);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
