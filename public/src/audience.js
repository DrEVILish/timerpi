/**
 * audience.js — the phone page (/a/<room>), PRODUCT §4.4.
 *
 * Live: one WebSocket on the audience lane (read-only, poll frames only);
 * REST fallback polling while it is down. Actions (vote, upvote, ask) are
 * REST calls with this device's random token. Everything the page shows
 * comes from the item the moderator has shown to the audience — nothing
 * else exists here. Motion honours prefers-reduced-motion (phones only).
 */
const code = document.body.dataset.showCode;
const main = document.getElementById('tp-aud-main');
const note = document.getElementById('tp-aud-note');
const lamp = document.getElementById('tp-aud-lamp');

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

function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}
function setNote(text, ok = true) {
  note.textContent = text;
  lamp.classList.toggle('is-on', ok);
}

async function post(path, body) {
  const res = await fetch(`/api/audience/${code}${path}`, {
    method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ ...body, peer }),
  });
  let data = {};
  try { data = await res.json(); } catch { /* */ }
  if (!res.ok) {
    const err = new Error(data.error || 'Something went wrong');
    err.status = res.status;
    throw err;
  }
  return data;
}

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

function render() {
  const sig = item ? `${item.id}:${item.kind}:${item.state}` : '';
  const fresh = sig !== shownSig;
  shownSig = sig;
  const box = el('div', 'tp-aud-item');
  if (!item) {
    box.append(el('p', 'tp-aud-wait-title', 'WAITING FOR THE ROOM'),
      el('p', 'text-muted', 'Keep this page open — questions and polls appear here when the presenter starts them.'));
  } else {
    box.append(el('h2', 'tp-aud-q', item.question));
    if (item.kind === 'poll' || item.kind === 'quiz') renderVote(box);
    else if (item.kind === 'wordcloud') renderCloud(box);
    else renderWall(box);
  }
  // Keep a focused text box (typing a question) across live updates.
  const typing = main.querySelector('textarea, input[type=text]');
  const draft = typing?.value || '';
  const hadFocus = typing && document.activeElement === typing;
  main.replaceChildren(box);
  const again = main.querySelector('textarea, input[type=text]');
  if (again && draft) again.value = draft;
  if (again && hadFocus) again.focus();
  if (fresh) box.classList.add('tp-aud-in');
}

function renderVote(box) {
  const mine = store.get(`tp.aud.vote.${item.id}`, null);
  const results = item.state === 'results';
  const opts = item.options || [];
  if (!results) {
    const list = el('div', 'tp-aud-opts');
    opts.forEach((label, i) => {
      const b = el('button', 'tp-aud-opt', label);
      b.type = 'button';
      b.setAttribute('aria-pressed', String(mine === i));
      b.addEventListener('click', async () => {
        store.set(`tp.aud.vote.${item.id}`, i);
        render();
        try {
          await postRetry('/vote', { pollId: item.id, choice: String(i) });
          setNote('Vote received — you can change it until the results are shown.');
        } catch (e) {
          setNote(e.message, false);
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
    const bar = el('div', 'tp-aud-bar');
    const fill = el('i');
    fill.style.width = `${pct}%`;
    bar.appendChild(fill);
    row.append(head, bar);
    box.appendChild(row);
  });
  if (item.kind === 'quiz' && mine !== null && item.correct >= 0) {
    box.append(el('p', 'tp-aud-verdict', mine === item.correct ? 'You got it right!' : `The answer was: ${opts[item.correct]}`));
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
      const sent = store.get(`tp.aud.sent.${item.id}`, []);
      sent.push({ id: out.id, text });
      store.set(`tp.aud.sent.${item.id}`, sent.slice(-10));
      input.value = '';
      setNote(out.approved ? 'Sent!' : sentText);
      render();
    } catch (err) {
      setNote(err.message, false);
    } finally {
      send.disabled = false;
    }
  });
  box.appendChild(form);
}

function renderWall(box) {
  const isQA = item.kind === 'qa';
  askForm(box, {
    placeholder: isQA ? 'Ask a question…' : 'Share an idea…', max: 280, multiline: true,
    sentText: 'Sent — the moderator will review it shortly.',
  });
  const kids = item.children || [];
  const onWall = new Set(kids.map((k) => k.id));
  const sent = store.get(`tp.aud.sent.${item.id}`, []).filter((s) => !onWall.has(s.id));
  if (sent.length) {
    const mineBox = el('div', 'tp-aud-mine');
    mineBox.append(el('p', 'label', 'Waiting for review'));
    for (const s of sent.slice(-3)) mineBox.append(el('p', 'text-muted', s.text));
    box.appendChild(mineBox);
  }
  if (item.spotlight) {
    const spot = el('div', 'tp-aud-spot');
    spot.append(el('span', 'label', 'Now answering'), el('p', '', item.spotlight.question));
    box.appendChild(spot);
  }
  const ups = new Set(store.get('tp.aud.up', []));
  const list = el('ul', 'tp-aud-wall');
  for (const k of kids) {
    if (item.spotlight && k.id === item.spotlight.id) continue;
    const li = el('li', 'tp-aud-entry' + (k.state === 'answered' ? ' is-answered' : ''));
    const up = el('button', 'btn btn-sm tp-aud-up', `▲ ${k.upvotes || 0}`);
    up.type = 'button';
    up.setAttribute('aria-pressed', String(ups.has(k.id)));
    up.setAttribute('aria-label', `Upvote: ${k.question}`);
    up.disabled = ups.has(k.id) || k.state !== 'open' || item.state !== 'open';
    up.addEventListener('click', async () => {
      ups.add(k.id);
      store.set('tp.aud.up', [...ups].slice(-200));
      up.disabled = true;
      try { await postRetry('/vote', { pollId: k.id, choice: '1' }); } catch (e) { setNote(e.message, false); }
    });
    li.append(up, el('span', '', k.question));
    list.appendChild(li);
  }
  if (kids.length) box.appendChild(list);
}

function renderCloud(box) {
  askForm(box, { placeholder: 'One word…', max: 32, multiline: false, sentText: 'Sent — it appears once approved.' });
  const words = item.children || [];
  if (!words.length) return;
  const max = Math.max(1, ...words.map((w) => w.upvotes || 0));
  const cloud = el('div', 'tp-aud-cloud');
  for (const w of words) {
    const t = el('span', 'tp-aud-word', w.question);
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

function startRest() {
  if (restTimer) return;
  const tick = async () => {
    try {
      const res = await fetch(`/api/audience/${code}`);
      const j = await res.json();
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

function connect() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  try { ws = new WebSocket(`${proto}://${location.host}/ws`); } catch { return retry(); }
  ws.onopen = () => {
    ws.send(JSON.stringify({ v: 1, t: 'join', role: 'audience', show: code, peerId: peer, joinedAt: Date.now() }));
  };
  ws.onmessage = (ev) => {
    let m;
    try { m = JSON.parse(ev.data); } catch { return; }
    if (m.t === 'poll') {
      backoff = 1000;
      stopRest();
      setNote('Connected');
      adopt(m.poll);
    } else if (m.t === 'err') {
      setNote(m.message || 'Connection problem', false);
    }
  };
  ws.onclose = () => retry();
  ws.onerror = () => { try { ws.close(); } catch { /* */ } };
}
function retry() {
  setNote('Reconnecting…', false);
  startRest();
  const wait = backoff + Math.random() * backoff;
  backoff = Math.min(backoff * 2, 15000);
  setTimeout(connect, wait);
}

connect();
// A phone waking from sleep reconnects at once.
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible' && (!ws || ws.readyState > 1)) connect();
});
