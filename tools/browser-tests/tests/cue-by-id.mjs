// E2E #6: running-order commands carry the cue id, so an operator acting on
// a stale view can't hit whichever cue now sits at that slot.
export const name = 'cue commands carry the cue id';

export async function run(t) {
  const { ctx, room } = await t.newEvent();
  await ctx.addInitScript(() => {
    window.__sent = [];
    const send = WebSocket.prototype.send;
    WebSocket.prototype.send = function (d) { window.__sent.push(String(d)); return send.call(this, d); };
  });
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForFunction(() => window.__tpmesh?.serverOnline());
  await pg.evaluate(() => {
    for (const label of ['Keynote', 'Panel', 'Lunch']) window.__tpmesh.sendCommand('cueAdd', { label, durationMS: 600000 });
  });
  await pg.waitForFunction(() => document.querySelectorAll('#cuelist tbody tr[data-cue-id]').length === 3);
  const ids = await pg.$$eval('#cuelist tbody tr[data-cue-id]', (trs) => trs.map((tr) => Number(tr.dataset.cueId)));

  // Two-tap delete on row 2 (Panel).
  const del = pg.locator('#cuelist tbody tr[data-pos="2"] [data-cmd="cueDel"]');
  await del.click();
  await del.click();
  await pg.waitForFunction(() => document.querySelectorAll('#cuelist tbody tr[data-cue-id]').length === 2);
  const sent = await pg.evaluate(() => window.__sent.map((s) => JSON.parse(s)).filter((m) => m.action === 'cueDel'));
  t.check(`cueDel carries the row's cue id (${JSON.stringify(sent[0]?.args)})`, sent[0]?.args?.id === ids[1]);
  const labels = await pg.$$eval('#cuelist tbody .tp-cue-label', (els) => els.map((e) => e.textContent));
  t.check(`Panel deleted, others kept (${labels})`, labels.join() === 'Keynote,Lunch');

  // A stale command for the deleted id is refused, not applied elsewhere.
  await pg.evaluate((id) => window.__tpmesh.sendCommand('cueEdit', { id, pos: 2, label: 'WRONG' }), ids[1]);
  await pg.waitForTimeout(800);
  const after = await pg.$$eval('#cuelist tbody .tp-cue-label', (els) => els.map((e) => e.textContent));
  t.check(`stale edit by a gone id changes nothing (${after})`, after.join() === 'Keynote,Lunch');
  await ctx.close();
}
