// STATUS U2: displays never go fullscreen on a tap or the F key, and show
// no "F / tap — fullscreen" hint (the ready card included).
export const name = 'displays have no tap/F fullscreen and always animate';

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
  // Read the rendered stylesheet on each display surface and on a phone.
  for (const path of [`/d/${room}`, `/d/${room}?view=next`, `/d/${room}?view=board`, `/a/${room}`]) {
    await tv.goto(t.base + path);
    for (const motion of ['no-preference', 'reduce']) {
      await tv.emulateMedia({ reducedMotion: motion });
      for (const state of ['overtime', 'alert2']) {
        const animation = await tv.evaluate((state) => {
          const clock = document.createElement('div');
          clock.className = 'tp-clock';
          clock.dataset.state = state;
          clock.textContent = '00:01';
          document.body.appendChild(clock);
          const name = getComputedStyle(clock).animationName;
          clock.remove();
          return name;
        }, state);
        const reducedPhone = path.startsWith('/a/') && motion === 'reduce';
        t.check(`${path} ${motion} ${state}: ${animation}`, animation === (reducedPhone ? 'none' : 'tp-overtime-pulse'));
      }
    }
  }
  await tv.context().close();
  await ctx.close();
}
