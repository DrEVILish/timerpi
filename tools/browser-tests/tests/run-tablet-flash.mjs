// 2026-10-07 owner notes: on a tablet the Run tab is one column (Current
// cue, transport, running order, Messages, Import); the add row matches the
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

  const top = (sel) => pg.$eval(sel, (e) => e.getBoundingClientRect().top);
  const order = ['#tp-now', '.tp-transport-bar', '.tp-runorder', '#messages-panel', '.tp-import'];
  const tops = [];
  for (const s of order) tops.push(await top(s));
  t.check(`tablet order: Current cue, transport, running order, Messages, Import (${tops.map(Math.round).join(', ')})`,
    tops.every((v, i) => i === 0 || v > tops[i - 1]));
  const w = await pg.$eval('#tp-now', (e) => e.getBoundingClientRect().width);
  const full = await pg.$eval('.tp-dash', (e) => e.getBoundingClientRect().width);
  t.check('Current cue is full width', Math.abs(w - full) < 2);

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
  const tm = await top('#tp-insp-timerKind'), az = await top('#tp-insp-endAction');
  t.check('Timer and At zero share a line', Math.abs(tm - az) < 2);
  await pg.check('#tp-insp-alert1Flash');
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
