// ftl swaps on the Event Technician page: rooms are the live cards with an
// ftl dropdown ⋯ menu (rename etc.), Sign out goes back to the Rooms page, the live footer is an
// ftl .connection, and the pairing code is an ftl .otp that keeps a pasted
// "123 456" as six digits.
export const name = 'event admin: one room list with a ⋯ menu, sign out, connection lamp, OTP pairing code';

export async function run(t) {
  const { ctx, event, room } = await t.newEvent();
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/e/${event}/admin`);

  await pg.waitForSelector('#live-status[data-state="live"]', { timeout: 5000 }).catch(() => {});
  t.check('the live footer is an ftl .connection in state live',
    await pg.$eval('#live-status', (e) => e.classList.contains('connection') && e.dataset.state === 'live'));

  // One room list (2026-10-20): the live card's ⋯ menu renames the room.
  const card = `#live-grid [data-room="${room}"]`;
  await pg.waitForSelector(`${card} [data-room-menu]`);
  t.check('the dashboard has one room list (no rooms table)', !(await pg.$('#rooms-body')));
  await pg.click(`${card} [data-room-menu]`);
  t.check('the ⋯ menu is an ftl dropdown popover', await pg.$eval('#room-menu', (m) => m.matches('.dropdown:popover-open')));
  t.check('a single room has no Move up/down', await pg.$$eval('#room-menu [data-room-move]', (bs) => bs.every((b) => b.hidden)));
  await pg.click('#room-menu [data-room-rename]');
  await pg.fill('dialog.modal[open] input', 'Main hall');
  await Promise.all([pg.waitForEvent('load'), pg.click('dialog.modal[open] .modal-footer .btn-primary')]);
  const lobby = await (await ctx.request.get(`${t.base}/api/events/${event}`)).json();
  t.check('the new name is stored', lobby.event.rooms[0].name === 'Main hall');
  t.check('the card shows the new name', await pg.waitForFunction((c) => document.querySelector(c)?.textContent.includes('Main hall'), card).then(() => true, () => false));

  // Sign out (2026-10-20): back to the event's Rooms page, no longer the Event Technician.
  await Promise.all([pg.waitForURL(`${t.base}/e/${event}`), pg.click('form[action$="/signout"] button')]);
  t.check('sign out lands on the Rooms page with the Event Technician sign-in', !!(await pg.$('#super-login')));
  t.check('the Event Technician session is gone', (await ctx.request.get(`${t.base}/api/events/${event}/live`)).status() === 401);
  await ctx.request.post(`${t.base}/api/events/${event}/login`, { data: { pw: 'test-pw1' }, headers: { Origin: t.base } });
  await pg.goto(`${t.base}/e/${event}/admin`);

  t.check('the pairing code is an ftl .otp', !!(await pg.$('.otp > input#pair-code.otp-input[autocomplete="one-time-code"]')));
  await pg.focus('#pair-code');
  await pg.evaluate(() => {
    const i = document.getElementById('pair-code');
    i.value = '123 456';
    i.dispatchEvent(new Event('input', { bubbles: true }));
  });
  t.check('a pasted "123 456" becomes six digits', (await pg.inputValue('#pair-code')) === '123456');
  t.check(`no page errors (${errs.join('; ') || 'none'})`, errs.length === 0);
  await ctx.close();
}
