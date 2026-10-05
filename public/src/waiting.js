/**
 * waiting.js — the orphaned-display waiting room (shared by every /d/
 * surface: stage, variants, board).
 *
 * When a display's show vanishes (deleted / unknown code) the mesh's join
 * is refused → status 'badshow'. applyWaiting() raises the full-screen
 * overlay shipped in the display templates (logo + "waiting for
 * connection…" — visibility purely body[data-waiting], like the blackout
 * pattern), registers the display with POST /api/waiting/register
 * (screen name + host), and polls /api/waiting/mine every 4 s. Any operator
 * Screens gallery can then CAPTURE the waiting display into a live show;
 * the claim carries the new code and this display navigates to
 * /d/<code>?screen=<name> — keeping its identity so theme/board
 * assignments follow the screen.
 *
 * The register/mine paths are AuthGate-exempt (a stage TV never logs in);
 * the waiting registry holds only screen names, not content.
 */
import { screenName } from './mesh.js';

let active = false;
let pollTimer = 0;

export function applyWaiting(status) {
  // Overlay presence is the page gate: only /d/ surfaces ship it.
  if (!document.getElementById('tp-waiting')) return;
  const on = status === 'badshow';
  if (on === active) return;
  active = on;
  const body = document.body;
  if (on) {
    body.setAttribute('data-waiting', '1');
    const nameEl = document.getElementById('tp-waiting-name');
    if (nameEl) nameEl.textContent = ` — ${screenName()}`;
    startPolling();
  } else {
    body.removeAttribute('data-waiting');
    clearInterval(pollTimer);
    pollTimer = 0;
  }
}

// runWaiting is the no-code /d/ entry (PLAN §11.3 phase 1): the READY
// page has no mesh and no join — body[data-waiting] is set server-side,
// so this only starts the same register/poll loop the orphaned-display
// flow runs after badshow.
export function runWaiting() {
  if (active) return;
  active = true;
  startPolling();
}

function startPolling() {
  register();
  clearInterval(pollTimer);
  pollTimer = setInterval(pollMine, 4000);
}

async function register() {
  try {
    await fetch('/api/waiting/register', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ name: screenName(), host: location.host }),
    });
  } catch { /* nothing to register against while fully offline */ }
}

async function pollMine() {
  try {
    const q = `?name=${encodeURIComponent(screenName())}&host=${encodeURIComponent(location.host)}`;
    const j = await (await fetch('/api/waiting/mine' + q)).json();
    if (j && j.assigned) {
      clearInterval(pollTimer);
      pollTimer = 0;
      location.href = `/d/${encodeURIComponent(j.assigned)}?screen=${encodeURIComponent(screenName())}`;
    }
  } catch { /* keep waiting */ }
}
