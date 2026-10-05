/**
 * ui.js — small shared helpers for the operator pages (event layer and
 * newer surfaces): JSON API calls, toasts, code formatting, time formats.
 * All DOM text goes through textContent (XSS rule).
 */

/** api(method, path, body?) → parsed JSON; throws Error(message) on !ok. */
export async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body instanceof FormData) {
    opts.body = body;
  } else if (body !== undefined) {
    opts.headers['content-type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(path, opts);
  } catch {
    throw new Error('Network error — is the TimerPi box reachable?');
  }
  let data = {};
  try { data = await res.json(); } catch { /* non-JSON */ }
  if (!res.ok || data.ok === false) {
    throw new Error(data.error || `Request failed (${res.status})`);
  }
  return data;
}

/** toast(text, kind) — 'info' | 'success' | 'danger'. */
export function toast(text, kind = 'info') {
  const region = document.getElementById('toast-region');
  if (!region) return;
  if (typeof region.showPopover === 'function' && !region.matches(':popover-open')) {
    try { region.showPopover(); } catch { /* */ }
  }
  const el = document.createElement('div');
  el.className = kind === 'info' ? 'toast' : `toast toast-${kind}`;
  el.textContent = text;
  region.appendChild(el);
  setTimeout(() => {
    el.remove();
    if (!region.children.length && typeof region.hidePopover === 'function') {
      try { region.hidePopover(); } catch { /* */ }
    }
  }, 3500);
}

/** showError(el, msg) — field-error paragraphs. */
export function showError(el, msg) {
  if (!el) return;
  el.textContent = msg || '';
  el.hidden = !msg;
}

const CODE_ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';
/** normalizeCode mirrors timerpi.NormalizeCode: typo maps, strip separators. */
export function normalizeCode(s) {
  let out = '';
  for (const ch of String(s || '').toUpperCase()) {
    let c = ch;
    if (c === 'I' || c === 'L') c = '1';
    else if (c === 'O') c = '0';
    else if (c === 'U') c = 'V';
    if (CODE_ALPHABET.includes(c)) out += c;
  }
  return out;
}

/** fmtCode "ABCD1234" → "ABCD-1234". */
export function fmtCode(code) {
  const c = normalizeCode(code);
  return c.length === 8 ? `${c.slice(0, 4)}-${c.slice(4)}` : c;
}

/** fmtRemaining ms → "m:ss" / "h:mm:ss", negative → "+m:ss" (overtime). */
export function fmtRemaining(ms) {
  const over = ms < 0;
  let s = Math.floor(Math.abs(ms) / 1000);
  if (!over && ms % 1000 !== 0) s = Math.ceil(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const body = h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}` : `${m}:${String(sec).padStart(2, '0')}`;
  return over ? `+${body}` : body;
}

/** el(tag, props, ...children) — tiny DOM builder (strings → text nodes). */
export function el(tag, props = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k === 'dataset') Object.assign(node.dataset, v);
    else if (k.startsWith('on') && typeof v === 'function') node.addEventListener(k.slice(2), v);
    else if (v === true) node.setAttribute(k, '');
    else node.setAttribute(k, String(v));
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    node.appendChild(typeof c === 'string' || typeof c === 'number' ? document.createTextNode(String(c)) : c);
  }
  return node;
}

/** Recent events on this browser (privacy: never server-side). */
const RECENT_KEY = 'tp.recent.events';
export function recentEvents() {
  try { return JSON.parse(localStorage.getItem(RECENT_KEY) || '[]'); } catch { return []; }
}
export function rememberEvent(code, name) {
  if (!code) return;
  const list = recentEvents().filter((e) => e.code !== code);
  list.unshift({ code, name: name || code, at: Date.now() });
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(list.slice(0, 8))); } catch { /* */ }
}
export function forgetEvent(code) {
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(recentEvents().filter((e) => e.code !== code))); } catch { /* */ }
}

/**
 * inlineEdit(span, onCommit) — double-click / double-tap / Enter turns a
 * label into an input; Enter or blur commits, Escape cancels. onCommit(text)
 * may throw to revert.
 */
export function inlineEdit(span, onCommit) {
  let lastTap = 0;
  const begin = () => {
    if (span.dataset.editing) return;
    span.dataset.editing = '1';
    const before = span.textContent;
    const input = el('input', { class: 'input input-sm tp-inline-input', value: before, 'aria-label': 'New name', maxlength: 80 });
    span.replaceWith(input);
    input.focus();
    input.select();
    let done = false;
    const finish = async (commit) => {
      if (done) return;
      done = true;
      const text = input.value.trim();
      input.replaceWith(span);
      delete span.dataset.editing;
      if (!commit || !text || text === before) return;
      span.textContent = text;
      try { await onCommit(text); } catch (err) { span.textContent = before; toast(err.message, 'danger'); }
    };
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); finish(true); }
      if (e.key === 'Escape') { e.preventDefault(); finish(false); }
    });
    input.addEventListener('blur', () => finish(true));
  };
  span.addEventListener('dblclick', begin);
  span.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); begin(); } });
  span.addEventListener('touchend', () => {
    const now = Date.now();
    if (now - lastTap < 350) begin();
    lastTap = now;
  });
}
