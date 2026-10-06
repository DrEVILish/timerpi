// BUGLOG RS20: a theme name in another case loads the right bundle
// ("Blue-Future" used to become "lue-uture").
export const name = 'theme names in any case load the right bundle';

export async function run(t) {
  const ctx = await t.browser.newContext();
  const pg = await ctx.newPage();
  await pg.goto(`${t.base}/`);
  const r = await pg.evaluate(async () => {
    const m = await import('/src/theme.js');
    m.applyTheme('Blue-Future');
    return { attr: document.documentElement.getAttribute('data-theme'), href: document.getElementById('theme-css-live')?.getAttribute('href') || '' };
  });
  t.check(`data-theme ${r.attr}, bundle ${r.href}`, r.attr === 'blue-future' && r.href.startsWith('/ftl/dist/blue-future.css'));
  await ctx.close();
}
