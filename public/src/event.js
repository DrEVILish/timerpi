/**
 * event.js — client for the event layer pages:
 *   home         join an event by code · create an event · recent events
 *   event        lobby: moderator room sign-in · SuperOperator sign-in
 *   event-admin  SuperOperator dashboard: live rooms, room admin, settings
 */
import {
  api, toast, showError, normalizeCode, fmtCode, fmtRemaining, el,
  recentEvents, rememberEvent, forgetEvent, inlineEdit,
} from './ui.js';
import { tpConfirm, tpPrompt } from './dialog.js';

const body = document.body;
const page = body.dataset.page;
const EV = body.dataset.event || '';

if (page === 'home') initHome();
if (page === 'event') initLobby();
if (page === 'event-admin') initAdmin();

/* ------------------------------------------------------------------ home -- */
function initHome() {
  const codeIn = document.getElementById('join-code');
  codeIn?.addEventListener('input', () => {
    const c = normalizeCode(codeIn.value).slice(0, 8);
    codeIn.value = c.length > 4 ? `${c.slice(0, 4)}-${c.slice(4)}` : c;
  });
  document.getElementById('join-form')?.addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = document.getElementById('join-error');
    const code = normalizeCode(codeIn.value);
    if (code.length !== 8) return showError(err, 'An event code has 8 characters, like K7QP-M3XB.');
    try {
      await api('GET', `/api/events/${code}`);
      location.href = `/e/${code}`;
    } catch (ex) {
      showError(err, ex.message === 'Unknown event code' ? 'No event with that code on this box. Check the code with your SuperOperator.' : ex.message);
    }
  });

  const rooms = document.getElementById('ev-rooms');
  document.getElementById('ev-add-room')?.addEventListener('click', () => {
    const n = rooms.querySelectorAll('input').length;
    const input = el('input', { class: 'input input-sm', name: 'room', maxlength: 60, 'aria-label': 'Room name', value: `Room ${String.fromCharCode(65 + Math.min(n, 25))}` });
    rooms.appendChild(input);
    input.focus();
    input.select();
  });
  document.getElementById('create-form')?.addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = document.getElementById('create-error');
    const name = document.getElementById('ev-name').value.trim();
    const pw = document.getElementById('ev-pw').value;
    const pw2 = document.getElementById('ev-pw2').value;
    if (pw !== pw2) return showError(err, 'The two passwords do not match.');
    const roomNames = [...rooms.querySelectorAll('input')].map((i) => i.value.trim()).filter(Boolean);
    try {
      const out = await api('POST', '/api/events', { name, password: pw, rooms: roomNames });
      rememberEvent(out.code, name);
      location.href = out.admin;
    } catch (ex) {
      showError(err, ex.message);
    }
  });

  const list = document.getElementById('recent-list');
  const recent = recentEvents();
  if (list && recent.length) {
    document.getElementById('recent-panel').hidden = false;
    for (const ev of recent) {
      list.appendChild(el('li', { class: 'tp-recent-item' },
        el('a', { href: `/e/${ev.code}`, class: 'tp-recent-link' }, el('strong', { text: ev.name }), ' ', el('span', { class: 'mono text-muted', text: fmtCode(ev.code) })),
        el('button', { class: 'btn btn-sm btn-ghost', type: 'button', title: 'Forget on this device', 'aria-label': `Forget ${ev.name}`, onclick: (e) => { forgetEvent(ev.code); e.currentTarget.closest('li').remove(); } }, '✕'),
      ));
    }
  }
}

/* ----------------------------------------------------------------- lobby -- */
function initLobby() {
  rememberEvent(EV, body.dataset.eventName);
  for (const form of document.querySelectorAll('[data-room-login]')) {
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const room = form.dataset.roomLogin;
      const pw = form.querySelector('[name=pw]')?.value || '';
      try {
        const out = await api('POST', `/api/events/${EV}/rooms/${room}/login`, { pw });
        location.href = out.room;
      } catch (ex) {
        showError(form.querySelector('.field-error'), ex.message);
      }
    });
  }
  const superForm = document.getElementById('super-login');
  superForm?.addEventListener('submit', async (e) => {
    e.preventDefault();
    try {
      const out = await api('POST', `/api/events/${EV}/login`, { pw: document.getElementById('super-pw')?.value || '' });
      location.href = out.admin;
    } catch (ex) {
      showError(document.getElementById('super-error'), ex.message);
    }
  });
  if (new URLSearchParams(location.search).get('signin') === 'super') {
    document.getElementById('super-door')?.scrollIntoView({ block: 'center' });
    document.getElementById('super-pw')?.focus();
  }
}

/* ----------------------------------------------------------------- admin -- */
function initAdmin() {
  rememberEvent(EV, body.dataset.eventName);
  const grid = document.getElementById('live-grid');
  const lamp = document.getElementById('live-lamp');
  const status = document.getElementById('live-status');
  let rooms = [];
  let fetchedAt = 0;

  const run = async (fn, okMsg) => {
    try {
      await fn();
      if (okMsg) toast(okMsg, 'success');
      poll();
    } catch (ex) {
      toast(ex.message, 'danger');
    }
  };
  const verb = (v, room) => run(() => api('POST', `/api/events/${EV}/verb`, { verb: v, room }));

  function stateOf(r) {
    if (r.blanked) return ['Blacked out', 'status-error'];
    if (r.running && r.paused) return ['Paused', 'status-warn'];
    if (r.running && r.overtime) return ['Overtime', 'status-error'];
    if (r.running) return ['Running', 'status-ok'];
    return ['Idle', 'status-idle'];
  }

  function render() {
    grid.replaceChildren();
    if (!rooms.length) {
      grid.appendChild(el('p', { class: 'text-muted', text: 'No rooms yet. Add one below.' }));
      return;
    }
    for (const r of rooms) {
      const [label, cls] = stateOf(r);
      const card = el('article', { class: 'panel tp-live-card', dataset: { room: r.code } },
        el('div', { class: 'panel-header' }, el('span', { text: r.name }), el('span', { class: `status ${cls} push`, text: label })),
        el('div', { class: 'tp-live-clock mono', dataset: { remaining: '1' }, text: r.running ? fmtRemaining(r.remainingMS) : '—' }),
        el('dl', { class: 'tp-live-facts' },
          el('dt', { text: 'Now' }), el('dd', { text: r.activeLabel || '—' }),
          el('dt', { text: 'Next' }), el('dd', { text: r.nextLabel || '—' }),
          el('dt', { text: 'Screens' }), el('dd', { text: String(r.screens) }),
        ),
        el('div', { class: 'cluster is-gap-2xs' },
          el('a', { class: 'btn btn-sm btn-primary', href: `/c/${r.code}` }, 'Open room'),
          el('a', { class: 'btn btn-sm', href: `/screens/${r.code}` }, 'Screens'),
          el('button', { class: 'btn btn-sm', type: 'button', onclick: () => verb('go', r.code), title: 'Start the next session' }, 'GO'),
          r.running ? el('button', { class: 'btn btn-sm', type: 'button', onclick: () => verb(r.paused ? 'resume' : 'pause', r.code) }, r.paused ? 'Resume' : 'Pause') : null,
          el('button', { class: `btn btn-sm ${r.blanked ? '' : 'btn-danger'}`, type: 'button', onclick: () => verb(r.blanked ? 'unblank' : 'blank', r.code) }, r.blanked ? 'Restore' : 'Blackout'),
        ),
      );
      grid.appendChild(card);
    }
  }

  function tick() {
    const elapsed = Date.now() - fetchedAt;
    for (const card of grid.querySelectorAll('.tp-live-card')) {
      const r = rooms.find((x) => x.code === card.dataset.room);
      if (!r || !r.running || r.paused) continue;
      card.querySelector('[data-remaining]').textContent = fmtRemaining(r.remainingMS - elapsed);
    }
  }

  async function poll() {
    try {
      const out = await api('GET', `/api/events/${EV}/live`);
      rooms = out.rooms || [];
      fetchedAt = Date.now();
      render();
      lamp?.classList.add('is-on');
      if (status) status.textContent = `Live · ${rooms.length} room${rooms.length === 1 ? '' : 's'}`;
    } catch (ex) {
      lamp?.classList.remove('is-on');
      if (status) status.textContent = ex.message;
    }
  }
  poll();
  setInterval(poll, 2000);
  setInterval(tick, 250);

  for (const b of document.querySelectorAll('[data-verb-all]')) {
    b.addEventListener('click', async () => {
      const v = b.dataset.verbAll;
      if (v === 'blank' && !(await tpConfirm('Every screen in every room goes dark.', { title: 'Blackout all rooms?', ok: 'Blackout', danger: true }))) return;
      verb(v, '');
    });
  }

  // Rooms table
  const reload = () => location.reload();
  for (const row of document.querySelectorAll('#rooms-body tr[data-room]')) {
    const room = row.dataset.room;
    const nameEl = row.querySelector('[data-rename]');
    inlineEdit(nameEl, (name) => api('PATCH', `/api/events/${EV}/rooms/${room}`, { name }));
    row.querySelector('[data-room-pw]')?.addEventListener('click', async () => {
      const res = await tpPrompt(`Moderators of "${nameEl.textContent}" will need this password.`, '', {
        title: 'Room password', ok: 'Set password',
        fields: [{ id: 'pw', label: 'Password', type: 'password', autocomplete: 'new-password' }],
      });
      const pw = res?.pw;
      if (pw) run(() => api('PATCH', `/api/events/${EV}/rooms/${room}`, { password: pw }), 'Room password set').then(reload);
    });
    row.querySelector('[data-room-pw-clear]')?.addEventListener('click', () =>
      run(() => api('PATCH', `/api/events/${EV}/rooms/${room}`, { clearPassword: true }), 'Room password removed').then(reload));
    for (const b of row.querySelectorAll('[data-room-move]')) {
      b.addEventListener('click', () => {
        const pos = Number(row.firstElementChild.textContent) + Number(b.dataset.roomMove);
        run(() => api('PATCH', `/api/events/${EV}/rooms/${room}`, { pos })).then(reload);
      });
    }
    row.querySelector('[data-room-delete]')?.addEventListener('click', async () => {
      const ok = await tpConfirm(`"${nameEl.textContent}" and its sessions, polls and screen layouts are deleted for good.`, { title: 'Delete room?', ok: 'Delete', danger: true });
      if (ok) run(() => api('DELETE', `/api/events/${EV}/rooms/${room}`), 'Room deleted').then(reload);
    });
  }
  document.getElementById('room-add')?.addEventListener('submit', (e) => {
    e.preventDefault();
    const name = e.target.name.value.trim();
    if (name) run(() => api('POST', `/api/events/${EV}/rooms`, { name }), 'Room added').then(reload);
  });
  document.getElementById('room-pw-all')?.addEventListener('click', async () => {
    const res = await tpPrompt('Every room gets this moderator password. Leave it empty to remove every room password.', '', {
      title: 'Same password for every room', ok: 'Apply to all rooms',
      fields: [{ id: 'pw', label: 'Password', type: 'password', autocomplete: 'new-password' }],
    });
    if (!res) return;
    const pw = res.pw;
    run(() => api('POST', `/api/events/${EV}/room-password`, { password: pw }), pw ? 'Password set on every room' : 'Room passwords removed').then(reload);
  });
  document.getElementById('room-import')?.addEventListener('change', (e) => {
    const f = e.target.files?.[0];
    if (!f) return;
    const fd = new FormData();
    fd.append('file', f);
    run(() => api('POST', `/api/events/${EV}/rooms/import`, fd), 'Room imported').then(reload);
  });

  // Settings
  document.getElementById('ev-rename')?.addEventListener('submit', (e) => {
    e.preventDefault();
    run(() => api('PATCH', `/api/events/${EV}`, { name: document.getElementById('ev-name-input').value }), 'Event renamed').then(reload);
  });
  document.getElementById('ev-theme')?.addEventListener('submit', (e) => {
    e.preventDefault();
    run(() => api('PATCH', `/api/events/${EV}`, { theme: document.getElementById('ev-theme-select').value }), 'Default screen theme saved');
  });
  document.getElementById('ev-pw')?.addEventListener('submit', (e) => {
    e.preventDefault();
    const input = document.getElementById('ev-pw-new');
    run(() => api('PATCH', `/api/events/${EV}`, { password: input.value }), 'Supervisor password changed').then(() => { input.value = ''; });
  });
  document.getElementById('ev-map')?.addEventListener('change', (e) => {
    const f = e.target.files?.[0];
    if (!f) return;
    const fd = new FormData();
    fd.append('file', f);
    run(async () => {
      const up = await api('POST', '/api/assets', fd);
      await api('POST', `/api/events/${EV}/map`, { assetId: up.id });
    }, 'Venue map uploaded').then(reload);
  });
  document.getElementById('ev-map-clear')?.addEventListener('click', () =>
    run(() => api('POST', `/api/events/${EV}/map`, { assetId: 0 }), 'Map removed').then(reload));
  document.getElementById('ev-delete')?.addEventListener('click', async () => {
    const res = await tpPrompt(`This deletes every room, session, poll and screen layout of "${body.dataset.eventName}". Type the event name to confirm.`, '', {
      title: 'Delete event?', ok: 'Delete event', fields: [{ id: 'name', label: 'Event name' }],
    });
    if (!res) return;
    if (res.name !== body.dataset.eventName) return toast('Name did not match — nothing deleted.', 'danger');
    try {
      await api('DELETE', `/api/events/${EV}`);
      forgetEvent(EV);
      location.href = '/';
    } catch (ex) { toast(ex.message, 'danger'); }
  });
}
