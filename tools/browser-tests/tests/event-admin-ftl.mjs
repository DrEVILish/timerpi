// ftl swaps on the Event Technician page: the room name is an ftl editable
// cell (td.is-editable → .is-editing → .is-saved), the live footer is an
// ftl .connection, and the pairing code is an ftl .otp that keeps a pasted
// "123 456" as six digits.
export const name = 'event admin: editable room cell, connection lamp, OTP pairing code';

export async function run(t) {
  const { ctx, event, room } = await t.newEvent();
  const pg = await ctx.newPage();
  const errs = [];
  pg.on('pageerror', (e) => errs.push(e.message));
  await pg.goto(`${t.base}/e/${event}/admin`);

  await pg.waitForSelector('#live-status[data-state="live"]', { timeout: 5000 }).catch(() => {});
  t.check('the live footer is an ftl .connection in state live',
    await pg.$eval('#live-status', (e) => e.classList.contains('connection') && e.dataset.state === 'live'));

  const cell = `#rooms-body tr[data-room="${room}"] td[data-rename]`;
  t.check('the room name is an ftl editable cell', await pg.$eval(cell, (td) => td.classList.contains('is-editable')));
  await pg.dblclick(cell);
  t.check('double-click opens the editor in the cell', await pg.$eval(cell, (td) => td.classList.contains('is-editing') && !!td.querySelector('input.input')));
  await pg.fill(`${cell} input`, 'Main hall');
  await pg.press(`${cell} input`, 'Enter');
  await pg.waitForTimeout(600);
  const after = await pg.$eval(cell, (td) => ({ text: td.textContent.trim(), saved: td.classList.contains('is-saved'), editable: td.classList.contains('is-editable') }));
  t.check(`Enter saves and flags the cell saved (${JSON.stringify(after)})`, after.text === 'Main hall' && after.saved && after.editable);
  const lobby = await (await ctx.request.get(`${t.base}/api/events/${event}`)).json();
  t.check('the new name is stored', lobby.event.rooms[0].name === 'Main hall');

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
