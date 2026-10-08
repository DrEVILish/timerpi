/**
 * ui.js — small shared helpers for the operator pages (event layer and
 * newer surfaces): JSON API calls, toasts, code formatting, time formats.
 * All DOM text goes through textContent (XSS rule).
 */

/** api(method, path, body?) → parsed JSON; throws Error(message) on !ok
 * (err.status = the HTTP status; unset on a network error). */
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
    throw Object.assign(new Error(data.error || `Request failed (${res.status})`), { status: res.status, retryAfter: Number(res.headers.get('retry-after')) || 0 });
  }
  return data;
}

/** toast(text, kind) — 'info' | 'success' | 'danger'. The region is a
 * manual popover (closed = display:none) so toasts sit above modals. */
let lastToast = null;
export function toast(text, kind = 'info') {
  // C2 drill UX: a flapping link (or a reconnect storm's err frames) must
  // not stack identical popups — swallow an exact repeat inside 4 s.
  const now = Date.now();
  if (lastToast && lastToast.text === text && lastToast.kind === kind && now - lastToast.at < 4000) return;
  lastToast = { text, kind, at: now };
  const region = document.getElementById('toast-region');
  if (!region) return;
  if (!region.matches(':popover-open')) region.showPopover();
  const el = document.createElement('div');
  el.className = kind === 'info' ? 'toast' : `toast toast-${kind}`;
  el.textContent = text;
  region.appendChild(el);
  setTimeout(() => {
    el.remove();
    if (!region.children.length && region.matches(':popover-open')) region.hidePopover();
  }, 4000);
}

/** setText(el, text) — write only on change (no needless layout churn). */
export function setText(el, text) {
  if (el && el.textContent !== text) el.textContent = text;
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

/** Any [data-close] inside an ftl modal (<dialog class="modal">) closes it:
 * the × (.btn-close) and Cancel buttons need no per-dialog wiring. */
document.addEventListener('click', (e) => {
  e.target.closest?.('dialog.modal [data-close]')?.closest('dialog').close();
});

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
 * may throw to revert. A table cell (ftl `.table td.is-editable`) edits in
 * place with ftl's cell states: .is-editing → .is-saving → .is-saved, or
 * aria-invalid when the save is refused.
 */
export function inlineEdit(span, onCommit) {
  const cell = span.tagName === 'TD';
  let lastTap = 0;
  const begin = () => {
    if (span.dataset.editing) return;
    span.dataset.editing = '1';
    const before = span.textContent.trim();
    const input = el('input', { class: cell ? 'input' : 'input input-sm tp-inline-input', value: before, 'aria-label': 'New name', maxlength: 80 });
    if (cell) {
      span.classList.remove('is-editable', 'is-saved');
      span.removeAttribute('aria-invalid');
      span.classList.add('is-editing');
      span.replaceChildren(input);
    } else {
      span.replaceWith(input);
    }
    input.focus();
    input.select();
    let done = false;
    const finish = async (commit) => {
      if (done) return;
      done = true;
      const text = input.value.trim();
      const refocus = cell && document.activeElement === input;
      if (cell) {
        span.classList.replace('is-editing', 'is-editable');
        span.textContent = before;
        if (refocus) span.focus();
      } else {
        input.replaceWith(span);
      }
      delete span.dataset.editing;
      if (!commit || !text || text === before) return;
      span.textContent = text;
      if (cell) { span.classList.add('is-saving'); span.setAttribute('aria-busy', 'true'); }
      try {
        await onCommit(text);
        if (cell) {
          span.classList.add('is-saved');
          setTimeout(() => span.classList.remove('is-saved'), 2000);
        }
      } catch (err) {
        span.textContent = before;
        if (cell) span.setAttribute('aria-invalid', 'true');
        toast(err.message, 'danger');
      } finally {
        if (cell) { span.classList.remove('is-saving'); span.removeAttribute('aria-busy'); }
      }
    };
    input.addEventListener('keydown', (e) => {
      e.stopPropagation(); // the cell's own Enter would reopen the editor
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

/** True while the user is typing or choosing inside root (an input,
 * textarea, select or editable element has focus). Polling pages skip
 * their re-render then, so a redraw never rips out a field mid-edit
 * (STATUS U13). */
export function isEditingIn(root) {
  const a = document.activeElement;
  if (!a || !root || !root.contains(a)) return false;
  return a.matches('input, textarea, select') || a.isContentEditable;
}
