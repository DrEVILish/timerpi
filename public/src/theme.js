/**
 * theme.js — the runtime theme-paint primitives shared by every client page
 * (dashboard operator, stage TV, display board).
 *
 * Split out of timerpi.js when the board page gained its OWN mesh client
 * (board.js): the `display` WS frame (operator pushes a theme at a screen)
 * must repaint there too, but board.js can't import timerpi.js — the file
 * its page already boots as a DIFFERENT module URL (the rev'd copy) would
 * execute all its boot logic a second time. This module holds just the leaf
 * paint functions; every page imports the same URL so the ES-module cache
 * keeps them singletons.
 *
 * Policy (locked by REVIEW-2 R6 + B7): a theme is the data-theme attribute
 * AND its compiled dist bundle — attribute alone leaves the old bundle's
 * tokens in charge. Icons follow the ACTIVE theme: templates ship sprite
 * NAMES; every runtime swap retargets <use> hrefs here.
 */

const THEME_KEY = 'timerpi.theme';
let themeVersion = ''; // themes.json version → cache-bust the bundles (CONTRACT.md)

export function setThemeVersion(v) { themeVersion = String(v || ''); }

/** Fetch themes.json and adopt its version for cache-busting; resolves the
 * theme list, or null when offline/absent (current version stands). */
export async function loadThemeVersion() {
  try {
    const themes = await (await fetch('/ftl/dist/themes.json')).json();
    if (Array.isArray(themes) && themes[0]?.version) setThemeVersion(themes[0].version);
    return themes;
  } catch { return null; }
}

/** dist path for a theme bundle; `?v=` busts stale caches after a bump. */
export function themeCssUrl(slug) {
  const v = themeVersion ? `?v=${encodeURIComponent(themeVersion)}` : '';
  return `/ftl/dist/${encodeURIComponent(slug)}.css${v}`;
}

/** No-network-lint: currentColor glyphs only; slugs are charset-sanitised. */
function safeSlug(slug) {
  // Lowercase first: stripping first turned "Blue-Future" into "lue-uture"
  // (BUGLOG RS20).
  return String(slug || 'blue-future').toLowerCase().replace(/[^a-z0-9_-]/g, '') || 'blue-future';
}

/** Apply a theme: attribute + bundle link, no persistence (callers decide). */
export function applyTheme(slug) {
  const s = safeSlug(slug);
  document.documentElement.setAttribute('data-theme', s);
  let link = document.getElementById('theme-css-live');
  if (!link) {
    link = document.createElement('link');
    link.id = 'theme-css-live';
    link.rel = 'stylesheet';
    document.head.appendChild(link);
  }
  link.href = themeCssUrl(s);
  // Keep exactly one dist bundle linked: drop the static default link.
  document.getElementById('theme-css-default')?.remove();
  applyIconTheme(s);
}

/** Point every <use> sprite reference at the chosen theme's icon bundle. */
export function applyIconTheme(slug) {
  let s = safeSlug(slug);
  // core and tokens are base layers with no icon sprite (404): use the generic set.
  if (s === 'core' || s === 'tokens') s = 'generic';
  for (const use of document.querySelectorAll('use')) {
    const href = use.getAttribute('href') || '';
    const m = href.match(/^(.*)\/ftl\/(?:dist\/)?icons\/[^/#]+\.svg(#.*)?$/);
    if (m) use.setAttribute('href', `/ftl/dist/icons/${encodeURIComponent(s)}.svg${m[2] || ''}`);
  }
}

/* ------------------------- client error reports -------------------------
 * F4: window.onerror + unhandledrejection batched back to the server for
 * debugging (POST /api/shows/:code/client-log). Every page boots this
 * through the shared leaf module so dashboard, stage and board all
 * report with zero per-page wiring. Identical signatures are throttled
 * (one per minute) so a throwing rAF loop can't flood the journal.
 */
const errQueue = [];
const errSeen = new Map(); // signature → last-report epoch ms
let errTimer = 0;
let errCode = '';

function errFlush() {
  if (!errQueue.length || !errCode) return;
  const batch = errQueue.splice(0, errQueue.length);
  fetch(`/api/shows/${encodeURIComponent(errCode)}/client-log`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ entries: batch.slice(0, 25) }),
    keepalive: true, // pagehide flush: a plain fetch dies with the document
  }).catch(() => { /* debugging must never break the show */ });
}

function errReport(kind, message, source) {
  const msg = String(message || '').slice(0, 500) || '(empty error)';
  const src = String(source || '').slice(0, 160);
  const sig = `${kind}|${msg}|${src}`;
  const now = Date.now();
  if ((errSeen.get(sig) || 0) + 60000 > now) return; // throttle repeats
  errSeen.set(sig, now);
  if (errSeen.size > 200) errSeen.clear();
  errQueue.push({ kind, message: msg, source: src });
  if (errQueue.length > 25) errQueue.shift();
  if (!errTimer) errTimer = setTimeout(() => { errTimer = 0; errFlush(); }, 5000);
}

/** Boot client error reporting for a show code (call once per page). */
export function initClientLog(code) {
  errCode = String(code || document.body?.dataset.show || '');
  if (!errCode || initClientLog.bound) return;
  initClientLog.bound = true;
  window.addEventListener('error', (e) => {
    const src = e.filename ? `${e.filename}:${e.lineno || 0}:${e.colno || 0}` : '';
    errReport('error', e.message || (e.error && e.error.stack) || 'unknown error', src);
  });
  window.addEventListener('unhandledrejection', (e) => {
    const r = e.reason;
    errReport('unhandledrejection', (r && (r.stack || r.message)) || String(r), '');
  });
  window.addEventListener('pagehide', () => { errFlush(); });
}

export { THEME_KEY };
