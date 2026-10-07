/**
 * event.js — client for the event layer pages:
 *   home         join an event by code · create an event · recent events
 *   event        lobby: moderator room sign-in · SuperOperator sign-in
 *   event-admin  SuperOperator dashboard: live rooms, room admin, settings
 *   box          box password: first-time setup, sign-in, change, sign out
 */
import {
  api, toast, showError, normalizeCode, el, setText,
  recentEvents, rememberEvent, forgetEvent, inlineEdit,
} from './ui.js';
import { fmtCode, fmtRemaining } from './engine.js';

// Lobby clocks show whole seconds (engine adds tenths below 10 s).
const fmtClock = (ms) => fmtRemaining(ms).replace(/\.\d$/, '');
import { tpConfirm, tpPrompt } from './dialog.js';

const body = document.body;
const page = body.dataset.page;
const EV = body.dataset.event || '';

if (page === 'home') initHome();
if (page === 'event') initLobby();
if (page === 'event-admin') initAdmin();
if (page === 'box') initBox();

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
    if (code.length !== 8) return showError(err, 'Event codes have 8 characters, like K7QP-M3XB.');
    try {
      await api('GET', `/api/events/${code}`);
      location.href = `/e/${code}`;
    } catch (ex) {
      showError(err, ex.message === 'Unknown event code' ? 'No event with that code on this box. Check the code with your Event Technician.' : ex.message);
    }
  });

  // Room rows: a name input with an X to remove it (STATUS U22).
  const rooms = document.getElementById('ev-rooms');
  const addRoom = (value, focus) => {
    const input = el('input', { class: 'input input-sm', name: 'room', maxlength: 60, 'aria-label': 'Room name', value });
    const row = el('div', { class: 'row is-gap-2xs tp-room-row' }, input,
      el('button', {
        class: 'btn btn-sm btn-icon btn-ghost', type: 'button', title: 'Remove this room', 'aria-label': 'Remove room',
        onclick: () => { row.remove(); },
      }, '✕'));
    rooms?.appendChild(row);
    if (focus) { input.focus(); input.select(); }
  };
  addRoom('Room A');
  addRoom('Room B');
  document.getElementById('ev-add-room')?.addEventListener('click', () => {
    const n = rooms.querySelectorAll('input').length;
    addRoom(`Room ${String.fromCharCode(65 + Math.min(n, 25))}`, true);
  });
  document.getElementById('create-form')?.addEventListener('submit', async (e) => {
    e.preventDefault();
    // One event per press: Enter twice used to create two (BUGLOG RS24).
    const btn = e.submitter || e.target.querySelector('[type="submit"]');
    if (btn?.disabled) return;
    const err = document.getElementById('create-error');
    const name = document.getElementById('ev-name').value.trim();
    const pw = document.getElementById('ev-pw').value;
    const pw2 = document.getElementById('ev-pw2').value;
    if (pw !== pw2) return showError(err, 'The two passwords do not match.');
    const roomNames = [...rooms.querySelectorAll('input')].map((i) => i.value.trim()).filter(Boolean);
    if (btn) btn.disabled = true;
    try {
      const out = await api('POST', '/api/events', { name, password: pw, rooms: roomNames });
      rememberEvent(out.code, name);
      location.href = out.admin; // stays disabled while the page changes
    } catch (ex) {
      if (btn) btn.disabled = false;
      showError(err, ex.message);
    }
  });

  // Recent events (this browser only), shown above everything with a
  // Resume button; the whole panel stays hidden when there are none
  // (STATUS U20).
  const list = document.getElementById('recent-list');
  const panel = document.getElementById('recent-panel');
  const recent = recentEvents();
  if (list && recent.length) {
    panel.hidden = false;
    for (const ev of recent) {
      list.appendChild(el('li', { class: 'list-item' },
        el('div', { class: 'stack is-gap-none' }, el('span', { class: 'list-item-title', text: ev.name }), el('span', { class: 'list-item-meta mono', text: fmtCode(ev.code) })),
        el('a', { class: 'btn btn-sm btn-primary push', href: `/e/${ev.code}` }, 'Resume'),
        el('button', {
          class: 'btn btn-sm btn-ghost', type: 'button', title: 'Forget on this device', 'aria-label': `Forget ${ev.name}`,
          onclick: (e) => {
            forgetEvent(ev.code);
            e.currentTarget.closest('li').remove();
            if (!list.children.length) panel.hidden = true;
          },
        }, '✕'),
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
      const out = await api('POST', `/api/events/${EV}/login`, { pw: document.getElementById('super-pw')?.value || '', now: Date.now() });
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
  const status = document.getElementById('live-status');
  let rooms = [];
  let fetchedAt = 0;

  // run reports success so callers reload only when the change landed
  // (BUGLOG RW43: a failed action used to reload anyway, wiping its error).
  const run = async (fn, okMsg) => {
    try {
      await fn();
      if (okMsg) toast(okMsg, 'success');
      poll();
      return true;
    } catch (ex) {
      toast(ex.message, 'danger');
      return false;
    }
  };
  // verb disables its button until the request settles, so a double-click
  // on GO can't advance two sessions (BUGLOG RW44).
  const verb = async (v, room, btn) => {
    if (btn?.disabled) return;
    if (btn) btn.disabled = true;
    try {
      await run(() => api('POST', `/api/events/${EV}/verb`, { verb: v, room }));
    } finally {
      if (btn) btn.disabled = false;
    }
  };

  function stateOf(r) {
    if (r.blanked) return ['Blacked out', 'status-error'];
    if (r.running && r.paused) return ['Paused', 'status-warn'];
    if (r.running && r.overtime) return ['Overtime', 'status-error'];
    if (r.running) return ['Running', 'status-ok'];
    return ['Idle', 'status-idle'];
  }

  // Cards are built once per room and patched in place on every poll
  // (BUGLOG RW44: rebuilding them every 2 s lost keyboard focus and
  // dropped clicks that straddled a re-render).
  const cards = new Map(); // room code → { card, parts }
  const empty = el('div', { class: 'empty-state' },
    el('span', { class: 'empty-state-title', text: 'No rooms yet.' }), el('span', { class: 'empty-state-hint', text: 'Add one below.' }));

  function buildCard(code) {
    const room = () => rooms.find((x) => x.code === code) || {};
    const parts = {
      name: el('span'),
      status: el('span', { class: 'status push' }),
      clock: el('div', { class: 'readout readout-lg', dataset: { remaining: '1' } }),
      now: el('dd'),
      next: el('dd'),
      screens: el('dd'),
    };
    parts.go = el('button', { class: 'btn btn-sm', type: 'button', title: 'Start the next session', dataset: { act: 'go' } }, 'GO');
    parts.go.addEventListener('click', () => verb('go', code, parts.go));
    parts.pause = el('button', { class: 'btn btn-sm', type: 'button', dataset: { act: 'pause' } });
    parts.pause.addEventListener('click', () => verb(room().paused ? 'resume' : 'pause', code, parts.pause));
    parts.blank = el('button', { class: 'btn btn-sm', type: 'button', dataset: { act: 'blank' } });
    parts.blank.addEventListener('click', () => verb(room().blanked ? 'unblank' : 'blank', code, parts.blank));
    const card = el('article', { class: 'panel stack is-gap-xs', dataset: { room: code } },
      el('div', { class: 'panel-header cluster' }, parts.name, parts.status),
      parts.clock,
      el('dl', { class: 'props' },
        el('dt', { text: 'Now' }), parts.now,
        el('dt', { text: 'Next' }), parts.next,
        el('dt', { text: 'Screens' }), parts.screens,
      ),
      el('div', { class: 'cluster is-gap-2xs' },
        el('a', { class: 'btn btn-sm btn-primary', href: `/c/${code}` }, 'Open room'),
        el('a', { class: 'btn btn-sm', href: `/screens/${code}` }, 'Screens'),
        parts.go, parts.pause, parts.blank,
      ),
    );
    return { card, parts };
  }

  function render() {
    if (!rooms.length) {
      for (const { card } of cards.values()) card.remove();
      cards.clear();
      if (empty.parentNode !== grid) grid.replaceChildren(empty);
      return;
    }
    empty.remove();
    grid.querySelector('.empty-state.is-loading')?.remove();
    const live = new Set(rooms.map((r) => r.code));
    for (const [code, { card }] of cards) {
      if (!live.has(code)) { card.remove(); cards.delete(code); }
    }
    rooms.forEach((r, i) => {
      let entry = cards.get(r.code);
      if (!entry) { entry = buildCard(r.code); cards.set(r.code, entry); }
      const { card, parts } = entry;
      const [label, cls] = stateOf(r);
      setText(parts.name, r.name);
      setText(parts.status, label);
      parts.status.className = `status ${cls} push`;
      setText(parts.clock, r.running ? fmtClock(r.remainingMS) : '—');
      setText(parts.now, r.activeLabel || '—');
      setText(parts.next, r.nextLabel || '—');
      setText(parts.screens, String(r.screens));
      parts.pause.hidden = !r.running;
      setText(parts.pause, r.paused ? 'Resume' : 'Pause');
      setText(parts.blank, r.blanked ? 'Restore' : 'Blackout');
      parts.blank.classList.toggle('btn-danger', !r.blanked);
      // Move a card only when it is out of place: moving a node drops focus.
      if (grid.children[i] !== card) grid.insertBefore(card, grid.children[i] || null);
    });
  }

  function tick() {
    const elapsed = Date.now() - fetchedAt;
    for (const r of rooms) {
      if (!r.running || r.paused) continue;
      const entry = cards.get(r.code);
      if (entry) setText(entry.parts.clock, fmtClock(r.remainingMS - elapsed));
    }
  }

  // One poll at a time: a slow reply can't overwrite a newer one. A poll
  // asked for while one is in flight runs once that one lands.
  let polling = false;
  let pollAgain = false;
  async function poll() {
    if (polling) { pollAgain = true; return; }
    polling = true;
    try {
      const t0 = performance.now();
      const out = await api('GET', `/api/events/${EV}/live`);
      const ping = document.getElementById('conn-ping');
      if (ping) ping.textContent = `${Math.round(performance.now() - t0)} ms`;
      rooms = out.rooms || [];
      fetchedAt = Date.now();
      render();
      if (status) status.dataset.state = 'live';
      if (status) status.textContent = `Live · ${rooms.length} room${rooms.length === 1 ? '' : 's'}`;
    } catch (ex) {
      if (status) status.dataset.state = 'offline';
      if (status) status.textContent = ex.message;
    } finally {
      polling = false;
      if (pollAgain) { pollAgain = false; poll(); }
    }
  }
  poll();
  setInterval(poll, 2000);
  setInterval(tick, 250);

  for (const b of document.querySelectorAll('[data-verb-all]')) {
    b.addEventListener('click', async () => {
      const v = b.dataset.verbAll;
      if (v === 'blank' && !(await tpConfirm('Every screen in every room goes dark.', { title: 'Blackout all rooms?', ok: 'Blackout', danger: true }))) return;
      verb(v, '', b);
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
      if (pw) run(() => api('PATCH', `/api/events/${EV}/rooms/${room}`, { password: pw }), 'Room password set').then((ok) => ok && reload());
    });
    row.querySelector('[data-room-pw-clear]')?.addEventListener('click', () =>
      run(() => api('PATCH', `/api/events/${EV}/rooms/${room}`, { clearPassword: true }), 'Room password removed').then((ok) => ok && reload()));
    for (const b of row.querySelectorAll('[data-room-move]')) {
      b.addEventListener('click', () => {
        const pos = Number(row.firstElementChild.textContent) + Number(b.dataset.roomMove);
        run(() => api('PATCH', `/api/events/${EV}/rooms/${room}`, { pos })).then((ok) => ok && reload());
      });
    }
    // Duplicate (moved here from the room's Setup tab, STATUS U29): same
    // sessions, notes and day start — a new room, stopped, no password.
    row.querySelector('[data-room-dup]')?.addEventListener('click', async () => {
      const res = await tpPrompt('The new room gets the same sessions, notes and day start. It starts stopped, without a password.', `${nameEl.textContent} (copy)`, {
        title: 'Duplicate room', ok: 'Duplicate',
      });
      if (!res) return;
      run(() => api('POST', `/api/shows/${room}/clone`, { title: res.value || '' }), 'Room duplicated').then((ok) => ok && reload());
    });
    row.querySelector('[data-room-delete]')?.addEventListener('click', async () => {
      const ok = await tpConfirm(`"${nameEl.textContent}" and its sessions, polls and screen layouts are deleted for good.`, { title: 'Delete room?', ok: 'Delete', danger: true });
      if (ok) run(() => api('DELETE', `/api/events/${EV}/rooms/${room}`), 'Room deleted').then((ok) => ok && reload());
    });
  }
  document.getElementById('room-add')?.addEventListener('submit', (e) => {
    e.preventDefault();
    const name = e.target.name.value.trim();
    if (name) run(() => api('POST', `/api/events/${EV}/rooms`, { name }), 'Room added').then((ok) => ok && reload());
  });
  document.getElementById('room-pw-all')?.addEventListener('click', async () => {
    const res = await tpPrompt('Every room gets this moderator password. Leave it empty to remove every room password.', '', {
      title: 'Same password for every room', ok: 'Apply to all rooms',
      fields: [{ id: 'pw', label: 'Password', type: 'password', autocomplete: 'new-password' }],
    });
    if (!res) return;
    const pw = res.pw;
    run(() => api('POST', `/api/events/${EV}/room-password`, { password: pw }), pw ? 'Password set on every room' : 'Room passwords removed').then((ok) => ok && reload());
  });
  document.getElementById('room-import')?.addEventListener('change', (e) => {
    const f = e.target.files?.[0];
    if (!f) return;
    const fd = new FormData();
    fd.append('file', f);
    run(() => api('POST', `/api/events/${EV}/rooms/import`, fd), 'Room imported').then((ok) => ok && reload());
  });

  // Settings
  document.getElementById('ev-rename')?.addEventListener('submit', (e) => {
    e.preventDefault();
    run(() => api('PATCH', `/api/events/${EV}`, { name: document.getElementById('ev-name-input').value }), 'Event renamed').then((ok) => ok && reload());
  });
  document.getElementById('ev-theme')?.addEventListener('submit', (e) => {
    e.preventDefault();
    run(() => api('PATCH', `/api/events/${EV}`, { theme: document.getElementById('ev-theme-select').value }), 'Default screen theme saved');
  });
  // Pair a box (N13): the code on the box's screen + where it goes.
  const pairForm = document.getElementById('pair-form');
  if (pairForm) {
    const kindSel = document.getElementById('pair-kind');
    // ftl .otp: one input drawn as six boxes; keep it to the six digits so a
    // pasted "123 456" fits.
    const codeIn = document.getElementById('pair-code');
    codeIn.addEventListener('input', () => { codeIn.value = codeIn.value.replace(/\D/g, '').slice(0, 6); });
    const tplSel = document.getElementById('pair-template');
    let catalog = [];
    const fillTemplates = () => {
      tplSel.replaceChildren();
      for (const t of catalog.filter((x) => x.kind === kindSel.value)) {
        const o = document.createElement('option');
        o.value = t.key;
        o.textContent = t.name;
        tplSel.appendChild(o);
      }
    };
    api('GET', '/api/board-templates').then((j) => { catalog = j.catalog || []; fillTemplates(); }).catch(() => {});
    kindSel.addEventListener('change', fillTemplates);
    pairForm.addEventListener('submit', (e) => {
      e.preventDefault();
      const body = {
        pairCode: document.getElementById('pair-code').value.replace(/\s+/g, ''),
        code: document.getElementById('pair-room').value,
        kind: kindSel.value,
        template: tplSel.value,
        rotation: Number(document.getElementById('pair-rotation').value),
        name: document.getElementById('pair-name').value.trim(),
        theme: '',
      };
      run(() => api('POST', `/api/events/${EV}/pair`, body), 'Box paired — it switches to its screen in a few seconds')
        .then((ok) => { if (ok) document.getElementById('pair-code').value = ''; });
    });
  }

  // Event end (N12): shown and entered in this browser's local time, which
  // is the venue's when the Event Technician is there; stored as epoch ms.
  const endForm = document.getElementById('ev-end');
  if (endForm) {
    const endInput = document.getElementById('ev-end-input');
    const ms = Number(endForm.dataset.endsAt) || 0;
    if (ms) {
      const d = new Date(ms);
      const p = (n) => String(n).padStart(2, '0');
      endInput.value = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
    }
    endForm.addEventListener('submit', (e) => {
      e.preventDefault();
      const v = endInput.value ? new Date(endInput.value).getTime() : 0;
      run(() => api('PATCH', `/api/events/${EV}`, { endsAt: v }), v ? 'Event end saved' : 'Event end cleared');
    });
  }
  document.getElementById('ev-pw')?.addEventListener('submit', (e) => {
    e.preventDefault();
    const input = document.getElementById('ev-pw-new');
    run(() => api('PATCH', `/api/events/${EV}`, { password: input.value }), 'Event Technician Password changed').then((ok) => { if (ok) input.value = ''; });
  });
  document.getElementById('ev-map')?.addEventListener('change', (e) => {
    const f = e.target.files?.[0];
    if (!f) return;
    const fd = new FormData();
    fd.append('file', f);
    run(async () => {
      const up = await api('POST', `/api/assets?event=${EV}`, fd);
      await api('POST', `/api/events/${EV}/map`, { assetId: up.id });
    }, 'Venue map uploaded').then((ok) => ok && reload());
  });
  document.getElementById('ev-map-clear')?.addEventListener('click', () =>
    run(() => api('POST', `/api/events/${EV}/map`, { assetId: 0 }), 'Map removed').then((ok) => ok && reload()));
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

/* ------------------------------------------------------------------- box -- */
function initBox() {
  const next = body.dataset.next || '/settings';
  const val = (id) => document.getElementById(id)?.value || '';
  const wire = (id, send) => {
    const form = document.getElementById(id);
    if (!form) return;
    const btn = form.querySelector('[type=submit]');
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const err = form.querySelector('.field-error');
      if (document.getElementById('box-new2') && val('box-new') !== val('box-new2')) {
        showError(err, 'The two passwords don\'t match.');
        return;
      }
      btn.disabled = true;
      try {
        await send();
        location.href = next;
      } catch (ex) {
        showError(err, ex.message);
        btn.disabled = false;
      }
    });
  };
  wire('box-setup', () => api('POST', '/api/box/setup', { password: val('box-new') }));
  wire('box-login', () => api('POST', '/api/box/login', { password: val('box-pw') }));
  wire('box-change', () => api('POST', '/api/box/password', { current: val('box-cur'), password: val('box-new') }));
  document.getElementById('box-logout')?.addEventListener('click', async () => {
    try { await api('POST', '/api/box/logout'); } catch { /* signed out either way */ }
    location.href = '/';
  });
}
