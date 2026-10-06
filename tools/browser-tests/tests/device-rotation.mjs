// STATUS U1: a phone or tablet used as a display follows its own
// orientation: the screen's "Mounted" rotation is ignored there (also when
// pushed live), and turning the device re-lays the page. A kiosk/TV (no
// touch) still applies Mounted.
export const name = 'phones/tablets follow their orientation; Mounted ignored';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/config`, { data: { name: 'Phone', kind: 'presenter', rotation: 90 }, headers: H });
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/template`, { data: { name: 'Phone', template: 'stage' }, headers: H });
  const link = (await (await ctx.request.post(`${t.base}/api/shows/${room}/screens/link`, { data: { name: 'Phone' }, headers: H })).json()).link;

  const phone = await (await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })).newPage();
  await phone.goto(link);
  await phone.waitForSelector('.b-widget');
  const attrs = await phone.evaluate(() => ({ rotate: document.documentElement.dataset.rotate || '', device: document.documentElement.dataset.deviceOrient || '' }));
  t.check(`phone ignores Mounted 90° (rotate "${attrs.rotate}", follows device "${attrs.device}")`, attrs.rotate === '' && attrs.device === '1');
  // Turn the phone: the canvas re-lays to the new shape, still unrotated.
  await phone.setViewportSize({ width: 844, height: 390 });
  await phone.waitForTimeout(400);
  const fit = await phone.evaluate(() => {
    const g = document.querySelector('.b-grid') || document.querySelector('main');
    const r = g.getBoundingClientRect();
    return { w: Math.round(r.width), h: Math.round(r.height), rotate: document.documentElement.dataset.rotate || '' };
  });
  t.check(`turned to landscape: canvas follows (${fit.w}×${fit.h}), no rotation`, fit.w > fit.h && fit.rotate === '');
  // A live "Mounted" push is ignored on the phone too.
  await ctx.request.post(`${t.base}/api/shows/${room}/screens/config`, { data: { name: 'Phone', rotation: 270 }, headers: H });
  await phone.waitForTimeout(1500);
  t.check('a live Mounted change is ignored on the phone', (await phone.evaluate(() => document.documentElement.dataset.rotate || '')) === '');

  // The same screen link on a kiosk/TV (no touch) applies Mounted.
  const tv = await (await t.browser.newContext({ viewport: { width: 1920, height: 1080 } })).newPage();
  await tv.goto(link);
  await tv.waitForSelector('.b-widget');
  t.check('a TV still applies Mounted', (await tv.evaluate(() => document.documentElement.dataset.rotate || '')) === '270');
  await ctx.close();
}
