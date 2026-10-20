// Display review (ui-displays / ux-audience / ux-operator findings): long
// overtime fits its tile, countdown states are distinct (ftl
// .countdown[data-state]), stage messages never reach audience or walk-in
// screens, a typed screen gets its type's layout, "Next up" never wraps to
// the running cue, "in" follows the live countdown, and a portrait panel
// at rotation 0 gets the portrait version.
export const name = 'display screens: overtime fit, states, messages, default layout, next up';

// Push the screen's idea of server time forward by `shift` ms (every
// serverTime in the server's frames), so a running cue reads that much
// later without waiting.
async function shifted(t, url, shift, viewport = { width: 1920, height: 1080 }) {
  const ctx = await t.browser.newContext({ viewport });
  const page = await ctx.newPage();
  if (shift) {
    await page.routeWebSocket(/\/ws/, (ws) => {
      const srv = ws.connectToServer();
      srv.onMessage((m) => ws.send(typeof m === 'string' ? m.replace(/"serverTime":(\d+)/g, (_, n) => `"serverTime":${Number(n) + shift}`) : m));
      ws.onMessage((m) => srv.send(m));
    });
  }
  await page.goto(url);
  await page.waitForTimeout(1500);
  return { ctx, page };
}

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const post = (path, data) => ctx.request.post(`${t.base}/api/shows/${room}${path}`, { data, headers: H });
  const linkOf = async (name) => (await (await post('/screens/link', { name })).json()).link;
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, { data: [
    { label: 'Keynote', speaker: 'Ada', durationMS: 600_000, alert1MS: 120_000, alert2MS: 30_000, endAction: 'OVERTIME' },
    { label: 'Panel', durationMS: 600_000 },
  ], headers: H });
  await post('/cmd/start', { pos: 1 });
  await post('/screens/template', { name: 'DSM', template: 'dsm' });
  const dsm = await linkOf('DSM');

  // 1. Overtime of over an hour fits the countdown tile; "+" sign, no tenths.
  {
    const { ctx: c, page } = await shifted(t, dsm, 600_000 + 62 * 60_000 + 3_000);
    const r = await page.evaluate(() => {
      const el = document.querySelector('.b-js-clock');
      const tile = el.closest('.b-widget').getBoundingClientRect(), b = el.getBoundingClientRect();
      return { state: el.dataset.state, text: el.textContent.trim(), sign: !el.querySelector('.countdown-sign').hidden, fits: b.left >= tile.left - 1 && b.right <= tile.right + 1 };
    });
    t.check(`overtime "${r.text}" fits its tile with a + sign (${JSON.stringify(r)})`, r.state === 'overtime' && r.sign && r.fits && !/\.\d$/.test(r.text));
    await c.close();
  }

  // 2. States are distinct: alert1 coloured, paused adds a pause glyph, held is muted.
  const look = (page) => page.evaluate(() => {
    const el = document.querySelector('.b-js-clock');
    return { state: el.dataset.state, color: getComputedStyle(el).color, glyph: getComputedStyle(el, '::before').content };
  });
  {
    const { ctx: c, page } = await shifted(t, dsm, 500_000); // 1:40 left: alert1
    const a1 = await look(page);
    await post('/cmd/pause', {});
    await page.waitForTimeout(800);
    const paused = await look(page);
    t.check(`alert1 and paused differ (glyph "${paused.glyph}" vs "${a1.glyph}")`, a1.state === 'alert1' && paused.state === 'paused' && paused.glyph !== 'none' && paused.glyph !== a1.glyph);
    await post('/cmd/resume', {});
    await c.close();
  }

  // 3. Stage messages: on the presenter screen, never on audience or walk-in screens.
  await post('/messages', { text: 'WRAP UP NOW', show: true });
  await post('/screens/config', { name: 'Hall', kind: 'audience' });
  await post('/screens/config', { name: 'Door', kind: 'walkin' });
  await post('/screens/template', { name: 'Door', template: 'lobby' });
  for (const [name, want] of [['DSM', true], ['Hall', false], ['Door', false]]) {
    const page = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
    await page.goto(await linkOf(name));
    await page.waitForTimeout(1500);
    const has = (await page.content()).includes('WRAP UP NOW');
    t.check(`${name}: stage message ${want ? 'shown' : 'absent'}`, has === want);
    if (name === 'Hall') {
      // 4. A screen given only the Audience type shows the audience layout and what is pushed.
      t.check(`audience screen lands on its type's layout (${page.url().replace(/.*\?/, '')})`, /tpl=main/.test(page.url()));
      const poll = await (await post('/polls', { kind: 'poll', question: 'Pick one', options: ['A', 'B'] })).json();
      await post(`/polls/${poll.id ?? poll.item?.id}/show`, { target: 'audience', on: true });
      const q = await page.waitForSelector('.b-poll-q', { timeout: 5000 }).then((e) => e.textContent()).catch(() => '');
      t.check(`pushed poll reaches the audience screen ("${q}")`, q === 'Pick one');
      const status = await page.$('#d-status');
      t.check('no operator status line on the screen', !status);
    }
    await page.context().close();
  }

  // 5. "in" follows the live countdown, also while paused.
  {
    const page = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
    await page.goto(dsm);
    await post('/cmd/pause', {});
    await page.waitForTimeout(1500);
    const r = await page.evaluate(() => ({ cd: document.querySelector('.b-js-digits').textContent, nextIn: document.querySelector('.b-js-nextin').textContent, next: document.querySelector('.b-js-nextlabel').textContent }));
    t.check(`next "${r.next}" ${r.nextIn} matches the countdown ${r.cd}`, r.next === 'Panel' && r.nextIn === `in ${r.cd}`);
    await post('/cmd/resume', {});
    // 6. On the last session nothing is next (no wrap to cue 1).
    await post('/cmd/start', { pos: 2 });
    await page.waitForTimeout(1200);
    const last = await page.textContent('.b-js-nextlabel');
    t.check(`last session: next up reads "${last}"`, last === 'Finished for today');
    await page.context().close();
  }

  // 7. A portrait panel with no Mounted rotation gets the portrait version.
  {
    const page = await (await t.browser.newContext({ viewport: { width: 1080, height: 1920 } })).newPage();
    await page.goto(dsm);
    await page.waitForTimeout(1200);
    const o = await page.evaluate(() => document.documentElement.dataset.layoutOrient);
    t.check(`portrait panel at rotation 0 shows the ${o} version`, o === 'portrait');
    await page.context().close();
  }
  await ctx.close();
}
