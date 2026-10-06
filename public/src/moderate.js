/**
 * moderate.js — the room moderator's Audience panel (PRODUCT §4.4).
 *
 * A table (STATUS U18), one row per item, ftl-themes components (U17):
 * Presenter · Audience · Results switches, Type, Title/Question, Approve
 * automatically (submission kinds), Edit and Delete icon buttons. Under an
 * item that has something to show, a detail row holds the live tally
 * (poll, quiz) or the moderation queue (Q&A, ideas, word cloud): Approve /
 * Dismiss pending entries; Spotlight / Answered / Dismiss approved ones.
 *
 * Live: the room WS sends {t:"polls"} after every change (votes and
 * submissions included); refresh() re-reads /api/shows/:code/polls.
 */
import { api, toast, el } from './ui.js';
import { tpConfirm } from './dialog.js';

const KINDS = {
  poll: 'Poll', quiz: 'Quiz', qa: 'Q&A', wordcloud: 'Word cloud', ideas: 'Ideas',
};
const SUBMISSIONS = new Set(['qa', 'wordcloud', 'ideas']);

let code = '';
let host = null;
let items = [];
let pending = null;
let lastRefresh = 0;

export function initModerate(roomCode) {
  host = document.getElementById('tp-aud-items');
  if (!host) return;
  code = roomCode;
  document.getElementById('tp-aud-new')?.addEventListener('click', () => openEditor(null));
  initEditor();
  refresh();
  setInterval(() => { if (Date.now() - lastRefresh > 8000) refresh(); }, 4000);
}

/** refresh — coalesces bursts (1000 votes → a few reads). */
export function refresh() {
  if (!host) return;
  if (pending) return;
  const wait = Math.max(0, 400 - (Date.now() - lastRefresh));
  pending = setTimeout(async () => {
    pending = null;
    lastRefresh = Date.now();
    try {
      const out = await api('GET', `/api/shows/${code}/polls`);
      items = out.items || [];
      render();
    } catch { /* offline: the next frame or tick retries */ }
  }, wait);
}

const base = () => `/api/shows/${code}/polls`;
async function act(path, body, okMsg) {
  try {
    await api('POST', `${base()}${path}`, body || {});
    if (okMsg) toast(okMsg, 'success');
    lastRefresh = 0;
    refresh();
  } catch (e) {
    toast(e.message, 'danger');
  }
}

/* ---------------------------------------------------------------- render -- */

function render() {
  const badge = document.getElementById('tp-aud-count');
  const totalPending = items.reduce((n, it) => n + (it.pending || 0), 0);
  if (badge) {
    badge.textContent = totalPending ? `${totalPending} to review` : String(items.length);
    badge.classList.toggle('badge-accent', totalPending > 0);
  }
  const tabBadge = document.getElementById('tp-aud-tabcount');
  if (tabBadge) {
    const live = items.filter((i) => i.toAudience || i.toPresenter).length;
    tabBadge.hidden = !totalPending && !live;
    tabBadge.textContent = totalPending ? `${totalPending} new` : 'live';
    tabBadge.classList.toggle('badge-accent', totalPending > 0);
  }
  const onAud = items.find((i) => i.toAudience);
  const onPre = items.find((i) => i.toPresenter);
  const air = document.getElementById('tp-aud-onair');
  if (air) {
    air.replaceChildren(
      el('span', { class: 'tp-onair-chip' + (onAud ? ' is-live' : '') }, '📱 Audience: ', el('strong', { text: onAud ? onAud.question : 'nothing' })),
      el('span', { class: 'tp-onair-chip' + (onPre ? ' is-live' : '') }, '🎤 Presenter: ', el('strong', { text: onPre ? onPre.question : 'nothing' })),
    );
  }
  if (!items.length) {
    host.replaceChildren(el('p', { class: 'text-muted', text: 'No polls or questions yet. Create one with “New item”. It stays hidden until you show it.' }));
    return;
  }
  // Keep the open <details> (moderation lists) open across refreshes.
  const open = new Set([...host.querySelectorAll('details[open]')].map((d) => d.dataset.key));
  const tbody = el('tbody');
  for (const it of [...items].reverse()) tbody.append(...rows(it, open));
  host.replaceChildren(el('div', { class: 'table-wrap', tabindex: '0' },
    el('table', { class: 'table tp-aud-table' },
      el('thead', {}, el('tr', {},
        el('th', { scope: 'col', text: 'Presenter' }),
        el('th', { scope: 'col', text: 'Audience' }),
        el('th', { scope: 'col', text: 'Results' }),
        el('th', { scope: 'col', text: 'Type' }),
        el('th', { scope: 'col', text: 'Title / Question' }),
        el('th', { scope: 'col', text: 'Approve automatically' }),
        el('th', { scope: 'col' }, el('span', { class: 'visually-hidden', text: 'Actions' })))),
      tbody)));
}

// switchCtl is the ftl-themes switch (U17). disabled = shown but greyed.
function switchCtl(label, on, onchange, disabled = false) {
  const input = el('input', { type: 'checkbox', 'aria-label': label, disabled });
  input.checked = !!on;
  input.addEventListener('change', () => onchange(input.checked));
  return el('label', { class: 'switch', title: label }, input,
    el('span', { class: 'switch-track' }, el('span', { class: 'switch-thumb' })));
}

function iconBtn(icon, label, cls, onclick) {
  const b = el('button', { class: `btn btn-sm btn-icon ${cls}`, type: 'button', title: label, 'aria-label': label, onclick });
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  const use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
  use.setAttribute('href', `/ftl/dist/icons/xbmc.svg#icon-${icon}`);
  svg.appendChild(use);
  b.appendChild(svg);
  return b;
}

// rows: the item's table row, plus a detail row (tally or queue) when it
// has something to show.
function rows(it, open) {
  const shown = it.toAudience || it.toPresenter;
  const choice = it.kind === 'poll' || it.kind === 'quiz';
  const q = it.question || '(untitled)';
  const status = it.state === 'results'
    ? el('span', { class: 'status status-ok', text: 'Results' })
    : (shown && it.state === 'open' ? el('span', { class: 'status status-rec', text: 'Live' }) : null);
  const tr = el('tr', { class: `tp-aud-row${shown ? ' is-shown' : ''}`, dataset: { id: String(it.id) } },
    el('td', {}, switchCtl(`Show "${q}" to the presenter`, it.toPresenter,
      (on) => act(`/${it.id}/show`, { target: 'presenter', on }))),
    el('td', {}, switchCtl(`Show "${q}" to the audience`, it.toAudience,
      (on) => act(`/${it.id}/show`, { target: 'audience', on }))),
    el('td', {}, choice
      ? switchCtl(`Show results of "${q}"`, it.state === 'results', (on) => act(`/${it.id}/results`, { on }), !shown)
      : el('span', { class: 'text-muted', text: '—' })),
    el('td', {}, el('span', { class: 'badge', text: KINDS[it.kind] || it.kind })),
    el('td', { class: 'tp-aud-q' }, el('span', { text: q }), ' ', status,
      it.pending ? el('span', { class: 'badge badge-accent', text: `${it.pending} to review` }) : null),
    el('td', {}, SUBMISSIONS.has(it.kind)
      ? switchCtl(`Approve submissions to "${q}" automatically`, it.autoApprove, async (on) => {
        try { await api('PATCH', `${base()}/${it.id}`, { autoApprove: on }); refresh(); } catch (err) { toast(err.message, 'danger'); }
      })
      : el('span', { class: 'text-muted', text: '—' })),
    el('td', { class: 'tp-aud-actions' },
      iconBtn('edit', `Edit "${q}"`, 'btn-ghost', () => openEditor(it)),
      iconBtn('trash', `Delete "${q}"`, 'btn-ghost btn-danger', async () => {
        if (!(await tpConfirm(`"${q}" and all its votes and submissions are deleted.`, { title: 'Delete item?', ok: 'Delete', danger: true }))) return;
        try { await api('DELETE', `${base()}/${it.id}`); refresh(); } catch (e) { toast(e.message, 'danger'); }
      })),
  );
  const out = [tr];
  const hasDetail = choice ? (shown || (it.total || 0) > 0) : (shown || (it.children || []).length > 0);
  if (hasDetail) {
    out.push(el('tr', { class: 'tp-aud-detail', dataset: { id: String(it.id) } },
      el('td', { colspan: '7' }, choice ? tally(it) : queue(it, open))));
  }
  return out;
}

function tally(it) {
  const total = Math.max(1, it.total || 0);
  const t = el('div', { class: 'tp-tally' }, el('div', { class: 'text-muted', text: `${it.total || 0} vote${it.total === 1 ? '' : 's'}` }));
  (it.options || []).forEach((o, i) => {
    const n = it.counts?.[i] || 0;
    const pct = Math.round((n / total) * 100);
    const row = el('div', { class: 'tp-tally-row' + (it.kind === 'quiz' && i === it.correct ? ' is-correct' : '') },
      el('span', { text: (it.kind === 'quiz' && i === it.correct ? '✔ ' : '') + o }),
      el('span', { class: 'mono', text: `${n} · ${pct}%` }),
      el('progress', { class: 'progress', max: '100', value: String(pct), 'aria-label': `${o}: ${pct}%` }));
    t.appendChild(row);
  });
  return t;
}

function queue(it, open) {
  const kids = it.children || [];
  const pend = kids.filter((k) => k.state === 'hidden');
  const wall = kids.filter((k) => k.state === 'open' || k.state === 'answered');
  const wrap = el('div', { class: 'stack is-gap-xs' });
  if (!it.toAudience && !pend.length && !wall.length) {
    wrap.appendChild(el('p', { class: 'text-muted', text: 'Show it to the Audience to start collecting submissions.' }));
  }
  if (pend.length) {
    const d = el('details', { class: 'tp-queue', open: true, dataset: { key: `p${it.id}` } },
      el('summary', {}, `To review (${pend.length})`));
    for (const k of pend) {
      d.appendChild(el('div', { class: 'tp-entry' },
        el('span', { class: 'tp-entry-text', text: k.question }),
        el('button', { class: 'btn btn-sm btn-primary', type: 'button', onclick: () => act(`/${k.id}/moderate`, { status: 'approved' }) }, 'Approve'),
        el('button', { class: 'btn btn-sm btn-ghost', type: 'button', onclick: () => act(`/${k.id}/moderate`, { status: 'dismissed' }) }, 'Dismiss')));
    }
    wrap.appendChild(d);
  }
  if (wall.length) {
    const label = it.kind === 'wordcloud' ? `In the cloud (${wall.length})` : `On the wall (${wall.length})`;
    const d = el('details', { class: 'tp-queue', open: open.has(`w${it.id}`) || it.kind !== 'wordcloud', dataset: { key: `w${it.id}` } },
      el('summary', {}, label));
    const spotID = it.spotlight?.id;
    for (const k of wall) {
      const isSpot = k.id === spotID;
      d.appendChild(el('div', { class: `tp-entry${k.state === 'answered' ? ' is-answered' : ''}${isSpot ? ' is-spot' : ''}` },
        el('span', { class: 'mono tp-entry-votes', text: `▲${k.upvotes || 0}` }),
        el('span', { class: 'tp-entry-text', text: k.question }),
        it.kind === 'qa' && k.state === 'open'
          ? el('button', { class: `btn btn-sm${isSpot ? ' btn-primary' : ''}`, type: 'button', onclick: () => act(`/${it.id}/spotlight`, { entry: isSpot ? 0 : k.id }) }, isSpot ? 'Clear spotlight' : 'Spotlight')
          : null,
        it.kind !== 'wordcloud' && k.state === 'open'
          ? el('button', { class: 'btn btn-sm', type: 'button', onclick: () => act(`/${k.id}/moderate`, { status: 'answered' }) }, 'Answered')
          : null,
        el('button', { class: 'btn btn-sm btn-ghost', type: 'button', onclick: () => act(`/${k.id}/moderate`, { status: 'dismissed' }) }, it.kind === 'wordcloud' ? 'Remove' : 'Dismiss')));
    }
    wrap.appendChild(d);
  }
  return wrap;
}

/* ---------------------------------------------------------------- editor -- */

let editing = null;

function initEditor() {
  const dlg = document.getElementById('tp-aud-editor');
  if (!dlg) return;
  const form = dlg.querySelector('form');
  for (const b of dlg.querySelectorAll('[data-kind]')) {
    b.addEventListener('click', () => setKind(b.dataset.kind));
  }
  dlg.querySelector('#tp-aud-add-opt')?.addEventListener('click', () => addOption(''));
  dlg.querySelector('[data-cancel]')?.addEventListener('click', () => dlg.close());
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const kind = dlg.dataset.kind;
    const question = dlg.querySelector('#tp-aud-question').value.trim();
    const rows = [...dlg.querySelectorAll('.tp-opt-row')];
    // The correct answer's index is counted while blanks are dropped, so
    // two options with the same text can't pick the wrong one (RS15).
    const kept = [];
    let correct = -1;
    for (const r of rows) {
      const v = r.querySelector('input[type=text]').value.trim();
      if (!v) continue;
      if (r.querySelector('input[type=radio]')?.checked) correct = kept.length;
      kept.push(v);
    }
    const body = { kind, question, autoApprove: dlg.querySelector('#tp-aud-auto').checked };
    if (kind === 'poll' || kind === 'quiz') body.options = kept;
    if (kind === 'quiz') body.correct = correct;
    try {
      if (editing) await api('PATCH', `${base()}/${editing.id}`, body);
      else await api('POST', base(), body);
      dlg.close();
      toast(editing ? 'Saved' : 'Created — hidden until you show it', 'success');
      refresh();
    } catch (err) {
      const p = dlg.querySelector('.field-error');
      p.textContent = err.message;
      p.hidden = false;
    }
  });
}

function setKind(kind) {
  const dlg = document.getElementById('tp-aud-editor');
  dlg.dataset.kind = kind;
  for (const b of dlg.querySelectorAll('[data-kind]')) {
    b.classList.toggle('is-active', b.dataset.kind === kind);
    b.setAttribute('aria-pressed', String(b.dataset.kind === kind));
  }
  const choice = kind === 'poll' || kind === 'quiz';
  dlg.querySelector('#tp-aud-opts-field').hidden = !choice;
  dlg.querySelector('#tp-aud-auto-field').hidden = choice;
  for (const r of dlg.querySelectorAll('.tp-opt-row input[type=radio]')) r.hidden = kind !== 'quiz';
  dlg.querySelector('#tp-aud-quiz-hint').hidden = kind !== 'quiz';
  const ph = { poll: 'What should we ask?', quiz: 'Quiz question', qa: 'e.g. Questions for the panel', wordcloud: 'e.g. One word for today', ideas: 'e.g. How could we improve?' };
  dlg.querySelector('#tp-aud-question').placeholder = ph[kind] || '';
  if (choice && !dlg.querySelectorAll('.tp-opt-row').length) { addOption(''); addOption(''); }
}

function addOption(value, correct = false) {
  const dlg = document.getElementById('tp-aud-editor');
  const list = dlg.querySelector('#tp-aud-opt-list');
  const n = list.children.length;
  const row = el('div', { class: 'tp-opt-row' },
    el('input', { type: 'radio', name: 'tp-correct', 'aria-label': `Option ${n + 1} is correct`, title: 'Correct answer', checked: correct, hidden: dlg.dataset.kind !== 'quiz' }),
    el('input', { type: 'text', class: 'input input-sm', maxlength: 120, placeholder: `Option ${n + 1}`, value, 'aria-label': `Option ${n + 1}` }),
    el('button', { type: 'button', class: 'btn btn-sm btn-ghost', 'aria-label': 'Remove option', onclick: (e) => e.currentTarget.parentElement.remove() }, '✕'));
  list.appendChild(row);
}

function openEditor(it) {
  const dlg = document.getElementById('tp-aud-editor');
  if (!dlg) return;
  editing = it;
  dlg.querySelector('#tp-aud-editor-title').textContent = it ? 'Edit item' : 'New audience item';
  dlg.querySelector('.field-error').hidden = true;
  dlg.querySelector('#tp-aud-opt-list').replaceChildren();
  dlg.querySelector('#tp-aud-question').value = it?.question || '';
  dlg.querySelector('#tp-aud-auto').checked = !!it?.autoApprove;
  for (const b of dlg.querySelectorAll('[data-kind]')) b.disabled = !!it && b.dataset.kind !== it.kind;
  dlg.dataset.kind = it?.kind || 'poll';
  (it?.options || []).forEach((o, i) => addOption(o, i === it.correct));
  setKind(dlg.dataset.kind);
  dlg.showModal();
  dlg.querySelector('#tp-aud-question').focus();
}
