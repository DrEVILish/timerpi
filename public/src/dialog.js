/**
 * dialog.js — ftl-styled confirm/prompt replacements (PLAN: always themed
 * popups, never browser chrome). Promise-based; textContent-only (XSS
 * rule); Escape / backdrop click = cancel.
 */

function buildDialog({ title, message, fields, ok, danger }) {
  return new Promise((resolve) => {
    const backdrop = document.createElement('div');
    backdrop.className = 'tp-dlg-backdrop';
    const dlg = document.createElement('div');
    dlg.className = 'tp-dlg';
    dlg.setAttribute('role', 'dialog');
    dlg.setAttribute('aria-modal', 'true');
    if (title) dlg.appendChild(Object.assign(document.createElement('strong'), { textContent: title }));
    if (message) {
      const p = document.createElement('p');
      p.textContent = message;
      dlg.appendChild(p);
    }
    const inputs = {};
    for (const f of fields || []) {
      const wrap = document.createElement('div');
      wrap.className = 'field';
      const lbl = document.createElement('label');
      lbl.className = 'label';
      lbl.textContent = f.label;
      lbl.htmlFor = f.id;
      const input = document.createElement('input');
      input.className = 'input';
      input.id = f.id;
      input.value = f.value || '';
      if (f.type) input.type = f.type;
      if (f.autocomplete) input.autocomplete = f.autocomplete;
      if (f.placeholder) input.placeholder = f.placeholder;
      input.addEventListener('keydown', (e) => { if (e.key === 'Enter') submit(); });
      wrap.append(lbl, input);
      dlg.appendChild(wrap);
      inputs[f.id] = input;
    }
    const row = document.createElement('div');
    row.className = 'tp-dlg-actions';
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'btn btn-sm';
    cancel.textContent = 'Cancel';
    const okBtn = document.createElement('button');
    okBtn.type = 'button';
    okBtn.className = 'btn btn-sm ' + (danger ? 'btn-danger' : 'btn-primary');
    okBtn.textContent = ok || 'OK';
    row.append(cancel, okBtn);
    dlg.appendChild(row);
    backdrop.appendChild(dlg);
    document.body.appendChild(backdrop);

    const close = (val) => {
      backdrop.remove();
      document.removeEventListener('keydown', onKey, true);
      resolve(val);
    };
    const submit = () => {
      const vals = {};
      for (const id of Object.keys(inputs)) vals[id] = inputs[id].value.trim();
      close(Object.keys(inputs).length ? vals : true);
    };
    const onKey = (e) => {
      if (e.key === 'Escape') { e.stopPropagation(); close(null); }
    };
    document.addEventListener('keydown', onKey, true);
    cancel.onclick = () => close(null);
    okBtn.onclick = submit;
    backdrop.onclick = (e) => { if (e.target === backdrop) close(null); };
    const first = Object.values(inputs)[0];
    (first || okBtn).focus();
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
    fields: fields.length ? fields : [{ id: 'value', label: message, value: def }],
  });
}
