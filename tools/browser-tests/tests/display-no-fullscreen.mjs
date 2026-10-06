// STATUS U2: displays never go fullscreen on a tap or the F key, and show
// no "F / tap — fullscreen" hint (the ready card included).
export const name = 'displays have no tap/F fullscreen';

export async function run(t) {
  const { ctx, room } = await t.newEvent();
  const tv = await (await t.browser.newContext({ viewport: { width: 960, height: 540 } })).newPage();
  for (const path of [`/d/${room}`, `/d/${room}?view=next`, `/d/${room}?view=board`, '/d/']) {
    await tv.goto(t.base + path);
    await tv.waitForTimeout(800);
    await tv.mouse.click(480, 270);
    await tv.keyboard.press('f');
    await tv.waitForTimeout(300);
    const full = await tv.evaluate(() => !!document.fullscreenElement);
    const chip = await tv.$('#tp-fs-chip');
    t.check(`${path}: no fullscreen after tap + F, no hint chip`, !full && !chip);
  }
  await ctx.close();
}
