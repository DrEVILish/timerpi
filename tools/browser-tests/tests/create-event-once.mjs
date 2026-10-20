// BUGLOG RS24: pressing Enter twice on "Create event" creates one event.
// With no rooms named it gets one room and opens on its timer (2026-10-20).
export const name = 'create event submits once; no rooms → one room, straight to its timer';

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
  await pg.waitForURL(/\/c\/[A-Z0-9]+$/, { timeout: 8000 });
  t.check(`one create request (${posts})`, posts === 1);
  await pg.waitForSelector('#tp-clock');
  t.check('opened on the room\'s Run page', !!(await pg.$('.tp-transport-bar [data-cmd="go"]')));
  await ctx.close();
}
