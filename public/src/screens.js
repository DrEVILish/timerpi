/**
 * screens.js — the room's Screens page (PRODUCT §3.2, S1–S4, S7, S9).
 *
 * Cards for every screen of the room: live status, inline rename
 * (double-click / double-tap), display type, layout (templates for that
 * type, or the screen's own edited layout), theme and rotation — each
 * change applies immediately and is pushed to the screen. Browsers waiting
 * on /d/ are set up (captured) from here with the same choices.
 */
import { api, toast, el, inlineEdit, isEditingIn } from './ui.js';
import { tpConfirm, tpPrompt } from './dialog.js';

const KIND_LABEL = { audience: 'Audience', walkin: 'Walk-in', presenter: 'Presenter' };
const ROT_LABEL = { 0: 'Normal', 90: 'Portrait ↻', 270: 'Portrait ↺', 180: 'Upside down' };

const page = document.getElementById('tp-screens-page');
const code = page?.dataset.room || '';
const st = { screens: [], waiting: [], themes: [], catalog: [], layouts: [] };
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
  initPresets();
}

export async function pull(force = false) {
  if (!page) return;
  const [s, w, l] = await Promise.all([
    api('GET', `/api/shows/${code}/screens`).catch(() => null),
    api('GET', '/api/waiting').catch(() => null),
    api('GET', `/api/shows/${code}/boards`).catch(() => null),
  ]);
  if (s) st.screens = s.screens || [];
  if (w) st.waiting = w.waiting || [];
  if (l) st.layouts = l.boards || l || [];
  // A rename box or dropdown in use: skip this redraw (U13, BUGLOG RW37).
  if (force !== true && (busy || isEditingIn(document.getElementById('tp-screens-page')))) return;
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
  pull(true); // the control just used still has focus: redraw anyway
}

/* ------------------------------------------------------------- screens -- */

function themeSelect(value, onchange, label) {
  const sel = el('select', { class: 'select input-sm', 'aria-label': label });
  sel.appendChild(new Option('Event default theme', ''));
  for (const t of st.themes) sel.appendChild(new Option(t, t));
  if (value && !st.themes.includes(value)) sel.appendChild(new Option(`${value} (missing)`, value));
  sel.value = value || '';
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
  kindSel.addEventListener('change', () => apply(() => post('/screens/config', { name: s.name, theme: s.theme, boardId: s.boardId, room: s.room, kind: kindSel.value }), 'Display type saved'));

  // Layout: one list (PRODUCT 2026-10-06) — the plain timer, the event's
  // layouts, then the built-ins of this display type labelled [built-in].
  // You pick a layout, not a shape: every layout has a landscape and a
  // portrait version, and the screen's orientation picks one (owner
  // 2026-10-07). Every pick applies at once; built-ins are shown as they
  // are (editing makes a named copy).
  const tplSel = el('select', { class: 'select input-sm', 'aria-label': `Layout of ${s.name}` });
  tplSel.appendChild(new Option('Plain timer (no layout)', 'b:0'));
  for (const b of st.layouts) tplSel.appendChild(new Option(b.name || `Layout ${b.id}`, `b:${b.id}`));
  for (const t of st.catalog.filter((x) => x.key === s.template || !s.kind || x.kind === s.kind)) tplSel.appendChild(new Option(`[built-in] ${t.name}`, `t:${t.key}`));
  tplSel.value = s.boardId ? `b:${s.boardId}` : (s.template ? `t:${s.template}` : 'b:0');
  tplSel.addEventListener('change', () => {
    const [kind, val] = tplSel.value.split(':');
    if (kind === 'b' && Number(val) > 0) {
      apply(() => post('/screens/config', { name: s.name, theme: s.theme, boardId: Number(val), room: s.room }), 'Layout changed');
    } else {
      apply(() => post('/screens/template', { name: s.name, template: kind === 't' ? val : '' }), 'Layout changed');
    }
  });

  const rotSel = el('select', { class: 'select input-sm', 'aria-label': `Rotation of ${s.name}` });
  for (const r of [0, 90, 270, 180]) rotSel.appendChild(new Option(ROT_LABEL[r], String(r)));
  rotSel.value = String(s.rotation || 0);
  rotSel.addEventListener('change', () => apply(() => post('/screens/config', { name: s.name, theme: s.theme, boardId: s.boardId, room: s.room, rotation: Number(rotSel.value) }), 'Rotation saved'));

  const themeSel = themeSelect(s.theme, (v) => apply(() => post('/screens/config', { name: s.name, theme: v, boardId: s.boardId, room: s.room }), 'Theme saved'), `Theme of ${s.name}`);

  const fields = el('div', { class: 'tp-scr-fields' },
    el('label', { class: 'tp-scr-field' }, el('span', { class: 'label', text: 'Type' }), kindSel),
    // A phone or tablet follows the way it is held: no Mounted setting.
    s.handheld ? null : el('label', { class: 'tp-scr-field' }, el('span', { class: 'label', text: 'Mounted' }), rotSel),
    el('label', { class: 'tp-scr-field' }, el('span', { class: 'label', text: 'Layout' }), tplSel),
    el('label', { class: 'tp-scr-field' }, el('span', { class: 'label', text: 'Theme' }), themeSel));

  const actions = el('div', { class: 'cluster is-gap-2xs' },
    el('button', { class: 'btn btn-sm btn-primary', type: 'button', onclick: () => editLayout(s) }, 'Edit layout'),
    el('a', { class: 'btn btn-sm', href: `/d/${code}?screen=${encodeURIComponent(s.name)}`, target: '_blank', rel: 'opener', title: 'Open what this screen shows' }, 'View'),
    el('button', {
      class: 'btn btn-sm btn-ghost', type: 'button', title: 'The address to open on this screen by hand (kiosk start page, TV bookmark)',
      onclick: async () => {
        try {
          const out = await post('/screens/link', { name: s.name });
          let copied = false;
          try { await navigator.clipboard.writeText(out.link); copied = true; } catch { /* plain-http box: no clipboard API */ }
          await tpPrompt(copied ? 'Copied. This is the screen\'s own address; keep it private, it lets a browser show this screen\'s stage messages and notes.'
            : 'This is the screen\'s own address. Copy it; keep it private, it lets a browser show this screen\'s stage messages and notes.',
          out.link, { title: `Screen link · ${s.name}`, ok: 'Done' });
        } catch (e) { toast(e.message, 'danger'); }
      },
    }, 'Screen link'),
    s.connected ? el('button', {
      class: 'btn btn-sm btn-ghost', type: 'button', title: 'Disconnect this screen (it returns to the ready card)',
      onclick: async () => {
        if (!(await tpConfirm(`"${s.name}" goes back to its ready card until you set it up again.`, { title: 'Disconnect screen?', ok: 'Disconnect', danger: true }))) return;
        for (const pr of s.peers || []) await api('DELETE', `/api/shows/${code}/sessions/${encodeURIComponent(pr.peerId)}`).catch(() => {});
        pull();
      },
    }, 'Disconnect') : null,
    null);

  const now = el('div', { class: 'tp-scr-now text-muted' }, el('span', { class: 'mono', text: s.previewClock || '—' }), ' ', s.previewLabel || 'No session running');
  // Forget = the theme's close button (ftl `.btn-close`, STATUS U12/U44)
  // in the card's top-right corner, tinted danger. It releases the screen:
  // its settings and key are dropped.
  const forget = el('button', {
    class: 'btn-close tp-scr-forget', type: 'button',
    title: `Forget ${s.name}`, 'aria-label': `Forget screen ${s.name}`,
    onclick: async () => {
      if (!(await tpConfirm(`"${s.name}" is removed from this list and released: its settings and screen link stop working. If it is open, it goes back to the ready screen, waiting to be captured.`, { title: 'Forget screen?', ok: 'Forget', danger: true }))) return;
      apply(() => post('/screens/forget', { name: s.name }), 'Screen forgotten');
    },
  });
  return el('article', { class: `panel stack is-gap-s tp-scr${s.connected ? ' is-live' : ''}` }, forget, head, preview(s), now, fields, actions);
}

function renderScreens() {
  const host = document.getElementById('tp-gal');
  if (!host) return;
  const live = st.screens.filter((s) => s.connected).length;
  document.getElementById('tp-gal-count').textContent = `${st.screens.length} · ${live} live`;
  if (!st.screens.length) {
    host.replaceChildren(el('div', { class: 'empty-state' },
      el('span', { class: 'empty-state-title', text: 'No screens in this room yet.' }),
      el('span', { class: 'empty-state-hint', text: 'Open /d/ on a TV, then set it up from “Waiting to be set up”.' })));
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
  host.replaceChildren(...st.waiting.map((w) => el('div', { class: 'card stack is-gap-xs' },
    el('div', { class: 'stack is-gap-none' }, el('strong', { text: w.name }), el('span', { class: 'text-muted', text: w.host || '' })),
    el('div', { class: 'cluster is-gap-2xs' },
      el('button', { class: 'btn btn-sm btn-primary', type: 'button', onclick: () => openCapture(w) }, 'Set up here'),
      el('button', { class: 'btn btn-sm btn-ghost', type: 'button', onclick: () => api('DELETE', `/api/waiting/${w.id}`).then(pull).catch((e) => toast(e.message, 'danger')) }, 'Dismiss')))));
}

/* ------------------------------------------------------------- capture -- */

let capWaiting = null;
let capKind = 'audience';

function fillTemplates() {
  // The layouts of the chosen display type (each has both versions; the
  // screen's orientation picks one).
  const sel = document.getElementById('tp-capture-template');
  const hint = document.getElementById('tp-capture-template-hint');
  const keep = sel.value;
  sel.replaceChildren();
  for (const t of st.catalog.filter((x) => x.kind === capKind)) sel.appendChild(new Option(t.name, t.key));
  sel.appendChild(new Option('Plain timer (no layout)', ''));
  if ([...sel.options].some((o) => o.value === keep)) sel.value = keep;
  const sync = () => {
    const t = st.catalog.find((x) => x.key === sel.value);
    hint.textContent = t ? t.desc : 'The full-screen timer.';
  };
  sel.onchange = sync;
  sync();
}

function setKind(k) {
  capKind = k;
  for (const r of document.querySelectorAll('#tp-capture-kind input[type=radio]')) r.checked = r.value === k;
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
  // A phone or tablet follows the way it is held: no Mounted setting.
  document.getElementById('tp-capture-rotation').closest('.field').hidden = !!w.handheld;
  dlg.querySelector('.field-error').hidden = true;
  setKind('audience');
  document.getElementById('tp-capture-name').value = nextName();
  busy = true;
  dlg.showModal();
}

function initCapture() {
  const dlg = document.getElementById('tp-capture');
  if (!dlg) return;
  for (const r of dlg.querySelectorAll('#tp-capture-kind input[type=radio]')) {
    r.addEventListener('change', () => {
      const nameIn = document.getElementById('tp-capture-name');
      const auto = nameIn.value === nextName();
      setKind(r.value);
      if (auto || !nameIn.value) nameIn.value = nextName();
    });
  }
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

// Edit layout (STATUS U8/U11): a screen on one of the event's layouts opens
// the editor on it; a screen on a built-in (or the plain timer) first makes
// a named copy for the event.
function editLayout(s) {
  if (s.boardId) {
    const b = st.layouts.find((x) => x.id === s.boardId);
    openEditor({ id: s.boardId, name: b?.name || s.boardName || 'Layout', usedBy: b?.usedBy || 0, theme: s.theme, orientation: s.orientation });
    return;
  }
  openCopy(s);
}

let copyFor = null;
async function openCopy(s) {
  const dlg = document.getElementById('tp-layout-copy');
  copyFor = s;
  const from = document.getElementById('tp-layout-copy-from');
  from.textContent = '';
  for (const t of st.catalog.filter((x) => !s.kind || x.kind === s.kind)) from.appendChild(new Option(`[built-in] ${t.name}`, t.key));
  if (s.template) from.value = s.template;
  const tName = () => st.catalog.find((x) => x.key === from.value)?.name || 'Layout';
  const nameIn = document.getElementById('tp-layout-copy-name');
  nameIn.value = `${tName()} (${page.dataset.roomName || 'event'})`;
  from.onchange = () => { nameIn.value = `${tName()} (${page.dataset.roomName || 'event'})`; };
  const list = document.getElementById('tp-layout-copy-list');
  list.textContent = '';
  const err = dlg.querySelector('.field-error');
  err.hidden = true;
  try {
    const q = s.kind ? `?kind=${encodeURIComponent(s.kind)}` : '';
    const out = await api('GET', `/api/shows/${code}/layout-targets${q}`);
    for (const r of out.rooms || []) {
      if (!r.screens.length) continue;
      const box = el('fieldset', { class: 'radio-group' }, el('legend', { text: r.name }));
      for (const sc of r.screens) {
        const cb = el('input', { type: 'checkbox', class: 'checkbox', dataset: { room: r.room, name: sc.name } });
        cb.checked = r.here && sc.name === s.name;
        box.appendChild(el('label', { class: 'check' }, cb, sc.name));
      }
      list.appendChild(box);
    }
  } catch (e) { toast(e.message, 'danger'); }
  if (!list.children.length) list.appendChild(el('div', { class: 'empty-state' }, el('span', { class: 'empty-state-title', text: 'No other screens of this type yet.' })));
  busy = true;
  dlg.showModal();
  nameIn.select();
}

function initCopy() {
  const dlg = document.getElementById('tp-layout-copy');
  if (!dlg) return;
  dlg.addEventListener('close', () => { busy = false; });
  dlg.querySelector('form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const screens = [...dlg.querySelectorAll('#tp-layout-copy-list input:checked')].map((cb) => ({ room: cb.dataset.room, name: cb.dataset.name }));
    const name = document.getElementById('tp-layout-copy-name').value.trim();
    const template = document.getElementById('tp-layout-copy-from').value;
    try {
      const out = await api('POST', `/api/shows/${code}/layouts`, { name, template, screens });
      dlg.close();
      toast(`"${out.name}" created${screens.length ? ` for ${screens.length} screen${screens.length === 1 ? '' : 's'}` : ''}`, 'success');
      await pull();
      openEditor({ id: out.boardId, name: out.name, usedBy: screens.length, theme: copyFor?.theme, orientation: copyFor?.orientation });
    } catch (err) {
      const p = dlg.querySelector('.field-error');
      p.textContent = err.message;
      p.hidden = false;
    }
  });
}

function initEditor() {
  initCopy();
  const dlg = document.getElementById('tp-screen-edit');
  const frame = document.getElementById('tp-screen-edit-frame');
  dlg?.addEventListener('close', () => { frame.src = 'about:blank'; busy = false; pull(); });
}

// The editor: an almost-full-screen modal straight into editing mode
// (compose=1: no second "Edit layout" press), on one layout.
function openEditor(b) {
  const dlg = document.getElementById('tp-screen-edit');
  const frame = document.getElementById('tp-screen-edit-frame');
  const q = new URLSearchParams({ view: 'board', edit: '1', preview: '1', compose: '1', board: String(b.id) });
  if (b.theme) q.set('theme', b.theme);
  // Open on the version this screen shows; the editor switches versions.
  q.set('orient', b.orientation === 'portrait' ? 'portrait' : 'landscape');
  frame.src = `/d/${code}?${q}`;
  dlg.classList.toggle('is-portrait', b.orientation === 'portrait');
  document.getElementById('tp-screen-edit-title').textContent = `Editing: ${b.name}`;
  const shared = document.getElementById('tp-screen-edit-shared');
  shared.hidden = !(b.usedBy > 1);
  shared.textContent = `Used by ${b.usedBy} screens: changes show on all of them`;
  busy = true;
  dlg.showModal();
}

/* ------------------------------------------------------------- presets --
 * Named bundles of this room's screen settings (moved from the retired
 * Setup tab, STATUS U29); they move between appliances as JSON files. */

const presetsPath = `/api/shows/${code}/presets`;

async function presetsRefresh() {
  const host = document.getElementById('tp-presets');
  let list;
  try { list = (await api('GET', presetsPath)).presets || []; } catch { return; } // keep last list
  if (!host) return;
  if (!list.length) { host.replaceChildren(el('div', { class: 'empty-state' }, el('span', { class: 'empty-state-title', text: 'No presets saved yet.' }))); return; }
  host.replaceChildren(el('ul', { class: 'list' }, ...list.map((pr) => el('li', { class: 'list-item' },
    el('span', { text: pr.name }),
    el('button', { type: 'button', class: 'btn btn-sm btn-primary push', text: 'Apply',
      onclick: () => api('POST', `${presetsPath}/${pr.id}/apply`)
        .then(() => toast('Preset applied to all named screens'), (e) => toast(e.message, 'danger')) }),
    el('button', { type: 'button', class: 'btn btn-sm', text: 'Export',
      onclick: () => { location.href = `${presetsPath}/${pr.id}/export`; } }),
    el('button', { type: 'button', class: 'btn btn-sm btn-icon btn-ghost', text: '✕', 'aria-label': `Delete preset ${pr.name}`,
      onclick: async () => {
        if (!(await tpConfirm('The preset is removed for every operator of this show.', { title: 'Delete this preset?', ok: 'Delete', danger: true }))) return;
        try { await api('DELETE', `${presetsPath}/${pr.id}`); } catch (e) { toast(e.message, 'danger'); }
        presetsRefresh();
      } })))));
}

function initPresets() {
  presetsRefresh();
  document.getElementById('tp-preset-save')?.addEventListener('submit', async (e) => {
    e.preventDefault();
    const name = e.target.querySelector('input[name="name"]')?.value.trim();
    if (!name) return;
    try {
      await api('POST', presetsPath, { name });
      toast(`Preset "${name}" saved`);
      e.target.reset();
      presetsRefresh();
    } catch (err) { toast(err.message, 'danger'); }
  });
  document.getElementById('tp-preset-import')?.addEventListener('change', async (e) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    let preset;
    try { preset = JSON.parse(await file.text()); } catch { toast('not a readable JSON preset', 'danger'); return; }
    try {
      const j = await api('POST', `${presetsPath}/import`, preset);
      toast(`Preset "${j.name}" imported`);
      presetsRefresh();
    } catch (err) { toast(err.message, 'danger'); }
  });
}
