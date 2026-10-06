// BUGLOG RS24: pressing Enter twice on "Create event" creates one event.
export const name = 'create event submits once';

export async function run(t) {
  const ctx = await t.browser.newContext();
  const pg = await ctx.newPage();
  let posts = 0;
  pg.on('request', (r) => { if (r.method() === 'POST' && r.url().endsWith('/api/events')) posts++; });
  // Slow the create down so the second Enter lands while the first runs.
  await pg.route('**/api/events', async (route) => { await new Promise((r) => setTimeout(r, 600)); route.continue(); });
  await pg.goto(`${t.base}/`);
  await pg.fill('#ev-name', 'Once only');
  await pg.fill('#ev-pw', 'secret-pw');
  await pg.fill('#ev-pw2', 'secret-pw');
  await pg.press('#ev-pw2', 'Enter');
  await pg.press('#ev-pw2', 'Enter').catch(() => {});
  await pg.waitForURL(/\/admin$/, { timeout: 8000 });
  t.check(`one create request (${posts})`, posts === 1);
  await ctx.close();
}
