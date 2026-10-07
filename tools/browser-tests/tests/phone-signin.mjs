// The Event Technician shows a QR code and their phone opens the dashboard,
// signed in (owner 2026-10-07: check settings while walking the venue).
export const name = 'Event Technician: sign in on your phone by QR';

const S = process.env.SHOTS;
const IPHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1';

export async function run(t) {
  const { ctx, H, event } = await t.newEvent(['Stark', 'Banner']);
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/e/${event}/admin`);
  await pg.click('#phone-link-btn');
  await pg.waitForFunction(() => document.getElementById('phone-link-qr')?.src.startsWith('data:image/png'), null, { timeout: 5000 });
  t.check('the dialog shows a QR code', await pg.$eval('#phone-link', (d) => d.open && d.classList.contains('modal')));
  if (S) await pg.screenshot({ path: `${S}/phone-qr.png` });
  await pg.click('#phone-link [data-close]');
  await pg.waitForFunction(() => !document.getElementById('phone-link-qr').getAttribute('src'), null, { timeout: 3000 }).catch(() => {});
  t.check('closing drops the code', await pg.$eval('#phone-link-qr', (i) => !i.getAttribute('src')));

  // The phone scans the code (the same link the QR carries).
  const { url } = await (await ctx.request.post(`${t.base}/api/events/${event}/phone-link`, { data: { base: t.base }, headers: H })).json();
  const phoneCtx = await t.browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, userAgent: IPHONE });
  const phone = await phoneCtx.newPage();
  await phone.goto(url);
  await phone.waitForSelector('#live-grid .panel', { timeout: 8000 });
  t.check(`the phone lands on the dashboard (${new URL(phone.url()).pathname})`, new URL(phone.url()).pathname === `/e/${event}/admin`);
  t.check('the code is gone from the address bar', !phone.url().includes('t='));
  if (S) await phone.screenshot({ path: `${S}/phone-admin.png`, fullPage: false });
  // Used once: another phone with the same code is refused.
  const other = await (await t.browser.newContext()).newPage();
  await other.goto(url);
  t.check('the same code does not sign in a second phone', (await other.textContent('body')).includes('expired or was already used'));
  await ctx.close();
}
