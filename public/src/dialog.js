/**
 * dialog.js — ftl-styled confirm/prompt replacements (PLAN: always themed
 * popups, never browser chrome). Promise-based; textContent-only (XSS
 * rule). The ftl-themes modal (`<dialog class="modal">` + .modal-header,
 * .btn-close, .modal-footer) — AGENTS.md: dialogs are ftl modals, always.
 * Escape / backdrop click = cancel.
 */
import { el } from './ui.js';

function buildDialog({ title, message, fields, ok, danger }) {
  return new Promise((resolve) => {
    const inputs = {};
    const submit = () => {
      const vals = {};
      for (const id of Object.keys(inputs)) vals[id] = inputs[id].value.trim();
      close(Object.keys(inputs).length ? vals : true);
    };
    const fieldEls = (fields || []).map((f) => {
      inputs[f.id] = el('input', {
        class: 'input', id: f.id, type: f.type, 'aria-label': f.label ? null : f.ariaLabel, autocomplete: f.autocomplete, placeholder: f.placeholder,
        onkeydown: (e) => { if (e.key === 'Enter') submit(); },
      });
      inputs[f.id].value = f.value || '';
      return el('div', { class: 'field' }, f.label && el('label', { class: 'label', for: f.id, text: f.label }), inputs[f.id]);
    });
    const okBtn = el('button', { type: 'button', class: 'btn ' + (danger ? 'btn-danger' : 'btn-primary'), text: ok || 'OK', onclick: submit });
    const dlg = el('dialog', { class: 'modal modal-sm' },
      title && el('div', { class: 'modal-header' }, title, el('button', { type: 'button', class: 'btn-close', 'aria-label': 'Close', 'data-close': true })),
      message ? el('p', { class: 'tp-dlg-msg', text: message }) : null,
      fieldEls,
      el('div', { class: 'modal-footer' }, el('button', { type: 'button', class: 'btn btn-secondary', text: 'Cancel', 'data-close': true }), okBtn));
    document.body.appendChild(dlg);

    let result = null; // Escape, Cancel, × and the backdrop all close with null
    const close = (val) => { result = val; dlg.close(); };
    dlg.addEventListener('close', () => { dlg.remove(); resolve(result); });
    // Page-level Escape handlers (e.g. leave edit mode) must not also fire.
    dlg.addEventListener('keydown', (e) => { if (e.key === 'Escape') e.stopPropagation(); });
    dlg.addEventListener('click', (e) => { // a click on the ::backdrop targets the dialog, outside its box
      const r = dlg.getBoundingClientRect();
      if (e.target === dlg && (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom)) close(null);
    });
    (Object.values(inputs)[0] || okBtn).autofocus = true;
    dlg.showModal();
  });
}

export function tpConfirm(message, { title = '', ok = 'Confirm', danger = false } = {}) {
  return buildDialog({ title, message, ok, danger });
}

export function tpPrompt(message, def = '', { title = '', ok = 'OK', fields = [] } = {}) {
  // One or more text fields; resolves {id: value} or null on cancel.
  return buildDialog({
    title,
    message,
    ok,
    // A single field is labelled by the message above it, not a second copy.
    fields: fields.length ? fields : [{ id: 'value', ariaLabel: message, value: def }],
  });
}
