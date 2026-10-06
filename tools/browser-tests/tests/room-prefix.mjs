// STATUS U6: with several rooms, screens keep "Room: <name>" after live
// updates (the scripts rewrite these texts on every room update).
export const name = 'screens keep the "Room:" prefix live';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent(['Stark', 'Banner']);
  await ctx.request.put(`${t.base}/api/shows/${room}/cues`, { data: [{ label: 'Keynote', durationMS: 600_000 }], headers: H });
  const tv = await (await t.browser.newContext({ viewport: { width: 1280, height: 720 } })).newPage();
  await tv.goto(`${t.base}/d/${room}`);
  await tv.waitForTimeout(1500); // joined; the live snapshot has been applied
  const status = await tv.textContent('#d-status');
  t.check(`stage status line: "${status}"`, status.includes('Room: Stark'));
  await tv.goto(`${t.base}/d/${room}?view=board`);
  await tv.waitForTimeout(1500);
  const titles = await tv.$$eval('.b-js-showtitle, .b-topbar-show', (els) => els.map((e) => e.textContent.trim()).filter(Boolean));
  t.check(`board titles: ${JSON.stringify(titles)}`, titles.length > 0 && titles.every((x) => x.startsWith('Room: Stark')));
  await ctx.close();
}
