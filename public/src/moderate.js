/**
 * moderate.js — the room moderator's Audience panel (PRODUCT §4.4).
 *
 * Two parts, ftl-themes components (U17):
 * - On air: one card per target (Audience, Presenter) with the live item,
 *   its Show results / Take off air controls and its live tally (poll,
 *   quiz) or moderation queue (Q&A, ideas, word cloud).
 * - Library: a table (STATUS U18; ftl .table.is-stacked turns rows into
 *   cards on phones), one row per item: Type, Title/Question, Audience and
 *   Presenter Show/Hide toggles, Auto Approve, and a ⋯ menu (Edit, Clear
 *   responses, Delete). An off-air row with responses opens a detail row.
 * The queue: Approve / Dismiss pending entries (Approve all); Spotlight /
 * Answered / Dismiss approved ones; Reopen answered and Restore dismissed
 * ones; Answered and Dismiss offer Undo on a toast.
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
let lastSig = '';
let pointerBusy = false;
let renderLater = 0;

export function initModerate(roomCode) {
  host = document.getElementById('tp-aud-items');
  if (!host) return;
  code = roomCode;
  document.getElementById('tp-aud-new')?.addEventListener('click', () => openEditor(null));
  initEditor();
  initItemMenu();
  // A click that lands while the table is being replaced is lost (BUGLOG
  // RW58): while a pointer is down in the panel, renders wait.
  (document.getElementById('tp-aud-panel') || host).addEventListener('pointerdown', () => { pointerBusy = true; });
  document.addEventListener('pointerup', () => { setTimeout(() => { pointerBusy = false; }, 250); });
  refresh(true);
  setInterval(() => { if (Date.now() - lastRefresh > 8000) refresh(); }, 4000);
}

/** refresh re-reads the item list. Live frames (a vote, a submission —
    up to a few a second per room) coalesce to one read per 1.5 s; the
    moderator's own actions pass soon=true for a quick read. An unchanged
    list is not re-rendered (BUGLOG RW58). */
export function refresh(soon = false) {
  if (!host) return;
  if (pending && !soon) return;
  clearTimeout(pending); // a moderator's own action never waits behind a live-frame read
  const gap = soon ? 150 : 1500;
  const wait = Math.max(0, gap - (Date.now() - lastRefresh));
  pending = setTimeout(async () => {
    pending = null;
    lastRefresh = Date.now();
    try {
      const out = await api('GET', `/api/shows/${code}/polls`);
      const sig = JSON.stringify(out.items || []);
      if (sig === lastSig) return;
      lastSig = sig;
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
    refresh(true);
  } catch (e) {
    toast(e.message, 'danger');
  }
}

/* ---------------------------------------------------------------- render -- */

// paintTabBadge fills the Audience tab's badge (Run and Screens pages carry
// the same one, so the tabs never shift between them).
export function paintTabBadge(list) {
  const tabBadge = document.getElementById('tp-aud-tabcount');
  if (!tabBadge) return;
  const pending = list.reduce((n, it) => n + (it.pending || 0), 0);
  const live = list.some((i) => i.toAudience || i.toPresenter);
  tabBadge.hidden = !pending && !live;
  tabBadge.textContent = pending ? `${pending} new` : 'live';
  tabBadge.classList.toggle('badge-accent', pending > 0);
}

const isChoice = (it) => it.kind === 'poll' || it.kind === 'quiz';
const title = (it) => it.question || '(untitled)';
const TARGET = { audience: 'Audience', presenter: 'Presenter' };

function render() {
  if (pointerBusy) {
    clearTimeout(renderLater);
    renderLater = setTimeout(render, 300);
    return;
  }
  const badge = document.getElementById('tp-aud-count');
  const totalPending = items.reduce((n, it) => n + (it.pending || 0), 0);
  if (badge) {
    badge.textContent = totalPending ? `${totalPending} to review` : String(items.length);
    badge.classList.toggle('badge-accent', totalPending > 0);
  }
  paintTabBadge(items);
  // Accordions keep the reader's open/closed choice across live renders.
  const panel = document.getElementById('tp-aud-panel') || host;
  const opened = new Map([...panel.querySelectorAll('details[data-key]')].map((d) => [d.dataset.key, d.open]));
  renderOnAir(opened);
  if (!items.length) {
    host.replaceChildren(el('div', { class: 'empty-state' },
      el('span', { class: 'empty-state-title', text: 'No polls or questions yet.' }),
      el('span', { class: 'empty-state-hint', text: 'Create one with “New item”. It stays hidden until you show it.' })));
    return;
  }
  const tbody = el('tbody');
  for (const it of [...items].reverse()) tbody.append(...rows(it, opened));
  host.replaceChildren(
    el('h3', { class: 'tp-aud-libtitle', text: 'Library' }),
    el('div', { class: 'table-wrap', tabindex: '0', role: 'region', 'aria-label': 'Audience items' },
      el('table', { class: 'table is-stacked tp-aud-table' },
        el('thead', {}, el('tr', {},
          el('th', { scope: 'col', text: 'Type' }),
          el('th', { scope: 'col', text: 'Title / Question' }),
          el('th', { scope: 'col', text: 'Audience' }),
          el('th', { scope: 'col', text: 'Presenter' }),
          el('th', { scope: 'col', text: 'Auto Approve' }),
          el('th', { scope: 'col' }, el('span', { class: 'visually-hidden', text: 'Actions' })))),
        tbody)));
}

/* ---- on air: the live Audience and Presenter items with their controls -- */

function renderOnAir(opened) {
  const air = document.getElementById('tp-aud-onair');
  if (!air) return;
  const onAud = items.find((i) => i.toAudience);
  const onPre = items.find((i) => i.toPresenter);
  const cards = [];
  if (onAud && onPre && onAud.id === onPre.id) cards.push(airCard(onAud, ['audience', 'presenter'], opened));
  else {
    cards.push(onAud ? airCard(onAud, ['audience'], opened) : airEmpty('audience'));
    cards.push(onPre ? airCard(onPre, ['presenter'], opened) : airEmpty('presenter'));
  }
  air.replaceChildren(...cards);
}

function airEmpty(target) {
  return el('article', { class: 'card tp-air-card is-empty', dataset: { target } },
    el('h3', { class: 'tp-air-label', text: `${TARGET[target]} · off air` }),
    el('p', { class: 'text-muted', text: target === 'audience'
      ? 'Phones and audience screens are waiting. Press Show under Audience in the library.'
      : 'The presenter screen shows no item. Press Show under Presenter in the library.' }));
}

function airCard(it, targets, opened) {
  const q = title(it);
  const where = targets.map((t) => TARGET[t]).join(' + ');
  const ctl = el('div', { class: 'cluster tp-air-ctl' });
  if (isChoice(it)) {
    if (it.state === 'results') ctl.append(el('span', { class: 'field-hint', text: 'Voting closed. Clear responses (⋯) runs it again.' }));
    else {
      ctl.append(el('button', {
        type: 'button', class: 'btn btn-secondary', title: 'Voting closes and the results appear wherever this item is shown',
        onclick: (e) => { e.currentTarget.disabled = true; act(`/${it.id}/results`, { on: true }); },
      }, 'Show results'));
    }
  }
  for (const t of targets) {
    ctl.append(el('button', {
      type: 'button', class: 'btn', title: `Take "${q}" off the ${TARGET[t]}`,
      onclick: (e) => { e.currentTarget.disabled = true; act(`/${it.id}/show`, { target: t, on: false }); },
    }, targets.length > 1 ? `Off ${TARGET[t]}` : 'Take off air'));
  }
  ctl.append(moreBtn(it));
  return el('article', { class: 'card tp-air-card', dataset: { target: targets.join(' '), id: String(it.id) } },
    el('div', { class: 'tp-air-head' },
      el('h3', { class: 'tp-air-label', text: `On ${where}` }),
      el('span', { class: 'badge', text: KINDS[it.kind] || it.kind }),
      statusEl(it)),
    el('p', { class: 'tp-air-q', text: q }),
    ctl,
    isChoice(it) ? tally(it) : queue(it, opened, `air${it.id}`));
}

// statusEl: the phase in words. Live is the theme's lit lamp (cyan = live),
// never the error red.
function statusEl(it) {
  if (it.state === 'results') return el('span', { class: 'status status-ok', text: 'Results' });
  if ((it.toAudience || it.toPresenter) && it.state === 'open') {
    return el('span', { class: 'tp-live' }, el('span', { class: 'lamp is-on', 'aria-hidden': 'true' }), 'Live');
  }
  return null;
}

/* ---- library ------------------------------------------------------------ */

// showBtn is a server-confirmed ftl toggle: aria-pressed lights its lamp.
function showBtn(label, on, onchange) {
  const b = el('button', {
    type: 'button', class: 'btn btn-sm tp-aud-showbtn',
    'aria-pressed': String(!!on), title: `${on ? 'Hide' : 'Show'}: ${label}`, 'aria-label': `${on ? 'Hide' : 'Show'}: ${label}`,
    onclick: () => { b.disabled = true; b.setAttribute('aria-busy', 'true'); onchange(!on); },
  }, on ? 'Hide' : 'Show');
  return b;
}

// switchCtl is the ftl-themes switch (U17).
function switchCtl(label, on, onchange) {
  const input = el('input', { type: 'checkbox', 'aria-label': label });
  input.checked = !!on;
  input.addEventListener('change', () => onchange(input.checked));
  return el('label', { class: 'switch', title: label }, input,
    el('span', { class: 'switch-track' }, el('span', { class: 'switch-thumb' })));
}

function icon(name) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('class', 'icon');
  svg.setAttribute('aria-hidden', 'true');
  const use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
  use.setAttribute('href', `/ftl/dist/icons/xbmc.svg#icon-${name}`);
  svg.appendChild(use);
  return svg;
}

// moreBtn opens the shared item menu (Edit, Clear responses, Delete).
function moreBtn(it) {
  const b = el('button', {
    type: 'button', class: 'btn btn-icon btn-ghost tp-aud-more', 'aria-haspopup': 'menu',
    title: 'Edit, clear responses, delete', 'aria-label': `More actions for "${title(it)}"`,
    onclick: () => openItemMenu(it, b),
  }, icon('more-horizontal'));
  return b;
}

let menuItem = null;
function openItemMenu(it, btn) {
  const menu = document.getElementById('tp-aud-menu');
  if (!menu) return;
  menuItem = it;
  try { menu.showPopover({ source: btn }); } catch { menu.showPopover(); }
}
function initItemMenu() {
  const menu = document.getElementById('tp-aud-menu');
  menu?.addEventListener('click', async (e) => {
    const b = e.target.closest('[data-aud-act]');
    if (!b || !menuItem) return;
    const it = menuItem;
    const q = title(it);
    menu.hidePopover();
    const what = b.dataset.audAct;
    if (what === 'edit') return openEditor(it);
    if (what === 'clear') {
      const lost = isChoice(it) ? 'Every vote' : 'Every submission and upvote';
      if (!(await tpConfirm(`${lost} for "${q}" is deleted and voting opens again. The item stays where it is shown.`, { title: 'Clear responses?', ok: 'Clear', danger: true }))) return;
      return act(`/${it.id}/reset`, {}, 'Cleared');
    }
    if (what === 'delete') {
      if (!(await tpConfirm(`"${q}" and all its votes and submissions are deleted.`, { title: 'Delete item?', ok: 'Delete', danger: true }))) return;
      try { await api('DELETE', `${base()}/${it.id}`); refresh(true); } catch (err) { toast(err.message, 'danger'); }
    }
  });
}

// Items whose detail row (tally / moderation queue) is open; kept across
// live re-renders. Items on air have theirs in the On air strip instead.
const expanded = new Set();

function rows(it, opened) {
  const shown = it.toAudience || it.toPresenter;
  const q = title(it);
  const hasDetail = !shown && (isChoice(it) ? (it.total || 0) > 0 : (it.children || []).length > 0);
  const isOpen = hasDetail && expanded.has(it.id);
  const detailID = `tp-aud-detail-${it.id}`;
  // The title is the row's dropdown: one click opens the tally / queue.
  const titleEl = hasDetail
    ? el('button', { type: 'button', class: 'table-toggle', 'aria-expanded': String(isOpen), 'aria-controls': detailID }, q)
    : el('span', { text: q });
  const tr = el('tr', { class: `tp-aud-row${shown ? ' is-active' : ''}${hasDetail ? ' is-expandable' : ''}${isOpen ? ' is-open' : ''}`, dataset: { id: String(it.id) } },
    el('td', { 'data-label': 'Type' }, el('span', { class: 'badge', text: KINDS[it.kind] || it.kind })),
    el('td', { class: 'tp-aud-title', 'data-label': 'Question' }, titleEl, ' ', statusEl(it), ' ',
      it.pending ? el('span', { class: 'badge badge-accent', text: `${it.pending} to review` }) : null),
    el('td', { 'data-label': 'Audience' }, showBtn(`"${q}" to the audience`, it.toAudience,
      (on) => act(`/${it.id}/show`, { target: 'audience', on }))),
    el('td', { 'data-label': 'Presenter' }, showBtn(`"${q}" on the presenter screen`, it.toPresenter,
      (on) => act(`/${it.id}/show`, { target: 'presenter', on }))),
    el('td', { 'data-label': 'Auto Approve' }, SUBMISSIONS.has(it.kind)
      ? switchCtl(`Auto Approve submissions to "${q}"`, it.autoApprove, async (on) => {
        try { await api('PATCH', `${base()}/${it.id}`, { autoApprove: on }); refresh(true); } catch (err) { toast(err.message, 'danger'); }
      })
      : el('span', { class: 'text-muted', text: '—' })),
    el('td', { class: 'tp-aud-actions', 'data-label': 'More' }, moreBtn(it)),
  );
  const out = [tr];
  if (hasDetail) {
    const detail = el('tr', { class: 'tp-aud-detail', id: detailID, dataset: { id: String(it.id) }, hidden: !isOpen },
      el('td', { colspan: '6' }, isChoice(it) ? tally(it) : queue(it, opened, `lib${it.id}`)));
    // A click anywhere on the row (not on its controls) toggles the detail.
    tr.addEventListener('click', (e) => {
      const ctl = e.target.closest('button, input, label, a, select');
      if (ctl && !ctl.classList.contains('table-toggle')) return;
      const now = !expanded.has(it.id);
      if (now) expanded.add(it.id); else expanded.delete(it.id);
      detail.hidden = !now;
      tr.classList.toggle('is-open', now);
      tr.querySelector('.table-toggle')?.setAttribute('aria-expanded', String(now));
    });
    out.push(detail);
  }
  return out;
}

function tally(it) {
  const total = Math.max(1, it.total || 0);
  const t = el('div', { class: 'tp-tally' }, el('div', { class: 'text-muted mono', text: `${it.total || 0} vote${it.total === 1 ? '' : 's'}` }));
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

/* ---- moderation queue --------------------------------------------------- */

// moderate sets an entry's status; undo (the previous status) rides on a
// toast, so a stray tap on a tablet is one tap to take back.
function moderate(k, status, msg, undo) {
  act(`/${k.id}/moderate`, { status });
  if (msg && undo) toast(msg, 'info', { label: 'Undo', run: () => act(`/${k.id}/moderate`, { status: undo }) });
}

// acts: an entry's buttons, kept together (they wrap as one group).
const acts = (...b) => el('span', { class: 'cluster tp-entry-acts' }, ...b);

// section: an accordion that keeps the reader's choice (opened map).
function section(key, label, defOpen, opened, ...body) {
  return el('details', { class: 'accordion-item', open: opened.has(key) ? opened.get(key) : defOpen, dataset: { key } },
    el('summary', { class: 'accordion-trigger' }, label), el('div', { class: 'accordion-panel stack is-gap-xs' }, ...body));
}

function queue(it, opened, key) {
  const kids = it.children || [];
  const cloud = it.kind === 'wordcloud';
  const pend = kids.filter((k) => k.state === 'hidden');
  const wall = kids.filter((k) => k.state === 'open' || k.state === 'answered');
  const gone = kids.filter((k) => k.state === 'dismissed');
  const wrap = el('div', { class: 'stack is-gap-xs tp-queue' });
  if (!pend.length && !wall.length && !gone.length) {
    wrap.appendChild(el('p', { class: 'text-muted', text: it.toAudience
      ? (cloud ? 'No words yet.' : 'Nothing sent yet.')
      : 'Show it to the Audience to start collecting submissions.' }));
    return wrap;
  }
  if (pend.length) {
    const list = el('ul', { class: 'list' });
    for (const k of pend) {
      list.appendChild(el('li', { class: 'list-item tp-entry' },
        el('span', { class: 'tp-entry-text', text: k.question }),
        cloud && k.upvotes > 1 ? el('span', { class: 'mono text-muted', text: `×${k.upvotes}` }) : null,
        acts(el('button', { class: 'btn btn-sm btn-secondary', type: 'button', onclick: () => moderate(k, 'approved') }, 'Approve'),
          el('button', { class: 'btn btn-sm btn-ghost', type: 'button', onclick: () => moderate(k, 'dismissed', `Dismissed “${k.question}”`, 'pending') }, 'Dismiss'))));
    }
    const all = el('button', { class: 'btn btn-sm btn-secondary tp-approve-all', type: 'button',
      onclick: async (e) => {
        e.currentTarget.disabled = true;
        for (const k of pend) { try { await api('POST', `${base()}/${k.id}/moderate`, { status: 'approved' }); } catch (err) { toast(err.message, 'danger'); break; } }
        refresh(true);
      } }, `Approve all (${pend.length})`);
    wrap.appendChild(section(`p${key}`, `To review (${pend.length})`, true, opened, pend.length > 1 ? all : null, list));
  }
  if (wall.length) {
    let body;
    if (cloud) {
      // Words as chips with their count; the chip's × removes the word.
      body = el('div', { class: 'tp-chips' });
      for (const k of wall) {
        body.appendChild(el('span', { class: 'tag' }, k.question,
          el('span', { class: 'mono text-muted', text: ` ×${k.upvotes || 1}` }),
          el('button', { class: 'tag-remove', type: 'button', 'aria-label': `Remove ${k.question}`,
            onclick: () => moderate(k, 'dismissed', `Removed “${k.question}”`, 'approved') })));
      }
    } else {
      body = el('ul', { class: 'list' });
      const spotID = it.spotlight?.id;
      for (const k of wall) {
        const isSpot = k.id === spotID;
        const answered = k.state === 'answered';
        body.appendChild(el('li', { class: `list-item tp-entry${answered ? ' is-answered' : ''}${isSpot ? ' is-spot' : ''}` },
          el('span', { class: 'mono tp-entry-votes', text: `▲${k.upvotes || 0}` }),
          el('span', { class: 'tp-entry-text', text: k.question }),
          answered ? el('span', { class: 'badge', text: 'Answered' }) : null,
          acts(it.kind === 'qa' && !answered
            ? el('button', { class: 'btn btn-sm', type: 'button', 'aria-pressed': String(isSpot), onclick: () => act(`/${it.id}/spotlight`, { entry: isSpot ? 0 : k.id }) }, isSpot ? 'In spotlight' : 'Spotlight')
            : null,
          answered
            ? el('button', { class: 'btn btn-sm', type: 'button', onclick: () => moderate(k, 'approved') }, 'Reopen')
            : el('button', { class: 'btn btn-sm', type: 'button', onclick: () => moderate(k, 'answered', `Marked answered`, 'approved') }, 'Answered'),
          el('button', { class: 'btn btn-sm btn-ghost', type: 'button', onclick: () => moderate(k, 'dismissed', `Dismissed “${k.question}”`, k.state === 'answered' ? 'answered' : 'approved') }, 'Dismiss'))));
      }
    }
    wrap.appendChild(section(`w${key}`, `${cloud ? 'In the cloud' : 'On the wall'} (${wall.length})`, true, opened, body));
  }
  if (gone.length) {
    const list = el('ul', { class: 'list' });
    for (const k of gone) {
      list.appendChild(el('li', { class: 'list-item tp-entry' },
        el('span', { class: 'tp-entry-text text-muted', text: k.question }),
        acts(el('button', { class: 'btn btn-sm', type: 'button', onclick: () => moderate(k, 'approved') }, 'Restore'))));
    }
    wrap.appendChild(section(`d${key}`, `${cloud ? 'Removed' : 'Dismissed'} (${gone.length})`, false, opened, list));
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
      refresh(true);
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
  for (const r of dlg.querySelectorAll('.tp-opt-row .check')) r.hidden = kind !== 'quiz';
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
    el('label', { class: 'check', title: 'Correct answer', hidden: dlg.dataset.kind !== 'quiz' },
      el('input', { type: 'radio', class: 'radio', name: 'tp-correct', checked: correct }),
      el('span', { class: 'visually-hidden', text: `Option ${n + 1} is correct` })),
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
