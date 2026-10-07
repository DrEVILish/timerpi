// STATUS U1 + owner 2026-10-07: a phone or tablet used as a display follows
// its own orientation: Mounted is ignored there (and hidden on the Screens
// page), and it gets the layout version for the way it is held — portrait
// held upright, landscape turned — switching as it turns. A kiosk/TV
// (touchscreen or not) still applies Mounted, which also picks its version.
export const name = 'phones/tablets follow their orientation and get the matching layout version';

const IPHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/config`, { data: { name: 'Phone', kind: 'presenter', rotation: 90 }, headers: H });
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/template`, { data: { name: 'Phone', template: 'stage' }, headers: H });
  const link = (await (await ctx.request.post(`${t.base}/api/shows/${room}/screens/link`, { data: { name: 'Phone' }, headers: H })).json()).link;

  const phone = await (await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, userAgent: IPHONE })).newPage();
  await phone.goto(link);
  await phone.waitForSelector('.b-widget');
  const look = () => phone.evaluate(() => ({
    rotate: document.documentElement.dataset.rotate || '',
    device: document.documentElement.dataset.deviceOrient || '',
    version: document.documentElement.dataset.layoutOrient || '',
  }));
  let a = await look();
  t.check(`upright phone: no Mounted rotation, portrait version (${JSON.stringify(a)})`, a.rotate === '' && a.device === '1' && a.version === 'portrait');

  // Turn the phone: it swaps to the landscape version of the same layout.
  await phone.setViewportSize({ width: 844, height: 390 });
  await phone.waitForFunction(() => document.documentElement.dataset.layoutOrient === 'landscape', null, { timeout: 5000 }).catch(() => {});
  await phone.waitForSelector('.b-widget');
  a = await look();
  t.check(`turned phone: landscape version, still unrotated (${JSON.stringify(a)})`, a.version === 'landscape' && a.rotate === '');
  // And back.
  await phone.setViewportSize({ width: 390, height: 844 });
  await phone.waitForFunction(() => document.documentElement.dataset.layoutOrient === 'portrait', null, { timeout: 5000 }).catch(() => {});
  t.check('turned back: portrait version again', (await look()).version === 'portrait');

  // A live "Mounted" push is ignored on the phone.
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/config`, { data: { name: 'Phone', rotation: 270 }, headers: H });
  await phone.waitForTimeout(1500);
  t.check('a live Mounted change is ignored on the phone', (await look()).rotate === '');

  // The Screens page knows it is handheld: no Mounted setting for it.
  const ops = await ctx.newPage();
  await ops.goto(`${t.base}/screens/${room}`);
  await ops.waitForSelector('select[aria-label="Layout of Phone"]');
  t.check('Screens page hides Mounted for the phone', !(await ops.$('select[aria-label="Rotation of Phone"]')));

  // The same screen link on a TV applies Mounted (270°), which picks the
  // portrait version.
  await phone.context().close();
  const tv = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
  await tv.goto(link);
  await tv.waitForSelector('.b-widget');
  const tvLook = await tv.evaluate(() => [document.documentElement.dataset.rotate || '', document.documentElement.dataset.layoutOrient]);
  t.check(`a TV applies Mounted and its version (${tvLook.join(', ')})`, tvLook[0] === '270' && tvLook[1] === 'portrait');
  await ctx.close();
}
