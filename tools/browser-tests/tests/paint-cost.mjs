// BUGLOG RS33: the room page's 60 Hz paint doesn't query the DOM every
// frame — the once-a-second parts run once a second.
export const name = 'room page paint stays cheap between seconds';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: [{ label: 'One', durationMS: 600_000 }, { label: 'Two', durationMS: 600_000 }], headers: H,
  });
  await ctx.request.post(`${t.base}/api/shows/${room}/cmd/start`, { data: { pos: 1 }, headers: H });
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tbody tr');
  await pg.waitForTimeout(1500);
  const n = await pg.evaluate(async () => {
    let calls = 0;
    const wrap = (proto) => {
      const orig = proto.querySelectorAll;
      proto.querySelectorAll = function (...a) { calls++; return orig.apply(this, a); };
      const one = proto.querySelector;
      proto.querySelector = function (...a) { calls++; return one.apply(this, a); };
    };
    wrap(Document.prototype);
    wrap(Element.prototype);
    await new Promise((r) => setTimeout(r, 2000));
    return calls;
  });
  // ~120 frames in 2 s; the old paint ran several queries on each.
  t.check(`${n} DOM queries in 2 s of a running cue (≤ 60)`, n <= 60);
  const clock = await pg.textContent('#tp-clock');
  t.check(`the clock still ticks (${clock})`, /\d:\d\d/.test(clock));
  // BUGLOG RS38: the room page no longer writes the old home page's
  // recent-shows ledger on every snapshot.
  t.check('no tp.recent.shows writes', (await pg.evaluate(() => localStorage.getItem('tp.recent.shows'))) === null);
  await ctx.close();
}
