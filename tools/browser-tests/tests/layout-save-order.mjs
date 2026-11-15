export const name = 'layout autosave writes stay ordered when the first save is slow';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const url = `${t.base}/api/shows/${room}/boards`;
  const made = await (await ctx.request.post(url, { data: { name: 'Save order' }, headers: H })).json();
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/d/${room}?view=board&board=${made.id}&edit=1&preview=1&compose=1`);
  let release;
  const gate = new Promise((r) => { release = r; });
  let writes = 0;
  await pg.route('**/boards/*', async (route) => {
    if (route.request().method() !== 'PUT') return route.continue();
    const number = ++writes;
    if (number === 1) await gate;
    const response = await route.fetch();
    await route.fulfill({ response });
  });
  const rows = await pg.locator('#b-rows').inputValue();
  const firstRows = Number(rows) + 1, secondRows = firstRows + 1;
  const matches = (request) => request.method() === 'PUT' && request.url().endsWith(`/boards/${made.id}`);
  const firstStarted = pg.waitForRequest(matches, { timeout: 5000 });
  await pg.locator('#b-rows').fill(String(firstRows));
  await pg.locator('#b-rows').dispatchEvent('change');
  await firstStarted;
  await pg.locator('#b-rows').fill(String(secondRows));
  await pg.locator('#b-rows').dispatchEvent('change');
  await pg.waitForTimeout(800); // let the second autosave become due while the first is held
  t.check('the second write waits for the first', writes === 1);
  const firstFinished = pg.waitForResponse((response) => matches(response.request()) &&
    response.request().postDataJSON().layout.rows === firstRows, { timeout: 5000 });
  release();
  await firstFinished;
  await pg.waitForFunction(() => document.querySelector('#b-save-state')?.textContent.startsWith('Saved '));
  for (let i = 0; i < 30; i++) {
    const list = await (await ctx.request.get(url)).json();
    const saved = (list.boards || list).find((b) => b.id === made.id).layout;
    if (saved.rows === secondRows) break;
    await pg.waitForTimeout(100);
  }
  const list = await (await ctx.request.get(url)).json();
  const saved = (list.boards || list).find((b) => b.id === made.id).layout;
  t.check('the newest canvas survives the slow earlier save', saved.rows === secondRows);
  await ctx.close();
}
