// BUGLOG RS34: screens spread their reconnects (50–150 % of the backoff)
// instead of all retrying in lockstep after a restart.
export const name = 'reconnect delays are jittered';

export async function run(t) {
  const ctx = await t.browser.newContext();
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/`);
  const r = await pg.evaluate(async () => {
    const { reconnectDelay } = await import('/src/mesh.js');
    const lo = reconnectDelay(1000, () => 0);
    const hi = reconnectDelay(1000, () => 0.999);
    const many = new Set(Array.from({ length: 50 }, () => reconnectDelay(4000)));
    return { lo, hi, distinct: many.size };
  });
  t.check(`range ${r.lo}–${r.hi} ms for a 1 s backoff`, r.lo === 500 && r.hi >= 1490 && r.hi <= 1500);
  t.check(`50 screens pick ${r.distinct} different delays`, r.distinct > 25);
  await ctx.close();
}
