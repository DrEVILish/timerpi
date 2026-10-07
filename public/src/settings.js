/**
 * settings.js — the box settings page (GET /settings): software update,
 * mesh radio, role override, rename, operator password, default theme and
 * the CuTePi OSC bridge. Results go to ftl toasts (textContent only).
 */
import { api, toast } from './ui.js';

const $ = (id) => document.getElementById(id);
const fail = (ex) => toast(ex.message, 'danger');

// UPDATE (VENUE-CLOUD §14): offered only when a newer signed build is
// found; installing restarts the box, so wait for it to come back.
const updBtn = $('upd-btn');
const updSay = (t) => { $('upd-status').textContent = t; };
api('GET', '/api/network/update').then((j) => {
  if (j.updating) return updSay('An update is being installed…');
  if (!j.available) return updSay('Up to date.');
  updSay(`Version ${j.available} is available.`);
  updBtn.textContent = `UPDATE to ${j.available}`;
  updBtn.hidden = false;
}).catch((ex) => updSay(ex.status ? ex.message : 'Could not check for updates'));
updBtn.addEventListener('click', async () => {
  updBtn.disabled = true;
  updSay('Downloading and installing…');
  let out;
  try {
    out = await api('POST', '/api/network/update');
  } catch (ex) {
    updSay('Not updated.');
    toast(ex.status ? ex.message : 'Network error — not updated', 'danger');
    updBtn.disabled = false;
    return;
  }
  updSay(`Installed ${out.installed}. The box is restarting…`);
  let tries = 0;
  const wait = setInterval(() => {
    if (++tries > 60) { clearInterval(wait); updSay('The box hasn\'t come back yet; reload in a moment.'); return; }
    fetch('/health', { cache: 'no-store' }).then((r) => r.json()).then((h) => {
      if (h.version === out.installed) { clearInterval(wait); location.reload(); }
    }).catch(() => {});
  }, 2000);
});

$('mesh-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    const out = await api('POST', '/api/network/mesh', {
      country: $('mesh-country').value.trim(),
      ch24: Number($('mesh-ch24').value),
      ch5: Number($('mesh-ch5').value),
    });
    toast(`Radio saved: ${out.country}, 2.4 GHz ch ${out.ch24}, 5 GHz ch ${out.ch5}. Applies at the next reboot.`, 'success');
  } catch (ex) { fail(ex); }
});

// Errors come back as {error} with a 4xx/5xx status (BUGLOG RW51: a 400
// used to say "saved"); api() throws on those.
const roleSel = $('net-role');
roleSel.addEventListener('change', async () => {
  try {
    await api('POST', '/api/network/role', { force: roleSel.value });
    toast(`Role override saved: ${roleSel.value}`, 'success');
    setTimeout(() => location.reload(), 900);
  } catch (ex) { fail(ex); }
});

$('net-rename').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    const out = await api('POST', '/api/network/hostname', { name: $('net-rename-name').value });
    toast(`Hostname applied — announcing as ${out.hostname}`, 'success');
    setTimeout(() => location.reload(), 900);
  } catch (ex) { fail(ex); }
});

// Appliance default theme (B7): fetched list, DB-persisted selection.
const themeSel = $('tp-def-theme');
api('GET', '/api/theme').then((j) => {
  for (const t of (j.themes || []).slice().sort()) {
    themeSel.appendChild(new Option(t === j.fallback ? `${t} (bundled default)` : t, t));
  }
  if (j.current) themeSel.value = j.current;
}).catch(() => {});
$('tp-def-theme-save').addEventListener('click', async () => {
  try {
    const j = await api('POST', '/api/theme', { theme: themeSel.value });
    toast(`Saved — fresh browsers open in “${j.theme || 'blue-future (product default)'}”`, 'success');
  } catch (ex) { fail(ex); }
});

// Operator password (A1): saves, keeps the fresh auth token in
// localStorage so THIS device keeps its controls join, then reloads the
// (possibly now-gated) page.
$('auth-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  try {
    const out = await api('POST', '/api/auth/password', { pw: $('auth-pw').value });
    try {
      if (out.token) localStorage.setItem('tp.atoken', out.token);
      else localStorage.removeItem('tp.atoken');
    } catch { /* private mode */ }
    setTimeout(() => location.reload(), 400);
  } catch (ex) { fail(ex); }
});

// PLAN §11.6: OSC bridge settings (global kv via /api/osc).
api('GET', '/api/osc').then((j) => {
  if (!j.config) return;
  $('osc-in-enabled').checked = !!j.config.in.enabled;
  $('osc-in-port').value = j.config.in.port || '';
  $('osc-in-allow').value = j.config.in.allow || '';
  $('osc-out-enabled').checked = !!j.config.out.enabled;
  $('osc-out-host').value = j.config.out.host || '';
  $('osc-out-port').value = j.config.out.port || '';
}).catch(() => {});
$('osc-save').addEventListener('click', async () => {
  try {
    await api('POST', '/api/osc', {
      in: { enabled: $('osc-in-enabled').checked, port: $('osc-in-port').value.trim(), allow: $('osc-in-allow').value.trim() },
      out: { enabled: $('osc-out-enabled').checked, host: $('osc-out-host').value.trim(), port: $('osc-out-port').value.trim() },
    });
    toast('Bridge saved — listener re-synced.', 'success');
  } catch (ex) { fail(ex); }
});
$('osc-test').addEventListener('click', async () => {
  try {
    const j = await api('POST', '/api/osc/test');
    toast(`Packet sent to ${j.sent} (UDP: no ack from CuTePi).`, 'success');
  } catch (ex) { fail(ex); }
});
