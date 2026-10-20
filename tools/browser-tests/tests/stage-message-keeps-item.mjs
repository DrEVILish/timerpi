// UX review audience #1 (mesh.js _adopt): a full state frame — here a stage
// message sent from the room page — used to drop snapshot.poll/presenter,
// blanking the pushed item on every screen until the next vote.
export const name = 'a stage message keeps the Audience and Presenter items on screen';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  await api('PUT', `/api/shows/${room}/cues`, [{ label: 'Keynote', durationMS: 1_800_000 }]);
  await api('POST', `/api/shows/${room}/cmd/start`, { pos: 1 });
  const screens = {};
  for (const [name, kind, template] of [['Hall', 'audience', 'main'], ['Stage', 'presenter', 'dsm']]) {
    await api('POST', `/api/shows/${room}/screens/config`, { name, kind });
    await api('POST', `/api/shows/${room}/screens/template`, { name, template });
    const link = (await (await api('POST', `/api/shows/${room}/screens/link`, { name })).json()).link;
    const pg = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
    await pg.goto(link);
    await pg.waitForSelector('.b-widget');
    screens[name] = pg;
  }
  const poll = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'poll', question: 'Lunch?', options: ['Pizza', 'Soup'] })).json();
  const qa = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'qa', question: 'Ask the speaker' })).json();
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'audience', on: true });
  await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target: 'presenter', on: true });
  const q = (pg) => pg.$$eval('.b-poll-q', (n) => n.map((e) => e.textContent.trim()).join('|'));
  for (const [name, text] of [['Hall', 'Lunch?'], ['Stage', 'Ask the speaker']]) {
    await screens[name].waitForFunction((s) => [...document.querySelectorAll('.b-poll-q')].some((e) => e.textContent.includes(s)), text, { timeout: 8000 }).catch(() => {});
    t.check(`${name} shows its item before the message (${await q(screens[name])})`, (await q(screens[name])).includes(text));
  }

  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/c/${room}`);
  await pg.fill('form[data-cmd="addMsg"] input[name=text]', 'WRAP UP');
  await pg.click('form[data-cmd="addMsg"] button[type=submit]');
  await screens.Stage.waitForFunction(() => /WRAP UP/.test(document.body.innerText), null, { timeout: 8000 }).catch(() => {});
  t.check('the presenter screen shows the stage message', /WRAP UP/.test(await screens.Stage.innerText('body')));
  await screens.Stage.waitForTimeout(1500);
  t.check(`Hall still shows the poll after the message (${await q(screens.Hall)})`, (await q(screens.Hall)).includes('Lunch?'));
  t.check(`Stage still shows the Q&A after the message (${await q(screens.Stage)})`, (await q(screens.Stage)).includes('Ask the speaker'));
  for (const s of Object.values(screens)) await s.context().close();
  await ctx.close();
}
