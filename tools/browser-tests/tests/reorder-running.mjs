// STATUS U15 (+ BUGLOG RC2): dragging rows of the running order — the
// running cue itself, or a row above it — never interrupts the running
// cue: it keeps running with the same anchor at its new position.
export const name = 'reordering the running order keeps the running cue';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: ['One', 'Two', 'Three', 'Four'].map((label, i) => ({ label, durationMS: (i + 1) * 600_000 })), headers: H,
  });
  const snap = async () => (await (await ctx.request.get(`${t.base}/api/shows/${room}`)).json());
  const pg = await ctx.newPage();
  await pg.setViewportSize({ width: 1280, height: 1600 }); // all four rows on screen
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tbody tr[data-pos="3"]');
  // Run "Three".
  await ctx.request.post(`${t.base}/api/shows/${room}/cmd/start`, { data: { pos: 3 }, headers: H });
  await pg.waitForTimeout(800);
  const before = (await snap()).runtime;
  const running = async () => {
    const s = await snap();
    return { label: s.cues.find((c) => c.pos === s.runtime.activePos)?.label, rt: s.runtime, order: s.cues.map((c) => c.label).join(',') };
  };
  const drag = async (fromPos, toPos) => {
    const from = await (await pg.$(`#cuelist tbody tr[data-pos="${fromPos}"] td.tp-cue-pos`)).boundingBox();
    const to = await (await pg.$(`#cuelist tbody tr[data-pos="${toPos}"]`)).boundingBox();
    await pg.mouse.move(from.x + from.width / 2, from.y + from.height / 2);
    await pg.mouse.down();
    await pg.mouse.move(from.x + from.width / 2, to.y + 4, { steps: 10 });
    await pg.mouse.up();
    // Watch what the page shows for a second: the clock must stay on
    // "Three" (30 min cue, so it reads 29:xx) the whole time.
    for (let i = 0; i < 10; i++) {
      await pg.waitForTimeout(100);
      seen.push(await pg.evaluate(() => `${document.getElementById('tp-cue-label').textContent}|${document.getElementById('tp-clock').textContent}`));
    }
  };
  const seen = [];
  const pageOK = () => seen.every((s) => /^Three\|29:/.test(s));

  await drag(4, 1); // a row from below to the top: "Three" shifts down
  let r = await running();
  t.check(`order changed (${r.order})`, r.order.startsWith('Four'));
  t.check(`"Three" still running after a row moved above it (${r.label})`, r.label === 'Three' && r.rt.running);
  t.check('anchor unchanged', r.rt.anchorTS === before.anchorTS);
  t.check(`page kept showing "Three" counting down (${[...new Set(seen)].join(' ; ')})`, pageOK());
  seen.length = 0;

  const pos = r.rt.activePos;
  await drag(pos, 1); // drag the running cue itself to the top
  r = await running();
  t.check(`running cue moved to the top (${r.order})`, r.order.startsWith('Three'));
  t.check(`"Three" still running after being dragged (${r.label}, pos ${r.rt.activePos})`, r.label === 'Three' && r.rt.running && r.rt.activePos === 1);
  t.check('anchor unchanged after its own drag', r.rt.anchorTS === before.anchorTS);
  t.check(`page kept showing "Three" after its own drag (${[...new Set(seen)].join(' ; ')})`, pageOK());
  await ctx.close();
}
