// STATUS U9 (+ BUGLOG RW35/RW36): on the Screens page a display's layout
// can be switched to another existing layout, to a built-in (shown as is),
// or back to the plain timer, and the live TV follows each time.
export const name = 'change a display\'s layout on the Screens page';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = async (method, path, data) => (await ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H })).json();
  await api('POST', `/api/shows/${room}/screens/config`, { name: 'Stage TV', theme: '', boardId: 0, room: '' });
  const sponsor = await api('POST', `/api/shows/${room}/boards`, { name: 'Sponsor loop' });
  const screenOf = async () => (await api('GET', `/api/shows/${room}/screens`)).screens.find((s) => s.name === 'Stage TV');

  const link = (await api('POST', `/api/shows/${room}/screens/link`, { name: 'Stage TV' })).link;
  const tv = await (await t.browser.newContext({ viewport: { width: 960, height: 540 } })).newPage();
  await tv.goto(link);
  await tv.waitForTimeout(1200);
  const tvBoard = async () => Number(new URL(tv.url()).searchParams.get('board') || 0);

  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/screens/${room}`);
  const sel = 'select[aria-label="Layout of Stage TV"]';
  await pg.waitForSelector(sel);
  t.check('an unassigned screen reads "Plain timer" (RW36)', (await pg.$eval(sel, (s) => s.selectedOptions[0]?.textContent)) === 'Plain timer (no layout)');

  await pg.selectOption(sel, `b:${sponsor.id}`);
  await pg.waitForTimeout(1500);
  t.check('picking an existing layout assigns it', (await screenOf()).boardId === sponsor.id);
  await tv.waitForTimeout(1500);
  t.check(`the TV follows (board ${await tvBoard()})`, (await tvBoard()) === sponsor.id);

  await pg.waitForSelector(sel);
  const tpl = await pg.$eval(sel, (s) => [...s.options].find((o) => o.value.startsWith('t:'))?.value);
  await pg.selectOption(sel, tpl); // built-ins apply directly (U10), no copy, no confirm
  await pg.waitForTimeout(1500);
  const s = await screenOf();
  t.check(`a built-in applies as is (template ${s.template}, board ${s.boardId})`, s.template === tpl.slice(2) && s.boardId === 0);
  await tv.waitForTimeout(1500);
  t.check(`the TV follows (${new URL(tv.url()).searchParams.get('tpl')})`, new URL(tv.url()).searchParams.get('tpl') === tpl.slice(2));

  await pg.waitForSelector(sel);
  await pg.selectOption(sel, 'b:0');
  await pg.waitForTimeout(1500);
  t.check('back to the plain timer', (await screenOf()).boardId === 0);
  await tv.waitForTimeout(1500);
  t.check(`the TV goes back to the plain timer (RW35; ${tv.url().replace(/key=\w+/, 'key=…')})`, (await tvBoard()) === 0 && !tv.url().includes('view=board'));
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
