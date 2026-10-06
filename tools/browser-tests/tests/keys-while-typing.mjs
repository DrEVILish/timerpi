// STATUS U13: typing into an inline cue cell never fires shortcuts (Space =
// GO, R = reset), even while a cue runs and the room state updates.
export const name = 'typing in a cue cell never triggers shortcuts';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: [{ label: 'Opening', durationMS: 1_800_000 }, { label: 'Talk', durationMS: 600_000 }], headers: H,
  });
  const pg = await ctx.newPage();
  const sent = [];
  pg.on('websocket', (ws) => ws.on('framesent', (f) => {
    const s = String(f.payload);
    if (s.includes('"cmd"')) sent.push(JSON.parse(s).action);
  }));
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tbody tr');
  await pg.keyboard.press(' '); // GO: a cue is running while we type
  await pg.waitForTimeout(600);
  await pg.dblclick('#cuelist tbody tr:nth-child(2) .tp-cue-label');
  sent.length = 0;
  for (const ch of 'Room R talk rr') { await pg.keyboard.type(ch); await pg.waitForTimeout(120); }
  t.check('the cell editor keeps focus while typing', await pg.evaluate(() => document.activeElement?.tagName === 'INPUT'));
  t.check(`no shortcut fired while typing (sent: ${sent.join(',') || 'none'})`, sent.length === 0);
  await pg.keyboard.press('Enter');
  await pg.waitForTimeout(600);
  t.check('the edit commits on Enter', sent.join(',') === 'cueEdit');
  const label = await pg.textContent('#cuelist tbody tr:nth-child(2) .tp-cue-label');
  t.check(`new title shows (${label?.trim()})`, label?.includes('Room R talk rr'));
  await ctx.close();
}
