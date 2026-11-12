// 2026-10-07 owner notes, reshaped by the 2026-11-12 review (R2): on a
// tablet the now band is on top, the transport sticks to the bottom, then the
// running order with the On air column (Messages) beside it; the add row matches the
// cue rows and says "Add Timer"; the edit modal starts with Type, has no
// Tags and puts the row colour last; FLASH blinks presenter timers.
export const name = 'Run tab on a tablet, edit modal, presenter flash';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.setViewportSize({ width: 1024, height: 1300 });
  await pg.goto(`${t.base}/c/${room}`);
  await pg.waitForSelector('#cuelist tfoot');

  const box = (sel) => pg.$eval(sel, (e) => { const r = e.getBoundingClientRect(); return { x: r.x, y: r.y, r: r.right, b: r.bottom, w: r.width }; });
  const now = await box('#tp-now'); const bar = await box('.tp-transport-bar');
  const run = await box('.tp-runorder'); const msgs = await box('#messages-panel');
  t.check(`tablet: the now band sits above the running order (${[now.b, run.y].map(Math.round).join(', ')})`, now.b <= run.y + 1);
  const vh = await pg.evaluate(() => innerHeight);
  const work = await box('.tp-work');
  t.check(`tablet: the transport is the bottom bar, with the time left (${Math.round(bar.y)} ≥ ${Math.round(work.b)}, ${Math.round(bar.b)} ≤ ${vh})`, bar.y >= work.b - 1 && bar.b <= vh + 1 && await pg.$eval('.tp-transport-bar #tp-clock-mini', (e) => getComputedStyle(e).display !== 'none'));
  const adj = await box('.tp-adjust-row');
  t.check('tablet: the adjust buttons sit inside the now band', adj.r <= now.r + 1);
  t.check(`tablet: Messages sit in the On air column beside the running order (${Math.round(msgs.x)} ≥ ${Math.round(run.r)})`, msgs.x >= run.r);

  const add = pg.locator('#cuelist tfoot button[type=submit]');
  t.check('the add button says "Add Timer"', (await add.textContent()).trim() === 'Add Timer');
  await pg.fill('#cuelist tfoot input[name=label]', 'Opening');
  await add.click();
  await pg.waitForSelector('#cuelist tbody tr[data-pos="1"] [data-cmd=go]');
  const hs = await pg.$$eval('#cuelist tbody tr[data-pos], #cuelist tfoot tr', (rs) => rs.map((r) => Math.round(r.getBoundingClientRect().height)));
  t.check(`add row is as tall as a cue row (${hs.join(', ')})`, new Set(hs).size === 1);

  await pg.setViewportSize({ width: 1440, height: 900 });
  await pg.click('#cuelist tbody tr', { button: 'right' });
  await pg.click('[data-row-act=details]');
  await pg.waitForSelector('#tp-inspector[open]');
  const labels = await pg.$$eval('#tp-insp-form .label', (ls) => ls.map((l) => l.textContent.trim()));
  t.check(`modal starts with Type, then Title (${labels.slice(0, 2).join(', ')})`, labels[0] === 'Type' && labels[1] === 'Title');
  t.check('Location and Duration have short labels', labels.includes('Location') && labels.includes('Duration'));
  t.check('no Tags field', !labels.some((l) => /tags/i.test(l)) && !(await pg.$('#tp-insp-tags')));
  t.check('row colour comes after the notes', labels.indexOf('Row colour') > labels.indexOf('Cue notes'));
  const top = (sel) => pg.$eval(sel, (e) => e.getBoundingClientRect().top);
  const tm = await top('#tp-insp-timerKind'), az = await top('#tp-insp-endAction');
  t.check('Timer and At zero share a line', Math.abs(tm - az) < 2);
  await pg.click('label:has(#tp-insp-alert1Flash)'); // ftl .toggle-btn
  await pg.click('#tp-insp-save');
  await pg.waitForTimeout(900);
  const cue = (await (await api('GET', `/api/shows/${room}`)).json()).cues?.[0]
    ?? (await (await api('GET', `/api/shows/${room}`)).json()).snapshot?.cues?.[0];
  t.check('Alert 1 flash is saved', cue?.alertFlash1 === true && !cue?.alertFlash2);

  // FLASH blinks the timer on a presenter screen.
  await api('POST', `/api/shows/${room}/screens/template`, { name: 'Stage', template: 'dsm' });
  const link = (await (await api('POST', `/api/shows/${room}/screens/link`, { name: 'Stage' })).json()).link;
  const tv = await (await t.browser.newContext({ viewport: { width: 1280, height: 720 } })).newPage();
  await tv.goto(link);
  await tv.waitForSelector('.b-js-clock');
  await tv.waitForTimeout(800);
  await pg.keyboard.press('Escape');
  await pg.click('[data-cmd=flash]');
  await tv.waitForFunction(() => document.body.classList.contains('b-flash'), null, { timeout: 5000 }).catch(() => {});
  t.check(`presenter screen flashes (kind ${await tv.evaluate(() => document.body.dataset.kind)})`,
    await tv.evaluate(() => document.body.classList.contains('b-flash') && getComputedStyle(document.querySelector('.b-js-clock')).animationName === 'b-blink'));
  const ping = await pg.textContent('#conn-ping');
  t.check(`footer shows the ping (${ping})`, /^\d+ ms$/.test(ping.trim()));
  t.check('footer is pinned to the bottom', await pg.$eval('.app-status', (f) => getComputedStyle(f).position === 'sticky'));
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
