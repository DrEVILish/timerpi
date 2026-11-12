// BUGLOG RW43 + RW44: the event page's live room rows are patched in place
// (focus survives the 2 s poll), a double-click on GO starts one session,
// and a failed settings change keeps the page and its error.
export const name = 'event page: stable live rows, guarded GO, no reload on failure';

export async function run(t) {
  const { ctx, H, event, room } = await t.newEvent();
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, {
    data: ['One', 'Two', 'Three'].map((label) => ({ label, durationMS: 1_200_000 })), headers: H,
  });
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/e/${event}/admin`);
  const card = `#live-grid [data-room="${room}"]`;
  await pg.waitForSelector(card);

  // Focus survives polls, and the card is the same node.
  await pg.$eval(card, (c) => { c.__mark = 1; });
  await pg.focus(`${card} a[href^="/c/"]`);
  await pg.waitForTimeout(4500); // two polls
  const kept = await pg.$eval(card, (c) => ({
    same: c.__mark === 1,
    focus: document.activeElement?.getAttribute('href') || document.activeElement?.tagName,
  }));
  t.check('the room row is patched, not rebuilt', kept.same);
  t.check(`keyboard focus survives the poll (${kept.focus})`, kept.focus === `/c/${room}`);

  // GO pressed twice before the first lands starts one session, not two.
  await pg.click(`${card} [data-room-menu]`);
  await pg.$eval('#room-menu [data-act="go"]', (b) => { b.click(); b.click(); });
  await pg.waitForTimeout(1500);
  const snap = await (await ctx.request.get(`${t.base}/api/shows/${room}`)).json();
  const label = snap.cues.find((c) => c.pos === snap.runtime.activePos)?.label;
  t.check(`GO twice starts only the first session (${label})`, label === 'One');
  await pg.waitForTimeout(2500);
  const shown = await pg.$eval(`${card} td[data-label="Now"]`, (d) => d.textContent);
  t.check(`the row shows the running session (${shown})`, shown === 'One');

  // A refused rename keeps the page and shows the error.
  await pg.evaluate(() => { window.__noReload = 1; });
  await pg.click('.tabs [data-tab="setup"]');
  await pg.fill('#ev-name-input', '   ');
  await pg.press('#ev-name-input', 'Tab'); // saves on leaving the field
  await pg.waitForTimeout(1500);
  const still = await pg.evaluate(() => window.__noReload === 1);
  t.check('a refused rename does not reload the page', still);
  const toast = await pg.evaluate(() => document.body.innerText.includes('needs a name'));
  t.check('the refusal is shown', toast);
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
