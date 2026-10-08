// Event file (PRODUCT E4): Export event downloads the whole event and
// "Import an event file…" makes a new event from it. "Leave event" is a
// same-origin POST form (a plain cross-site link no longer signs out).
export const name = 'event admin: export/import the event file; Leave event posts';

import { readFileSync } from 'node:fs';

export async function run(t) {
  const { ctx, event } = await t.newEvent(['Stark', 'Banner']);
  const pg = await ctx.newPage();
  const errors = [];
  pg.on('pageerror', (e) => errors.push(e.message));
  await pg.goto(`${t.base}/e/${event}/admin`);

  const [dl] = await Promise.all([pg.waitForEvent('download'), pg.click('#ev-export')]);
  const file = readFileSync(await dl.path(), 'utf8');
  const j = JSON.parse(file);
  t.check(`the download is the event file (${dl.suggestedFilename()})`, j.kind === 'timerpi-event' && j.rooms.length === 2);
  t.check('no password hash in the file', !/pbkdf2\$/.test(file));

  await pg.setInputFiles('#ev-import', { name: 'event.json', mimeType: 'application/json', buffer: Buffer.from(file) });
  await pg.waitForSelector('dialog.modal[open] input[type=password]', { timeout: 5000 });
  await pg.fill('dialog.modal[open] input[type=password]', 'copy-pass1');
  await Promise.all([
    pg.waitForURL((u) => u.pathname.endsWith('/admin') && !u.pathname.includes(event), { timeout: 8000 }),
    pg.click('dialog.modal[open] .modal-footer .btn-primary'),
  ]);
  const copy = new URL(pg.url()).pathname.split('/')[2];
  t.check(`the import opened a new event (${copy})`, copy && copy !== event);
  t.check('the new event has both rooms', (await pg.textContent('body')).includes('Banner'));

  if (process.env.SHOTS) await pg.screenshot({ path: `${process.env.SHOTS}/event-file-leave.png` });
  // Leave event: a POST form, back home, signed out of the copy.
  await Promise.all([pg.waitForURL(`${t.base}/`, { timeout: 5000 }), pg.click('form.tp-nav-form button')]);
  const st = (await ctx.request.get(`${t.base}/api/events/${copy}/export`)).status();
  t.check(`Leave event signed this browser out (${st})`, st === 401);
  // A plain GET link does not sign out.
  await pg.goto(`${t.base}/e/${event}/leave`);
  t.check('a GET leave link keeps the session', (await ctx.request.get(`${t.base}/api/events/${event}/export`)).status() === 200);
  t.check(`no page errors (${errors.join('; ') || 'none'})`, errors.length === 0);
  await ctx.close();
}
