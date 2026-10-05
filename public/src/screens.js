/**
 * screens.js — the room's Screens page (PRODUCT §3.2, S1–S4, S7, S9).
 *
 * Cards for every screen of the room: live status, inline rename
 * (double-click / double-tap), display type, layout (templates for that
 * type, or the screen's own edited layout), theme and rotation — each
 * change applies immediately and is pushed to the screen. Browsers waiting
 * on /d/ are set up (captured) from here with the same choices.
 */
import { api, toast, el, inlineEdit } from './ui.js';
import { tpConfirm } from './dialog.js';

const KIND_LABEL = { audience: 'Audience', walkin: 'Walk-in', presenter: 'Presenter' };
const ROT_LABEL = { 0: 'Normal', 90: 'Portrait ↻', 270: 'Portrait ↺', 180: 'Upside down' };

const page = document.getElementById('tp-screens-page');
const code = page?.dataset.room || '';
const st = { screens: [], waiting: [], themes: [], catalog: [] };
let busy = false; // pause re-render while a select/rename is in use

export function initScreens() {
  if (!page) return;
  Promise.all([
    api('GET', '/api/board-templates').then((j) => { st.catalog = j.catalog || []; }).catch(() => {}),
    api('GET', '/api/theme').then((j) => { st.themes = j.themes || []; }).catch(() => {}),
  ]).then(pull);
  setInterval(pull, 3000);
  initCapture();
  initEditor();
}

export async function pull() {
  if (!page) return;
  const [s, w] = await Promise.all([
    api('GET', `/api/shows/${code}/screens`).catch(() => null),
    api('GET', '/api/waiting').catch(() => null),
  ]);
  if (s) st.screens = s.screens || [];
  if (w) st.waiting = w.waiting || [];
  if (busy) return;
  renderWaiting();
  renderScreens();
}

const post = (path, body) => api('POST', `/api/shows/${code}${path}`, body);
async function apply(fn, msg) {
  try {
    await fn();
    if (msg) toast(msg, 'success');
  } catch (e) {
    toast(e.message, 'danger');
  }
  busy = false;
  pull();
}

/* ------------------------------------------------------------- screens -- */

function themeSelect(value, onchange, label) {
  const sel = el('select', { class: 'select input-sm', 'aria-label': label });
  sel.appendChild(new Option('Event default theme', ''));
  for (const t of st.themes) sel.appendChild(new Option(t, t));
  if (value && !st.themes.includes(value)) sel.appendChild(new Option(`${value} (missing)`, value));
  sel.value = value || '';
  sel.addEventListener('focus', () => { busy = true; });
  sel.addEventListener('blur', () => { busy = false; });
  sel.addEventListener('change', () => onchange(sel.value));
  return sel;
}

function preview(s) {
  const portrait = s.orientation === 'portrait';
  const rows = Math.max(1, s.rows || 8);
  const box = el('div', { class: `tp-scr-prev${portrait ? ' is-portrait' : ''}` });
  for (const w of s.widgets || []) {
    const t = el('span', { class: 'tp-scr-tile', text: w.type });
    t.style.left = `${(w.x / 12) * 100}%`;
    t.style.width = `${(w.w / 12) * 100}%`;
    t.style.top = `${(w.y / rows) * 100}%`;
    t.style.height = `${(w.h / rows) * 100}%`;
    box.appendChild(t);
  }
  if (!(s.widgets || []).length) box.appendChild(el('span', { class: 'tp-scr-empty', text: 'Timer (no layout)' }));
  return box;
}

function card(s) {
  const name = el('span', { class: 'tp-inline-edit tp-scr-name', tabindex: '0', title: 'Double-click to rename', text: s.name });
  inlineEdit(name, (to) => post('/screens/rename', { from: s.name, to }).then(() => pull()));
  const head = el('div', { class: 'tp-scr-head' }, name,
    el('span', { class: `status ${s.connected ? 'status-ok' : 'status-idle'}`, text: s.connected ? 'Live' : 'Offline' }),
    s.kind ? el('span', { class: 'badge', text: KIND_LABEL[s.kind] || s.kind }) : null,
    s.rotation ? el('span', { class: 'badge', text: ROT_LABEL[s.rotation] }) : null);

  const kindSel = el('select', { class: 'select input-sm', 'aria-label': `Display type of ${s.name}` });
  kindSel.appendChild(new Option('Type…', ''));
  for (const [k, v] of Object.entries(KIND_LABEL)) kindSel.appendChild(new Option(v, k));
  kindSel.value = s.kind || '';
  kindSel.addEventListener('focus', () => { busy = true; });
  kindSel.addEventListener('change', () => apply(() => post('/screens/config', { name: s.name, theme: s.theme, boardId: s.boardId, room: s.room, kind: kindSel.value }), 'Display type saved'));

  const tplSel = el('select', { class: 'select input-sm', 'aria-label': `Layout of ${s.name}` });
  tplSel.appendChild(new Option(s.boardName ? `Current: ${s.boardName}` : 'Current layout', ''));
  const pick = st.catalog.filter((t) => !s.kind || t.kind === s.kind);
  for (const t of pick) tplSel.appendChild(new Option(`Use template: ${t.name}`, t.key));
  tplSel.addEventListener('focus', () => { busy = true; });
  tplSel.addEventListener('change', async () => {
    if (!tplSel.value) return;
    const t = st.catalog.find((x) => x.key === tplSel.value);
    if (!(await tpConfirm(`"${s.name}" switches to the "${t?.name}" layout. Its current layout edits are replaced.`, { title: 'Change layout?', ok: 'Change layout' }))) {
      tplSel.value = '';
      busy = false;
      return;
    }
    apply(() => post('/screens/template', { name: s.name, template: tplSel.value }), 'Layout changed');
  });

  const rotSel = el('select', { class: 'select input-sm', 'aria-label': `Rotation of ${s.name}` });
  for (const r of [0, 90, 270, 180]) rotSel.appendChild(new Option(ROT_LABEL[r], String(r)));
  rotSel.value = String(s.rotation || 0);
  rotSel.addEventListener('focus', () => { busy = true; });
  rotSel.addEventListener('change', () => apply(() => post('/screens/config', { name: s.name, theme: s.theme, boardId: s.boardId, room: s.room, rotation: Number(rotSel.value) }), 'Rotation saved'));

  const themeSel = themeSelect(s.theme, (v) => apply(() => post('/screens/config', { name: s.name, theme: v, boardId: s.boardId, room: s.room }), 'Theme saved'), `Theme of ${s.name}`);

  const fields = el('div', { class: 'tp-scr-fields' },
    el('label', { class: 'tp-scr-field' }, el('span', { class: 'label', text: 'Type' }), kindSel),
    el('label', { class: 'tp-scr-field' }, el('span', { class: 'label', text: 'Layout' }), tplSel),
    el('label', { class: 'tp-scr-field' }, el('span', { class: 'label', text: 'Theme' }), themeSel),
    el('label', { class: 'tp-scr-field' }, el('span', { class: 'label', text: 'Mounted' }), rotSel));

  const actions = el('div', { class: 'cluster is-gap-2xs' },
    el('button', { class: 'btn btn-sm btn-primary', type: 'button', onclick: () => openEditor(s) }, 'Edit layout'),
    el('a', { class: 'btn btn-sm', href: `/d/${code}?screen=${encodeURIComponent(s.name)}`, target: '_blank', rel: 'opener', title: 'Open what this screen shows' }, 'View'),
    s.connected ? el('button', {
      class: 'btn btn-sm btn-ghost', type: 'button', title: 'Disconnect this screen (it returns to the ready card)',
      onclick: async () => {
        if (!(await tpConfirm(`"${s.name}" goes back to its ready card until you set it up again.`, { title: 'Disconnect screen?', ok: 'Disconnect', danger: true }))) return;
        for (const pr of s.peers || []) await api('DELETE', `/api/shows/${code}/sessions/${encodeURIComponent(pr.peerId)}`).catch(() => {});
        pull();
      },
    }, 'Disconnect') : null,
    el('button', {
      class: 'btn btn-sm btn-ghost', type: 'button', title: 'Remove from this list',
      onclick: async () => {
        if (!(await tpConfirm(`"${s.name}" is removed from this list. If it is still open it reappears when it reconnects.`, { title: 'Forget screen?', ok: 'Forget', danger: true }))) return;
        apply(() => post('/screens/forget', { name: s.name }));
      },
    }, 'Forget'));

  const now = el('div', { class: 'tp-scr-now text-muted' }, el('span', { class: 'mono', text: s.previewClock || '—' }), ' ', s.previewLabel || 'No session running');
  return el('article', { class: `panel tp-scr${s.connected ? ' is-live' : ''}` }, head, preview(s), now, fields, actions);
}

function renderScreens() {
  const host = document.getElementById('tp-gal');
  if (!host) return;
  const live = st.screens.filter((s) => s.connected).length;
  document.getElementById('tp-gal-count').textContent = `${st.screens.length} · ${live} live`;
  if (!st.screens.length) {
    host.replaceChildren(el('p', { class: 'text-muted', text: 'No screens in this room yet. Open /d/ on a TV, then set it up from “Waiting to be set up”.' }));
    return;
  }
  host.replaceChildren(...st.screens.map(card));
}

/* ------------------------------------------------------------- waiting -- */

function renderWaiting() {
  const panel = document.getElementById('tp-waiting-panel');
  const host = document.getElementById('tp-waiting-list');
  if (!panel || !host) return;
  panel.hidden = !st.waiting.length;
  document.getElementById('tp-waiting-count').textContent = String(st.waiting.length);
  host.replaceChildren(...st.waiting.map((w) => el('div', { class: 'tp-wait-card' },
    el('div', { class: 'tp-wait-name' }, el('strong', { text: w.name }), el('span', { class: 'text-muted', text: w.host || '' })),
    el('div', { class: 'cluster is-gap-2xs' },
      el('button', { class: 'btn btn-sm btn-primary', type: 'button', onclick: () => openCapture(w) }, 'Set up here'),
      el('button', { class: 'btn btn-sm btn-ghost', type: 'button', onclick: () => api('DELETE', `/api/waiting/${w.id}`).then(pull).catch((e) => toast(e.message, 'danger')) }, 'Dismiss')))));
}

/* ------------------------------------------------------------- capture -- */

let capWaiting = null;
let capKind = 'audience';

function fillTemplates() {
  const sel = document.getElementById('tp-capture-template');
  const hint = document.getElementById('tp-capture-template-hint');
  sel.replaceChildren();
  for (const t of st.catalog.filter((x) => x.kind === capKind)) sel.appendChild(new Option(t.name, t.key));
  sel.appendChild(new Option('Plain timer (no layout)', ''));
  const sync = () => {
    const t = st.catalog.find((x) => x.key === sel.value);
    hint.textContent = t ? t.desc : 'The full-screen timer.';
    const rot = document.getElementById('tp-capture-rotation');
    if (t?.layout?.orientation === 'portrait' && rot.value === '0') rot.value = '90';
    if (t && t.layout?.orientation !== 'portrait' && (rot.value === '90' || rot.value === '270')) rot.value = '0';
  };
  sel.onchange = sync;
  sync();
}

function setKind(k) {
  capKind = k;
  for (const b of document.querySelectorAll('#tp-capture-kind [data-kind]')) {
    const on = b.dataset.kind === k;
    b.classList.toggle('is-active', on);
    b.setAttribute('aria-checked', String(on));
  }
  fillTemplates();
}

function nextName() {
  const used = new Set(st.screens.map((s) => s.name));
  const base = { audience: 'Main screen', walkin: 'Walk-in', presenter: 'Presenter' }[capKind] || 'Screen';
  if (!used.has(base)) return base;
  let n = 2;
  while (used.has(`${base} ${n}`)) n++;
  return `${base} ${n}`;
}

function openCapture(w) {
  const dlg = document.getElementById('tp-capture');
  capWaiting = w;
  document.getElementById('tp-capture-host').textContent = w.host ? `· ${w.host}` : '';
  const theme = document.getElementById('tp-capture-theme');
  theme.replaceChildren(new Option('Event default theme', ''), ...st.themes.map((t) => new Option(t, t)));
  document.getElementById('tp-capture-rotation').value = '0';
  dlg.querySelector('.field-error').hidden = true;
  setKind('audience');
  document.getElementById('tp-capture-name').value = nextName();
  busy = true;
  dlg.showModal();
}

function initCapture() {
  const dlg = document.getElementById('tp-capture');
  if (!dlg) return;
  for (const b of dlg.querySelectorAll('#tp-capture-kind [data-kind]')) {
    b.addEventListener('click', () => {
      const nameIn = document.getElementById('tp-capture-name');
      const auto = nameIn.value === nextName();
      setKind(b.dataset.kind);
      if (auto || !nameIn.value) nameIn.value = nextName();
    });
  }
  dlg.querySelector('[data-cancel]').addEventListener('click', () => dlg.close());
  dlg.addEventListener('close', () => { busy = false; });
  dlg.querySelector('form').addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      const out = await api('POST', `/api/waiting/${capWaiting.id}/capture`, {
        code,
        name: document.getElementById('tp-capture-name').value,
        kind: capKind,
        template: document.getElementById('tp-capture-template').value,
        rotation: Number(document.getElementById('tp-capture-rotation').value),
        theme: document.getElementById('tp-capture-theme').value,
      });
      dlg.close();
      toast(`"${out.name}" is set up — it switches over within a few seconds`, 'success');
      for (const d of [500, 1500, 3000, 5000]) setTimeout(pull, d);
    } catch (err) {
      const p = dlg.querySelector('.field-error');
      p.textContent = err.message;
      p.hidden = false;
    }
  });
}

/* -------------------------------------------------------------- editor -- */

function initEditor() {
  const dlg = document.getElementById('tp-screen-edit');
  const frame = document.getElementById('tp-screen-edit-frame');
  document.getElementById('tp-screen-edit-close')?.addEventListener('click', () => dlg.close());
  dlg?.addEventListener('close', () => { frame.src = 'about:blank'; busy = false; pull(); });
}

function openEditor(s) {
  const dlg = document.getElementById('tp-screen-edit');
  const frame = document.getElementById('tp-screen-edit-frame');
  const q = new URLSearchParams({ view: 'board', edit: '1', preview: '1' });
  if (s.boardId) q.set('board', String(s.boardId));
  if (s.theme) q.set('theme', s.theme);
  frame.src = `/d/${code}?${q}`;
  dlg.classList.toggle('is-portrait', s.orientation === 'portrait');
  document.getElementById('tp-screen-edit-title').textContent = `Layout — ${s.name}`;
  busy = true;
  dlg.showModal();
}
