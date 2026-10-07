// Screens page: the event's own layouts can be deleted, and a deleted one
// stays deleted (owner 2026-10-07: the auto-made "Main" could not be
// removed and kept coming back).
export const name = 'Screens page: delete a layout; nothing re-creates it';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent(['Stark']);
  const lay = async () => (await (await ctx.request.get(`${t.base}/api/shows/${room}/boards`, { headers: H })).json()).map((b) => b.name);
  t.check(`an event starts with no layouts of its own (${(await lay()).join(', ')})`, (await lay()).length === 0);
  await ctx.request.post(`${t.base}/api/shows/${room}/boards`, { data: { name: 'Main' }, headers: H });
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/screens/${room}`);
  await pg.waitForSelector('#tp-layouts .list-item');
  t.check('the Layouts panel lists Main', (await pg.textContent('#tp-layouts')).includes('Main'));
  await pg.click('#tp-layouts [aria-label="Delete layout Main"]');
  await pg.click('dialog.modal[open] .modal-footer .btn-danger');
  await pg.waitForSelector('#tp-layouts .empty-state', { timeout: 5000 });
  t.check('deleted', (await lay()).length === 0);
  await pg.reload();
  await pg.waitForSelector('#tp-layouts .empty-state');
  await pg.goto(`${t.base}/d/${room}?view=board`); // a board page must not re-make it
  t.check('still gone after reloads and a board page', (await lay()).length === 0);
  await ctx.close();
}
