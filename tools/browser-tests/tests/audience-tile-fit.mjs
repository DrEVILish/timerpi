// Review round 2 (ux-audience N1/N2/N5/N6/N10, ui-displays #6): the
// audience tiles fit their content as one box (fit.js fitHeight). A
// 170-character question with ten options never overprints or cuts on the
// audience display or the DSM; a long spotlight fits the DSM and the wall
// says "+N more"; word-cloud words keep a gap; the layout editor shows a
// quiet hint in an empty messages tile while a screen paints nothing.
export const name = 'audience tiles fit their content (long poll, spotlight, +N more, cloud gaps)';

const Q = 'Thinking about everything we covered this morning, from the venue logistics to the hybrid streaming set-up and the new badge printers, which change matters most?';

export async function run(t) {
  const { ctx, H, room } = await t.newEvent();
  const api = (method, path, data) => ctx.request.fetch(`${t.base}${path}`, { method, data, headers: H });
  const ask = async (item, text) => {
    const phone = await t.browser.newContext();
    await phone.request.fetch(`${t.base}/api/audience/${room}/ask`, { method: 'POST', data: { item, text }, headers: H });
    await phone.close();
  };
  await api('PUT', `/api/shows/${room}/cues`, [{ label: 'Keynote', durationMS: 1_800_000 }]);
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
  // Every part of the box inside it, the question above the options.
  const fitOf = (pg) => pg.evaluate(() => {
    const box = [...document.querySelectorAll('.b-js-poll')].find((b) => b.dataset.audMode === 'bars');
    if (!box) return null;
    const r = box.getBoundingClientRect();
    const q = box.querySelector('.b-poll-q').getBoundingClientRect();
    const parts = [...box.querySelectorAll('.b-poll-meta, .b-poll-opt')].map((e) => e.getBoundingClientRect());
    return {
      over: box.scrollHeight - box.clientHeight,
      out: [q, ...parts].filter((p) => p.top < r.top - 1 || p.bottom > r.bottom + 1).length,
      under: parts.filter((p) => p.top < q.bottom - 1).length,
      opts: box.querySelectorAll('.b-poll-opt').length,
      size: parseFloat(getComputedStyle(box.querySelector('.b-poll-opt-head')).fontSize),
    };
  });
  const poll = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'poll', question: Q, options: Array.from({ length: 10 }, (_, i) => `Option number ${i + 1} with a longer label`) })).json();
  for (const target of ['audience', 'presenter']) await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target, on: true });
  for (const [name, sizes] of [['Hall', [[1920, 1080], [1280, 720], [1080, 1920]]], ['Stage', [[1920, 1080], [1280, 720]]]]) {
    const pg = screens[name];
    await pg.waitForFunction(() => document.querySelectorAll('.b-poll-opt').length === 10, null, { timeout: 8000 }).catch(() => {});
    for (const [w, h] of sizes) {
      await pg.setViewportSize({ width: w, height: h });
      await pg.waitForTimeout(700);
      const f = await fitOf(pg);
      t.check(`${name} ${w}x${h}: 170-char question + 10 options fit (${JSON.stringify(f)})`, f && f.opts === 10 && f.over <= 1 && f.out === 0 && f.under === 0 && f.size >= 11);
    }
    await pg.setViewportSize({ width: 1920, height: 1080 });
  }
  for (const target of ['audience', 'presenter']) await api('POST', `/api/shows/${room}/polls/${poll.id}/show`, { target, on: false });

  // DSM Q&A: a long spotlight fits; the wall says how many more there are.
  const stage = screens.Stage;
  const qa = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'qa', question: 'Ask the speaker', autoApprove: true })).json();
  // Phones ask while it is on the audience too.
  for (const target of ['audience', 'presenter']) await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target, on: true });
  for (const q of ['First question?', 'Second question?', 'Third question?', 'Fourth question?', 'Fifth question?']) await ask(qa.id, q);
  await stage.waitForFunction(() => document.querySelectorAll('.b-qa-item').length === 4, null, { timeout: 8000 }).catch(() => {});
  await stage.waitForTimeout(1200);
  const wall = () => stage.evaluate(() => {
    const items = [...document.querySelectorAll('.b-qa-item')];
    const more = document.querySelector('.b-qa-more');
    const box = document.querySelector('.b-js-poll[data-aud-mode="wall"]').getBoundingClientRect();
    const st = document.querySelector('.b-qa-spot-text');
    const s = st?.getBoundingClientRect();
    return {
      seen: items.filter((li) => li.style.opacity === '1').length,
      cut: items.filter((li) => li.style.opacity === '1' && li.getBoundingClientRect().bottom > box.bottom + 1).length,
      more: more && !more.hidden ? more.textContent : '',
      spot: s && s.height ? { inside: s.top >= box.top - 1 && s.bottom <= box.bottom + 1, over: st.scrollHeight - st.clientHeight } : null,
    };
  });
  const w1 = await wall();
  const n1 = Number((w1.more.match(/\+(\d+) more/) || [])[1] || 0);
  t.check(`DSM wall: every open question is shown or counted (${JSON.stringify(w1)})`, w1.cut === 0 && w1.seen + n1 === 5 && (n1 === 0 || /^\+\d+ more$/.test(w1.more)));
  const kids = (await (await api('GET', `/api/shows/${room}/polls`)).json()).items.find((i) => i.id === qa.id).children;
  const longQ = 'When the hybrid stream drops for remote attendees in the middle of a keynote, who makes the call to pause the room, how is that announced, and who decides?';
  await ask(qa.id, longQ);
  await stage.waitForTimeout(800);
  const kids2 = (await (await api('GET', `/api/shows/${room}/polls`)).json()).items.find((i) => i.id === qa.id).children;
  const long = kids2.find((c) => c.question === longQ) || kids[0];
  await api('POST', `/api/shows/${room}/polls/${qa.id}/spotlight`, { entry: long.id });
  for (const [w, h] of [[1920, 1080], [1280, 720], [1080, 1920]]) {
    await stage.setViewportSize({ width: w, height: h });
    await stage.waitForTimeout(1200);
    const w2 = await wall();
    const n2 = Number((w2.more.match(/\+(\d+) more/) || [])[1] || 0);
    t.check(`DSM ${w}x${h}: the long spotlight fits the tile (${JSON.stringify(w2.spot)})`, w2.spot && w2.spot.inside && w2.spot.over <= 1);
    t.check(`DSM ${w}x${h}: the wall shows or counts the rest (${JSON.stringify(w2)})`, w2.cut === 0 && w2.seen + n2 === 5);
  }
  await stage.setViewportSize({ width: 1920, height: 1080 });
  for (const target of ['audience', 'presenter']) await api('POST', `/api/shows/${room}/polls/${qa.id}/show`, { target, on: false });

  // Word cloud: words keep a visible gap ("oktired", review2 N10).
  const hall = screens.Hall;
  const cloud = await (await api('POST', `/api/shows/${room}/polls`, { kind: 'wordcloud', question: 'How do you feel?', autoApprove: true })).json();
  await api('POST', `/api/shows/${room}/polls/${cloud.id}/show`, { target: 'audience', on: true });
  for (const w of ['ok', 'tired', 'coffee', 'great', 'inspired', 'hungry', 'ok', 'ok']) await ask(cloud.id, w);
  await hall.waitForFunction(() => document.querySelectorAll('.b-cloud-word').length === 6, null, { timeout: 8000 }).catch(() => {});
  await hall.waitForTimeout(4000);
  const gap = await hall.$$eval('.b-cloud-word', (ws) => {
    const b = ws.map((w) => w.getBoundingClientRect());
    let min = Infinity;
    for (let i = 0; i < b.length; i++) for (let j = i + 1; j < b.length; j++) {
      const dx = Math.max(b[i].left - b[j].right, b[j].left - b[i].right);
      const dy = Math.max(b[i].top - b[j].bottom, b[j].top - b[i].bottom);
      min = Math.min(min, Math.max(dx, dy));
    }
    return Math.round(min);
  });
  t.check(`cloud words keep a gap (closest ${gap}px)`, gap >= 4);

  // Layout editor: the empty messages tile shows a quiet hint (screens paint nothing).
  const nb = await (await ctx.request.post(`${t.base}/api/shows/${room}/boards`, { data: { name: 'Hint' }, headers: H })).json();
  const ed = await ctx.newPage();
  await ed.goto(`${t.base}/d/${room}?view=board&board=${nb.id}&edit=1&preview=1`);
  await ed.waitForSelector('.b-widget');
  await ed.waitForTimeout(600);
  const hint = await ed.evaluate(() => {
    const m = document.querySelector('.b-w-messages');
    return m && { text: m.innerText.trim(), band: getComputedStyle(m.querySelector('.lower-third')).display };
  });
  t.check(`editor: empty messages tile shows a quiet hint, no band (${JSON.stringify(hint)})`, hint && hint.text === 'Stage messages appear here' && hint.band === 'none');
  for (const s of Object.values(screens)) await s.context().close();
  await ctx.close();
}
