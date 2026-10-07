// AGENTS.md: every dialog is an ftl-themes modal (<dialog class="modal">,
// .modal-header with .btn-close, .modal-footer) — no app-owned chrome.
export const name = 'dialogs are ftl-themes modals; × and Cancel close them';

const shots = process.env.TP_SHOTS || '';

export async function run(t) {
  const { ctx, event, room } = await t.newEvent(['Stark']);
  const pg = await ctx.newPage();
  // Prompt (dialog.js): Duplicate a room.
  await pg.goto(`${t.base}/e/${event}/admin`);
  await pg.click(`tr[data-room="${room}"] [data-room-dup]`);
  await pg.waitForSelector('dialog.modal[open] .modal-footer .btn-primary');
  if (shots) await pg.screenshot({ path: `${shots}/prompt.png` });
  await pg.click('dialog.modal[open] .modal-footer [data-close]');
  t.check('Cancel closes the prompt', !(await pg.$('dialog.modal[open]')));
  // Every dialog in the room pages is an ftl modal.
  for (const path of [`/c/${room}`, `/screens/${room}`]) {
    await pg.goto(t.base + path);
    const bad = await pg.$$eval('dialog', (ds) => ds.filter((d) => !d.classList.contains('modal')).map((d) => d.id));
    t.check(`${path}: every <dialog> is .modal (${bad.join(',') || 'all'})`, bad.length === 0);
  }
  // The layout editor: Done (data-close) closes it.
  await pg.evaluate(() => document.getElementById('tp-screen-edit').showModal());
  if (shots) await pg.screenshot({ path: `${shots}/layout-editor.png` });
  await pg.click('#tp-screen-edit-close');
  t.check('Done closes the layout editor', !(await pg.$('#tp-screen-edit[open]')));
  // The cue editor: open, × closes.
  await pg.goto(`${t.base}/c/${room}`);
  await pg.evaluate(() => document.getElementById('tp-inspector').showModal());
  if (shots) await pg.screenshot({ path: `${shots}/inspector.png` });
  await pg.click('#tp-inspector .modal-header .btn-close');
  t.check('× closes the cue editor', !(await pg.$('#tp-inspector[open]')));
  await ctx.close();
}
