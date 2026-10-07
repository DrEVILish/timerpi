/**
 * audience.js — the phone page (/a/<room>), PRODUCT §4.4.
 *
 * Live: one WebSocket on the audience lane (read-only, poll frames only);
 * REST fallback polling while it is down. Actions (vote, upvote, ask) are
 * REST calls with this device's random token. Everything the page shows
 * comes from the item the moderator has shown to the audience — nothing
 * else exists here. Motion honours prefers-reduced-motion (phones only).
 */
import { api, el as h, setText } from './ui.js';

// el(tag, cls, text): this page's terse shorthand over ui.js el().
const el = (tag, cls, text) => h(tag, { class: cls || undefined, text });

const code = document.body.dataset.showCode;
const main = document.getElementById('tp-aud-main');
const note = document.getElementById('tp-aud-note'); // ftl .connection

const store = {
  get(k, d) { try { const v = localStorage.getItem(k); return v === null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { localStorage.setItem(k, JSON.stringify(v)); } catch { /* */ } },
};
let peer = store.get('tp.aud.peer', '');
if (!peer) {
  peer = 'p-' + Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, '0')).join('');
  store.set('tp.aud.peer', peer);
}

let item = null; // the on-air item (or null)
let shownSig = '';

// setNote writes the footer line; state (live|reconnecting|offline) moves
// the .connection lamp, and action feedback leaves it as it is.
function setNote(text, state) {
  note.textContent = text;
  if (state) note.dataset.state = state;
}

// The device id is the server's tp_aud cookie.
const post = (path, body) => api('POST', `/api/audience/${code}${path}`, body);

// Rate-limited taps (429) retry once after a short jittered wait.
async function postRetry(path, body) {
  try {
    return await post(path, body);
  } catch (e) {
    if (e.status !== 429) throw e;
    await new Promise((r) => setTimeout(r, 400 + Math.random() * 900));
    return post(path, body);
  }
}

/* ---------------------------------------------------------------- render -- */

// The page is the question, a form slot (the "ask" box, built once per
// item) and a live slot (bars, wall, cloud). Live updates while the same
// item stays in the same state only rebuild the live slot, so the text box
// someone is typing in is never torn down: no lost caret or keyboard
// composition, and a send that lands during an update clears the real box
// (BUGLOG RW23).
let liveSlot = null;
// paused: the room runs at a venue whose link to the cloud is down
// (VENUE-CLOUD §6) — phones wait instead of voting into nothing.
let paused = false;
function setPaused(on) {
  if (on) setNote('Audience paused', 'reconnecting');
  if (!!on === paused) return;
  paused = !!on;
  shownSig = null;
  render();
}
function render() {
  const sig = paused ? 'paused' : (item ? `${item.id}:${item.kind}:${item.state}` : '');
  const fresh = sig !== shownSig;
  shownSig = sig;
  if (!fresh && item && liveSlot && main.contains(liveSlot)) {
    setText(main.querySelector('.tp-aud-q'), item.question);
    const live = el('div', 'tp-aud-live');
    renderLive(live, null);
    liveSlot.replaceWith(live);
    liveSlot = live;
    return;
  }
  const box = el('div', 'tp-aud-item');
  liveSlot = null;
  if (paused) {
    box.append(stateBox('offline', 'Audience paused', 'The room is reconnecting. Keep this page open — it carries on by itself.'));
  } else if (!item) {
    box.append(stateBox('loading', 'Waiting for the room', 'Keep this page open — questions and polls appear here when the presenter starts them.'));
  } else {
    box.append(el('h2', 'tp-aud-q', item.question));
    const formSlot = el('div', 'tp-aud-formslot');
    const live = el('div', 'tp-aud-live');
    box.append(formSlot, live);
    renderLive(live, formSlot);
    liveSlot = live;
  }
  main.replaceChildren(box);
  if (fresh) box.classList.add('tp-aud-in');
}
// stateBox: ftl .empty-state.is-offline / .is-loading (STATUS U17), the
// loading one matching the server's first paint.
function stateBox(kind, title, hint) {
  const box = el('div', `empty-state is-${kind} tp-aud-wait`);
  if (kind === 'loading') box.setAttribute('aria-busy', 'true');
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  svg.setAttribute('class', 'icon empty-state-icon');
  svg.setAttribute('aria-hidden', 'true');
  const use = document.createElementNS(ns, 'use');
  use.setAttribute('href', `/ftl/assets/icons/icons.svg#icon-${kind === 'loading' ? 'hourglass' : 'cloud-off'}`);
  svg.appendChild(use);
  box.append(svg, el('span', 'empty-state-title', title), el('span', 'empty-state-hint', hint));
  return box;
}
// renderLive fills the live slot; formSlot is given only on a full build.
function renderLive(live, formSlot) {
  if (item.kind === 'poll' || item.kind === 'quiz') renderVote(live);
  else if (item.kind === 'wordcloud') renderCloud(live, formSlot);
  else renderWall(live, formSlot);
}

function renderVote(box) {
  const mine = store.get(`tp.aud.vote.${item.id}`, null);
  const results = item.state === 'results';
  const opts = item.options || [];
  if (!results) {
    const list = el('div', 'tp-aud-opts');
    opts.forEach((label, i) => {
      // ftl buttons (U17): the chosen answer is the primary one.
      const b = el('button', `btn tp-aud-opt${mine === i ? ' btn-primary' : ''}`, label);
      b.type = 'button';
      b.setAttribute('aria-pressed', String(mine === i));
      b.addEventListener('click', async () => {
        const key = `tp.aud.vote.${item.id}`;
        const before = store.get(key, null);
        store.set(key, i);
        render();
        try {
          await postRetry('/vote', { pollId: item.id, choice: String(i) });
          setNote('Vote received — you can change it until the results are shown.');
        } catch (e) {
          // Not counted: don't show it as this phone's vote (BUGLOG RS11).
          store.set(key, before);
          render();
          setNote(e.message);
        }
      });
      list.appendChild(b);
    });
    box.append(list, el('p', 'text-muted tp-aud-hint', mine === null ? 'Tap an answer.' : 'Thanks! Tap another answer to change your vote.'));
    return;
  }
  const total = Math.max(1, item.total || 0);
  box.append(el('p', 'text-muted', `${item.total || 0} vote${item.total === 1 ? '' : 's'}`));
  opts.forEach((label, i) => {
    const n = item.counts?.[i] || 0;
    const pct = Math.round((n / total) * 100);
    const correct = item.kind === 'quiz' && i === item.correct;
    const row = el('div', 'tp-aud-res' + (correct ? ' is-correct' : '') + (mine === i ? ' is-mine' : ''));
    const head = el('div', 'tp-aud-res-head');
    head.append(el('span', '', (correct ? '✔ ' : '') + label + (mine === i ? ' (you)' : '')), el('span', 'mono', `${pct}%`));
    const bar = el('progress', 'progress tp-aud-bar'); // ftl progress (U17)
    bar.max = 100;
    bar.value = pct;
    bar.setAttribute('aria-label', `${label}: ${pct}%`);
    row.append(head, bar);
    box.appendChild(row);
  });
  if (item.kind === 'quiz' && mine !== null && item.correct >= 0) {
    const right = mine === item.correct;
    box.append(el('p', `alert ${right ? 'alert-success' : 'alert-warning'} tp-aud-verdict`, right ? 'You got it right!' : `The answer was: ${opts[item.correct]}`));
  }
}

function askForm(box, { placeholder, max, multiline, sentText }) {
  if (item.state !== 'open') {
    box.append(el('p', 'text-muted', 'Submissions are closed.'));
    return;
  }
  const form = el('form', 'tp-aud-ask');
  const input = multiline ? el('textarea', 'textarea') : el('input', 'input');
  if (!multiline) input.type = 'text';
  input.maxLength = max;
  input.placeholder = placeholder;
  input.setAttribute('aria-label', placeholder);
  if (multiline) input.rows = 2;
  const send = el('button', 'btn btn-primary', 'Send');
  send.type = 'submit';
  form.append(input, send);
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const text = input.value.trim();
    if (!text) return;
    send.disabled = true;
    try {
      const out = await post('/ask', { item: item.id, text });
      if (!out.approved) {
        const sent = store.get(`tp.aud.sent.${item.id}`, []);
        sent.push({ id: out.id, text, at: Date.now() });
        store.set(`tp.aud.sent.${item.id}`, sent.slice(-10));
      }
      input.value = '';
      setNote(out.approved ? 'Sent!' : sentText);
      render();
    } catch (err) {
      setNote(err.message);
    } finally {
      send.disabled = false;
    }
  });
  box.appendChild(form);
}

function renderWall(box, formSlot) {
  const isQA = item.kind === 'qa';
  if (formSlot) askForm(formSlot, {
    placeholder: isQA ? 'Ask a question…' : 'Share an idea…', max: 280, multiline: true,
    sentText: 'Sent — the moderator will review it shortly.',
  });
  const kids = item.children || [];
  // Only entries still waiting for review stay in "your questions": once
  // approved they are on the wall; dismissed or cleared ones are gone.
  const waiting = new Set(item.waiting || []);
  const all = store.get(`tp.aud.sent.${item.id}`, []);
  // A just-sent entry may not be in the frame yet: keep it a little while.
  const sent = all.filter((s) => waiting.has(s.id) || Date.now() - (s.at || 0) < 15000);
  if (sent.length !== all.length) store.set(`tp.aud.sent.${item.id}`, sent);
  if (sent.length) {
    const mineBox = el('div', 'tp-aud-mine');
    mineBox.append(el('p', 'label', 'Waiting for review'));
    for (const s of sent.slice(-3)) mineBox.append(el('p', 'text-muted', s.text));
    box.appendChild(mineBox);
  }
  if (item.spotlight) {
    const spot = el('div', 'alert alert-info tp-aud-spot');
    spot.append(el('span', 'label', 'Now answering'), el('p', '', item.spotlight.question));
    box.appendChild(spot);
  }
  const ups = new Set(store.get('tp.aud.up', []));
  const list = el('ul', 'list tp-aud-wall');
  for (const k of kids) {
    if (item.spotlight && k.id === item.spotlight.id) continue;
    const li = el('li', 'list-item tp-aud-entry' + (k.state === 'answered' ? ' is-answered' : ''));
    const up = el('button', 'badge badge-button tp-aud-up', `▲ ${k.upvotes || 0}`);
    up.type = 'button';
    up.setAttribute('aria-pressed', String(ups.has(k.id)));
    up.setAttribute('aria-label', `Upvote: ${k.question}`);
    up.disabled = ups.has(k.id) || k.state !== 'open' || item.state !== 'open';
    up.addEventListener('click', async () => {
      ups.add(k.id);
      store.set('tp.aud.up', [...ups].slice(-200));
      up.disabled = true;
      try {
        await postRetry('/vote', { pollId: k.id, choice: '1' });
      } catch (e) {
        // Not counted: the phone may try again (RS11).
        ups.delete(k.id);
        store.set('tp.aud.up', [...ups].slice(-200));
        up.disabled = false;
        setNote(e.message);
      }
    });
    li.append(up, el('span', '', k.question));
    list.appendChild(li);
  }
  if (kids.length) box.appendChild(list);
}

function renderCloud(box, formSlot) {
  if (formSlot) askForm(formSlot, { placeholder: 'One word…', max: 32, multiline: false, sentText: 'Sent — it appears once approved.' });
  const words = item.children || [];
  if (!words.length) return;
  const max = Math.max(1, ...words.map((w) => w.upvotes || 0));
  const cloud = el('div', 'tp-aud-cloud');
  for (const w of words) {
    const t = el('span', 'badge badge-accent tp-aud-word', w.question); // ftl badge (U17)
    t.style.fontSize = `${(0.9 + ((w.upvotes || 0) / max) * 1.1).toFixed(2)}rem`;
    cloud.appendChild(t);
  }
  box.appendChild(cloud);
}

/* ------------------------------------------------------------ live link -- */

function adopt(poll) {
  item = poll || null;
  render();
}

let ws = null;
let backoff = 1000;
let restTimer = null;
let retryTimer = 0;

function startRest() {
  if (restTimer) return;
  const tick = async () => {
    try {
      const res = await fetch(`/api/audience/${code}`);
      const j = await res.json();
      setPaused(j.paused);
      adopt(j.data?.poll);
    } catch { /* keep trying */ }
  };
  tick();
  restTimer = setInterval(tick, 4000);
}
function stopRest() {
  clearInterval(restTimer);
  restTimer = null;
}

// One socket per phone (BUGLOG RW22): connect() is a no-op while a socket
// is connecting or open, a pending retry is cancelled when another path
// connects first, and a close from a socket that is no longer current
// never schedules another retry.
function connect() {
  clearTimeout(retryTimer);
  retryTimer = 0;
  if (ws && ws.readyState <= 1) return;
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  let sock;
  try { sock = new WebSocket(`${proto}://${location.host}/ws`); } catch { return retry(); }
  ws = sock;
  sock.onopen = () => {
    sock.send(JSON.stringify({ v: 1, t: 'join', role: 'audience', show: code, peerId: peer, joinedAt: Date.now() }));
  };
  sock.onmessage = (ev) => {
    let m;
    try { m = JSON.parse(ev.data); } catch { return; }
    if (m.t === 'poll') {
      backoff = 1000;
      stopRest();
      setNote('Connected', 'live');
      setPaused(m.paused);
      adopt(m.poll);
    } else if (m.t === 'err') {
      setNote(m.message || 'Connection problem', 'offline');
    }
  };
  sock.onclose = () => { if (sock === ws) retry(); };
  sock.onerror = () => { try { sock.close(); } catch { /* */ } };
}
function retry() {
  setNote('Reconnecting…', 'reconnecting');
  startRest();
  if (retryTimer) return;
  const wait = backoff + Math.random() * backoff;
  backoff = Math.min(backoff * 2, 15000);
  retryTimer = setTimeout(connect, wait);
}

connect();
// A phone waking from sleep reconnects at once.
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible' && (!ws || ws.readyState > 1)) connect();
});
