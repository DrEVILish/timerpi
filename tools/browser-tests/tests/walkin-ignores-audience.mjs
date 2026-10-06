// STATUS U7: showing, voting on and hiding an audience item never flashes
// or re-renders a Walk-in screen (nothing on it is about the item).
export const name = 'walk-in screens ignore audience interaction changes';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent(['Stark', 'Banner']);
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  await api('PUT', `/api/shows/${room}/cues`, [{ label: 'Keynote', durationMS: 1_800_000, speaker: 'Ada' }, { label: 'Panel', durationMS: 600_000 }]);
  await api('POST', `/api/shows/${room}/daystart`, { hhmm: '09:00' });
  await api('POST', `/api/shows/${room}/cmd/start`, { pos: 1 });
  for (const [name, tpl] of [['Door', 'room'], ['Foyer', 'event']]) {
    await api('POST', `/api/shows/${room}/screens/template`, { name, template: tpl });
  }
  const pages = [];
  for (const name of ['Door', 'Foyer']) {
    const link = (await (await api('POST', `/api/shows/${room}/screens/link`, { name })).json()).link;
    const pg = await (await t.browser.newContext({ viewport: { width: 1280, height: 720 } })).newPage();
    await pg.goto(link);
    await pg.waitForSelector('.b-widget');
    await pg.waitForTimeout(2000);
    // Count animations and tile-content rewrites from here on.
    await pg.evaluate(() => {
      window.__anims = 0; window.__writes = 0;
      new MutationObserver((ms) => {
        for (const m of ms) {
          if (m.type === 'attributes' && /b-anim-/.test(m.target.className || '')) window.__anims++;
          // Clocks and countdowns tick every second by design; anything else
          // being rewritten is a re-render.
          const tile = m.target.closest?.('.b-widget');
          if (m.type === 'childList' && tile && !['wallclock', 'countdown', 'progress', 'dayprogress'].includes(tile.dataset.widget)) window.__writes++;
        }
      }).observe(document.querySelector('main') || document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['class'] });
    });
    pages.push([name, pg]);
  }
  const poll = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'poll', question: 'Lunch?', options: ['Pizza', 'Soup'] })).json();
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'audience', on: true });
  await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target: 'presenter', on: true });
  await new Promise((r) => setTimeout(r, 800));
  await api('POST', `/api/shows/${room}/polls/${poll.id}/results`, { on: true });
  await new Promise((r) => setTimeout(r, 800));
  await api('POST', `/api/shows/${room}/polls/${poll.id}/hide`, {});
  await new Promise((r) => setTimeout(r, 1500));
  for (const [name, pg] of pages) {
    const { anims, writes } = await pg.evaluate(() => ({ anims: window.__anims, writes: window.__writes }));
    t.check(`${name}: no animation and no tile rewrite (anims ${anims}, rewrites ${writes})`, anims === 0 && writes === 0);
  }
  await ctx.close();
}
