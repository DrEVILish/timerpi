// STATUS U19–U23: home page wording and layout.
export const name = 'home page: recent + resume, logo, room rows, wording';

export async function run(t) {
  const ctx = await t.browser.newContext({ viewport: { width: 1280, height: 900 } });
  const pg = await ctx.newPage();
  await pg.goto(t.base + '/');
  t.check('U20: no recent box on a fresh device', await pg.$eval('#recent-panel', (p) => p.hidden));
  const text = await pg.evaluate(() => document.querySelector('main').innerText);
  t.check('U23: no "an" in the page wording', !/\ban\b/i.test(text));
  t.check('U19: "Open a screen here" + "Open a screen" button, no /d/ instructions',
    /open a screen here/i.test(text) && !!(await pg.$('a.btn:text-is("Open a screen")')) && !/it shows ready/i.test(text) && !/open \/d\//i.test(text));
  t.check('U21: the logo sits inline before "Run your event"',
    await pg.$eval('h1.tp-hero-title', (h) => h.firstElementChild?.tagName === 'IMG' && h.textContent.trim() === 'Run your event'));

  // U22: room rows with an X; "Add a room" is a real button.
  t.check('U22: two room rows to start, each with an X', (await pg.$$('#ev-rooms .tp-room-row button[aria-label="Remove room"]')).length === 2);
  await pg.click('#ev-add-room');
  await pg.click('#ev-add-room');
  await pg.click('#ev-rooms .tp-room-row:nth-child(2) button');
  const names = await pg.$$eval('#ev-rooms input', (is) => is.map((i) => i.value));
  t.check(`U22: add two, remove "Room B" → ${names.join(',')}`, names.join(',') === 'Room A,Room C,Room D');
  t.check('U22: "Add a room" is a button (not a ghost link)', await pg.$eval('#ev-add-room', (b) => b.tagName === 'BUTTON' && !b.classList.contains('btn-ghost')));

  // Create the event, come back: recent box with Resume, above the hero.
  await pg.fill('#ev-name', 'Home test');
  await pg.fill('#ev-pw', 'home-pw1');
  await pg.fill('#ev-pw2', 'home-pw1');
  await pg.click('#create-form button[type=submit]');
  await pg.waitForURL(/\/admin$/);
  const code = pg.url().match(/\/e\/([A-Z0-9]+)\//)[1];
  const ev = await (await ctx.request.get(`${t.base}/api/events/${code}`)).json();
  t.check(`created with the rooms as listed (${ev.event.rooms.map((r) => r.name).join(',')})`, ev.event.rooms.map((r) => r.name).join(',') === 'Room A,Room C,Room D');
  await pg.goto(t.base + '/');
  t.check('U20: recent box shown', !(await pg.$eval('#recent-panel', (p) => p.hidden)));
  t.check('U20: recent box sits above "Run your event"', await pg.evaluate(() =>
    document.getElementById('recent-panel').compareDocumentPosition(document.querySelector('.tp-hero')) & Node.DOCUMENT_POSITION_FOLLOWING));
  if (process.env.SHOTS) await pg.screenshot({ path: `${process.env.SHOTS}/home.png`, fullPage: true });
  const resume = await pg.$('#recent-list a.btn:text-is("Resume")');
  t.check('U20: Resume button goes to the event', !!resume && (await resume.getAttribute('href')) === `/e/${code}`);
  await pg.click('#recent-list button[aria-label^="Forget"]');
  t.check('U20: forgetting the last one hides the box', await pg.$eval('#recent-panel', (p) => p.hidden));
  await ctx.close();
}
