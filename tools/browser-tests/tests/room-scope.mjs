// STATUS U25/U28/U29: moderators work in Run and Audience (no Setup tab,
// no Screens, no theme picker); Import sits on Run; Duplicate is on the
// SuperOperator dashboard; presets on the Screens page.
export const name = 'room page scope: Run + Audience, Import on Run, Duplicate, presets';

export async function run(t) {
  const { ctx, H, event, room } = await t.newEvent(['Stark', 'Banner']);
  // Moderator view.
  const modCtx = await t.browser.newContext({ viewport: { width: 1280, height: 900 } });
  await modCtx.request.post(`${t.base}/api/events/${event}/rooms/${room}/login`, { data: { pw: '' }, headers: H });
  const mod = await modCtx.newPage();
  await mod.goto(`${t.base}/c/${room}`);
  const tabs = await mod.$$eval('.tp-room-tabs .tab', (ts) => ts.map((x) => x.textContent.replace(/\s+/g, ' ').trim().split(' ')[0]));
  t.check(`moderator tabs: ${tabs.join(', ')}`, tabs.join(',') === 'Run,Audience');
  t.check('moderator has no theme picker', !(await mod.$('#theme-select')));
  // Import from the Run tab.
  await mod.click('details.tp-import > summary');
  await mod.setInputFiles('#import-file', { name: 'day.csv', mimeType: 'text/csv', buffer: Buffer.from('label,duration\nOpening,10\nKeynote,45\n') });
  await mod.click('details.tp-import button[type=submit]');
  await mod.waitForTimeout(1500);
  const snap = await (await modCtx.request.get(`${t.base}/api/shows/${room}`)).json();
  t.check(`moderator imported from Run (${snap.cues.map((c) => c.label).join(',')})`, snap.cues.map((c) => c.label).join(',') === 'Opening,Keynote');

  // SuperOperator: Duplicate on the dashboard.
  const sup = await ctx.newPage();
  await sup.goto(`${t.base}/e/${event}/admin`);
  await sup.click(`tr[data-room="${room}"] [data-room-dup]`);
  await sup.fill('.tp-dlg input', 'Stark 2');
  await sup.click('.tp-dlg-actions .btn-primary');
  await sup.waitForTimeout(1500);
  const lobby = await (await ctx.request.get(`${t.base}/api/events/${event}`)).json();
  t.check(`Duplicate made "Stark 2" (${lobby.event.rooms.map((r) => r.name).join(', ')})`, lobby.event.rooms.some((r) => r.name === 'Stark 2'));
  // SuperOperator room page keeps Screens + theme picker.
  await sup.goto(`${t.base}/c/${room}`);
  t.check('SuperOperator has the Screens tab', !!(await sup.$(`.tp-room-tabs a[href="/screens/${room}"]`)));
  // Presets on the Screens page.
  await sup.goto(`${t.base}/screens/${room}`);
  await sup.fill('#tp-preset-save input[name=name]', 'Morning');
  await sup.click('#tp-preset-save button[type=submit]');
  await sup.waitForTimeout(1000);
  t.check('a preset saves on the Screens page', (await sup.textContent('#tp-presets')).includes('Morning'));
  await ctx.close();
  await modCtx.close();
}
