/**
 * TimerPi client (vanilla ES module, no build step)
 *
 * Per-page bootstrap via <body data-page="…">:
 *   home      — no WS; theme picker + create-show + list refresh helpers
 *   dashboard — operator surface: WS mesh, local clock render, commands
 *   display   — fullscreen TV output: local clock render, message overlay
 *
 * Rendering rule (PROTOCOL.md): digits are NEVER server-ticked. This module
 * holds the latest snapshot, computes the active cue's remaining time from
 * anchorTS/rate/pausedElapsedMS with the server clock offset
 * (serverTime - Date.now()) and paints on requestAnimationFrame.
 */

import { Mesh, screenName } from './mesh.v57.js';
import {
  clockView, activeCue, cueAfter, remainingMS, elapsedMS, fmtRemaining,
  fmtDuration, fmtTimeOfDay, fmtCode, computeSchedule,
} from './engine.v57.js';
import { createUndo } from './undo.v57.js';
import { applyTheme, applyIconTheme, setThemeVersion, initClientLog } from './theme.v57.js';
import { applyWaiting } from './waiting.v57.js';
import { tpConfirm, tpPrompt } from './dialog.js';

const THEME_KEY = 'timerpi.theme';
// Product default is BLUE-FUTURE (owner-favourite sci-fi HUD). The html attr
// is already server-themed by bootstrap; boot picks up whatever was applied.
const SERVER_DEFAULT = document.documentElement.getAttribute('data-theme') || 'blue-future';

/** B3: the operator's undo ledger — capture wraps sendCommand, observe
    rides the snapshot pump, perform is wired to the button + Ctrl/Cmd+Z. */
const undo = createUndo({
  // The ledger's own replays must NOT capture: undo.capture(sendCommand's
  // hook) would push each undo's inverse, so a second Ctrl+Z re-applies
  // the very edit the first one just reverted (permanent ping-pong).
  send: (action, args) => sendCommand(action, args, { noCapture: true }),
  snap: () => clockUI?.snap || mesh?.snap || null,
});

/* --------------------------------------------------------------- helpers -- */

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

function setText(el, text) {
  if (el && el.textContent !== text) el.textContent = text;
}

function setState(el, state) {
  if (el && el.dataset.state !== state) el.dataset.state = state;
}

function debounce(fn, ms) {
  let t;
  return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

function toast(text, kind = 'info') {
  // C2 drill UX: a flapping link (or a reconnect storm's err frames) must
  // not stack identical popups — swallow an exact repeat inside 4 s.
  const now = Date.now();
  if (toast.last && toast.last.text === text && toast.last.kind === kind
      && now - toast.last.at < 4000) return;
  toast.last = { text, kind, at: now };
  const region = $('#toast-region');
  if (!region) return;
  // Decode a manual popover region once, so toasts are actually visible
  // (base/display ship .toast-region[popover=manual]; a closed popover is
  // display:none — CONTRACT.md "Toasts over a modal"). Guarded for browsers
  // without the Popover API.
  if (typeof region.showPopover === 'function' && !region.open) {
    try { region.showPopover(); } catch { /* display lock: fall back in flow */ }
  }
  const el = document.createElement('div');
  // core ships .toast-success/.toast-danger only; 'info' uses the base .toast.
  el.className = kind === 'info' ? 'toast' : `toast toast-${kind}`;
  el.textContent = text;
  region.appendChild(el);
  setTimeout(() => {
    el.remove();
    // Release the top layer once the region drains.
    if (!region.children.length && region.open && typeof region.hidePopover === 'function') {
      try { region.hidePopover(); } catch { /* */ }
    }
  }, 4000);
}

/* ------------------------------------------------------------ theme picker -- */
async function initTheme() {
  // Non-default stored theme: clean up the double-bundle the bootstrap left
  // on pages that have no picker (display) — independent of #theme-select (O7).
  const current = document.documentElement.getAttribute('data-theme') || 'blue-future';
  // Icons follow the ACTIVE theme even at the default (templates ship the
  // sprite's name as data — applyIconTheme rewrites <use> hrefs per theme),
  // and oob swaps add new <use> nodes; the swap handler re-runs this.
  applyIconTheme(current);
  // Change Theme dropdown closes when the user clicks/taps anywhere else.
  document.addEventListener('click', (e) => {
    document.querySelectorAll('details.tp-theme-menu[open]').forEach((d) => {
      if (!d.contains(e.target)) d.removeAttribute('open');
    });
  });
  const select = $('#theme-select');
  if (!select) return;
  try {
    const res = await fetch('/ftl/dist/themes.json');
    const themes = await res.json();
    if (Array.isArray(themes) && themes[0]?.version) setThemeVersion(themes[0].version);
    select.innerHTML = '';
    for (const t of themes) {
      const opt = document.createElement('option');
      opt.value = t.dataTheme;
      opt.textContent = t.label + (t.scheme === 'light' ? ' light' : '');
      opt.title = (t.description || '') + (t.tint ? ` — tint ${t.tint}` : '');
      select.appendChild(opt);
    }
  } catch { /* picker stays sparse; bootstrap script already applied the theme */ }
  select.value = current;
  select.addEventListener('change', () => {
    const slug = select.value || 'blue-future';
    applyTheme(slug);
    try { localStorage.setItem(THEME_KEY, slug); } catch { /* */ }
    toast(`Theme: ${select.selectedOptions[0]?.textContent || slug}`);
    // The picker lives in the Change Theme dropdown — close it on pick.
    select.closest('details')?.removeAttribute('open');
  });
}

/* --------------------------------------------------------------- clocks -- */

class ClockUI {
  constructor(page) {
    this.page = page; // 'dashboard' | 'display'
    this.snap = null;
    this.schedule = null;
    this._cueSig = '';
    this._collect();
    this._lastPaint = '';
  }

  /**
   * (Re)collect every DOM reference. The ws oob handler replaces #tp-now /
   * #tp-daybar / #cuelist wholesale, so the cached refs go detached — this
   * must run after every oob swap or the clocks freeze in dead nodes (R3).
   */
  _collect() {
    this.el = {
      // dashboard
      clock: $('#tp-clock'), stateChip: $('#tp-state-chip'),
      meter: $('#tp-meter'), nowPanel: $('#tp-now'),
      delta: $('#tp-delta'),
      nextLabel: $('#tp-next-label'), nextDur: $('#tp-next-dur'),
      nextStart: $('#tp-next-start'), tod: $('#tp-tod'),
      cueLabel: $('#tp-cue-label'), cueSpeaker: $('#tp-cue-speaker'),
      daybar: $('#tp-daybar'), needle: $('#tp-daybar-needle'),
      rows: () => $$('#cuelist tbody tr[data-pos]'),
      lampRun: $('#tp-lamp-run'),
      // display
      stage: $('#d-stage'), dClock: $('#d-clock'), dLabel: $('#d-label'),
      dSpeaker: $('#d-speaker'), dNextLabel: $('#d-next-label'),
      dNextDur: $('#d-next-dur'), dMessage: $('#d-message'),
      dStatus: $('#d-status'), dChip: $('#d-chip'),
    };
    // Digit regions announce politely; digits are JS-painted, the server's
    // placeholder must not be announced (CONTRACT-UI §9d).
    for (const c of [this.el.clock, this.el.dClock]) {
      if (c && !c.hasAttribute('aria-live')) c.setAttribute('aria-live', 'polite');
    }
  }

  setSnapshot(snap) {
    this.snap = snap;
    if (snap) {
      paintBlanked(snap.show?.blanked);
      // Recompute the schedule when the cue STRUCTURE changes (pos/duration/
      // hold/label): post-edit the old rows would strand needle + next-start
      // on a stale day (REVIEW-3 D3).
      const sig = (snap.cues || []).map(c => `${c.pos}:${c.durationMS}:${c.holdMS || 0}:${c.label}`).join('|');
      if (sig !== this._cueSig) {
        this._cueSig = sig;
        this.schedule = computeSchedule(snap);
      }
      // Privacy: keep this browser's recent ledger current (server never
      // serves the code list, so the operator's own device remembers).
      recordRecent(snap.show?.code || '', snap.show?.title || '');
      // B3: resolve a queued add-inverse once the appended row lands.
      undo.observe(snap);
      updateUndoButton();
    }
    this.renderStatic();
  }

  setSchedule(frame) {
    if (frame.rows) this.schedule = { rows: frame.rows, totalMS: frame.totalMS ?? this.schedule?.totalMS ?? 0 };
    if (frame.dayStartTS && this.snap) this.snap.runtime.dayStartTS = frame.dayStartTS;
    this.renderStatic();
  }

  serverNow() { return Date.now() + mesh.clockOffset; }

  /** slow-changing structure: labels, day bar, rows, chips */
  renderStatic() {
    const snap = this.snap;
    if (!snap) return;
    const cue = activeCue(snap);
    const next = snap.runtime.nextPos ? snap.cues.find(c => c.pos === snap.runtime.nextPos) : cueAfter(snap, snap.runtime.activePos);

    // Per-cue alert colors as overridable tokens.
    const root = this.el.nowPanel || this.el.stage || document.body;
    if (cue && cue.alertColor1) root.style.setProperty('--clock-alert1', cue.alertColor1);
    else root.style.removeProperty('--clock-alert1');
    if (cue && cue.alertColor2) root.style.setProperty('--clock-alert2', cue.alertColor2);
    else root.style.removeProperty('--clock-alert2');

    if (this.page === 'dashboard') {
      setText(this.el.cueLabel, cue ? cue.label : '—');
      setText(this.el.cueSpeaker, cue && cue.speaker ? cue.speaker : '');
      // C2 layout: while nothing is on the clock, park the dead meter and
      // dim the placeholder (the --:-- at rail scale read as broken blocks).
      this.el.nowPanel?.classList.toggle('tp-idle-cue', !cue);
      setText(this.el.nextLabel, next ? next.label : '—');
      setText(this.el.nextDur, next ? fmtDuration(next.durationMS) : '');
      setText(this.el.nextStart, next && this.schedule
        ? fmtTimeOfDay((snap.runtime.dayStartTS || this.serverNow()) + (this.schedule.rows.find(r => r.pos === next.pos)?.startMS ?? 0))
        : '');
      this.renderRows();
      // U1: release/park the quick-adjust row with the active cue.
      const canAdjust = !!cue;
      for (const b of $$('.tp-adjust-row [data-adjust]')) {
        if (b.disabled !== !canAdjust) b.disabled = !canAdjust;
      }
    } else {
      setText(this.el.dLabel, cue ? cue.label : (snap.show?.title || ''));
      setText(this.el.dSpeaker, cue && cue.speaker ? `🎙 ${cue.speaker}` : '');
      setText(this.el.dNextLabel, next ? next.label : '');
      setText(this.el.dNextDur, next ? fmtDuration(next.durationMS) : '');
    }
    this.renderMessage();
  }

  renderRows() {
    const snap = this.snap;
    if (!snap) return;
    const act = snap.runtime.activePos;
    // C2: OFFLINE-RENDER parity. Row CREATION belongs to the server's oob
    // fragment; while the mesh master executes locally there is no server,
    // so an offline add/delete would mutate snap and leave the table blind
    // (drill finding: add applied, rows frozen at 3, badge at 0). Heal the
    // DOM whenever its row set diverges from snap, online or off — when the
    // server IS online its oob frame lands microseconds later and this
    // becomes a no-op.
    this.rebuildRowsIfNeeded();
    // A3: the operator's filter hides non-matching rows. Applied HERE (this
    // runs after every snapshot repaint and oob swap) so live cue edits
    // stay filtered instead of flashing the full table back.
    const filterInput = $('#tp-cue-filter');
    const q = (filterInput?.value || '').trim().toLowerCase();
    let visible = 0;
    for (const tr of this.el.rows()) {
      tr.hidden = !!q && !tr.textContent.toLowerCase().includes(q);
      if (!tr.hidden) visible++;
    }
    const noMatch = $('#tp-cue-no-match');
    if (noMatch) noMatch.hidden = !q || visible > 0;
    const count = $('#tp-cue-filter-count');
    if (count) count.textContent = q ? `${visible} / ${this.el.rows().length}` : '';
    let activeTr = null;
    for (const tr of this.el.rows()) {
      const pos = Number(tr.dataset.pos);
      // Core paints rows through tr.is-active (--row-active-bg + accent
      // marker); tp-row-active stays as app glue (REVIEW-2 R2).
      tr.classList.toggle('is-active', pos === act);
      tr.classList.toggle('tp-row-active', pos === act);
      tr.classList.toggle('tp-row-past', !!act && pos < act);
      tr.classList.toggle('tp-row-next', pos === snap.runtime.nextPos);
      if (pos === act) activeTr = tr;
    }
    // U1: the running cue walks down the table all day — chase it so it is
    // in view without the operator hunting (nearest = no jump if visible).
    // Never chase while filtered: a hidden row would yank the scrollport.
    if (activeTr && !activeTr.hidden && act !== this._chasedPos) {
      this._chasedPos = act;
      activeTr.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
    }
    this.renderDaybarSegs();
  }

  renderDaybarSegs() {
    const bar = this.el.daybar;
    if (!bar || !this.schedule) return;
    if (!this.snap?.runtime?.dayStartTS) {
      // U1: unanchored day — segment offsets are relative, not wall-clock,
      // so epoch math here would mark every segment "done". The
      // server-rendered initial state stands until the day is anchored.
      const needle = this.el.needle;
      if (needle) needle.hidden = true;
      return;
    }
    const total = this.schedule.totalMS || 1;
    const nowMS = this.serverNow() - this.snap.runtime.dayStartTS;
    for (const seg of $$('.tp-daybar-seg', bar)) {
      const s = Number(seg.dataset.startMS), e = Number(seg.dataset.endMS);
      seg.classList.toggle('is-done', e <= nowMS);
      seg.classList.toggle('is-active', s <= nowMS && nowMS < e);
    }
  }

  /**
   * C2: mirror the server's frag-messages markup from snap. Runs on every
   * renderMessage; when the DOM already matches (server oob drew it), this
   * is a single signature compare. Everything the operator can tap rides
   * the same data-cmd contracts, so the document-level handlers keep
   * working with zero special cases.
   */
  renderMessagesList() {
    const panel = $('#messages-panel');
    if (!panel) return;
    const msgs = this.snap?.messages || [];
    const sig = `${msgs.length}|${msgs.map(m => `${m.id}:${!!m.shownAt}`).join(',')}`;
    if (panel.dataset.tpMsgSig === sig) return; // server frag already matches
    // A server oob for this fragment will land with ITS OWN identical
    // markup; the signature guard above makes both paths converge instead
    // of fighting (last writer wins, both render the same truth).
    panel.dataset.tpMsgSig = sig;
    const badge = panel.querySelector('.badge.push');
    if (badge) setText(badge, String(msgs.length));
    let list = panel.querySelector('ul.list');
    const empty = panel.querySelector('.empty-state');
    if (!msgs.length) {
      if (list) list.remove();
      if (!empty && !panel.querySelector('.tp-wrapup')) return;
      if (empty) return;
      return this.messagesEmptyState(panel);
    }
    if (empty) empty.remove();
    if (!list) {
      list = document.createElement('ul');
      list.className = 'list';
      // Insert after the compose form (server markup order).
      const form = panel.querySelector('form[data-cmd="addMsg"]');
      form?.after(list);
    }
    // Rebuild items (id-keyed; server order = DB order).
    list.replaceChildren(...msgs.map((m) => {
      const li = document.createElement('li');
      li.className = 'list-item' + (m.shownAt ? ' is-active' : '');
      const title = document.createElement('span');
      title.className = 'list-item-title';
      setText(title, m.text);
      const meta = document.createElement('span');
      meta.className = 'list-item-meta';
      const status = document.createElement('span');
      if (m.shownAt) {
        status.className = 'status status-warn';
        setText(status, 'ON STAGE');
      } else {
        status.className = 'status';
        setText(status, 'queued');
      }
      const cluster = document.createElement('span');
      cluster.className = 'cluster is-gap-2xs';
      const btn = document.createElement('button');
      btn.className = 'btn btn-sm' + (m.shownAt ? '' : ' btn-go');
      btn.dataset.cmd = m.shownAt ? 'hideMsg' : 'showMsg';
      btn.dataset.id = String(m.id);
      setText(btn, m.shownAt ? 'Hide' : 'Show');
      cluster.appendChild(btn);
      meta.append(status, cluster);
      li.append(title, meta);
      return li;
    }));
    let clear = panel.querySelector('button[data-cmd="clearMsgs"]');
    if (!clear) {
      clear = document.createElement('button');
      clear.className = 'btn btn-sm btn-ghost';
      clear.dataset.cmd = 'clearMsgs';
      setText(clear, 'Clear all');
      list.after(clear);
    }
  }

  messagesEmptyState(panel) {
    // Mirror the server's empty-state block (frag-messages).
    const wrap = document.createElement('div');
    wrap.className = 'empty-state';
    const mk = (cls, text) => { const s = document.createElement('span'); s.className = cls; setText(s, text); return s; };
    wrap.append(mk('empty-state-icon', '💬'), mk('empty-state-title', 'No messages'),
      mk('empty-state-hint', 'One tap on WRAP UP! puts it on every display — or type your own above and Add.'));
    const wrapup = panel.querySelector('.tp-wrapup');
    const form = panel.querySelector('form[data-cmd="addMsg"]');
    (form || wrapup)?.after(wrap);
  }

  /**
   * C2: rebuild the cue table body when its rows stop matching snap.
   * Mirrors frag-cuelist's row contract (CONTRACT-UI §2): label/break/tags/
   * alerts/kind/notes, speaker, dur(+hold), schedule start/end, and the
   * row action group (GO / cue / move / inspector / duplicate A4 / armed
   * delete). Text goes in via textContent only — never innerHTML.
   */
  rebuildRowsIfNeeded() {
    const snap = this.snap;
    if (!snap || this.page !== 'dashboard') return;
    const tbody = $('#cuelist tbody');
    if (!tbody) return;
    const domSig = [...tbody.querySelectorAll('tr[data-pos]')].map(tr =>
      `${tr.dataset.pos}:${tr.querySelector('.tp-cue-label')?.childNodes[0]?.textContent ?? ''}`).join(',');
    const snapSig = (snap.cues || []).map(c =>
      `${c.pos}:${c.label ?? ''}`).join(',');
    if (domSig === snapSig) return; // server oob owns the DOM and it's true
    this.rebuildCueRows(tbody, snap);
  }

  rebuildCueRows(tbody, snap) {
    const scheduleByPos = new Map((this.schedule?.rows || []).map(r => [r.pos, r]));
    const rows = snap.cues || [];
    const wrap = tbody.parentElement; // table
    const list = wrap?.parentElement; // #cuelist
    if (!rows.length) {
      // Mirror the server's empty-state block; the table itself goes away.
      wrap?.remove();
      if (list && !list.querySelector('.empty-state')) {
        const empty = document.createElement('div');
        empty.className = 'empty-state';
        const mk = (cls, text) => { const s = document.createElement('span'); s.className = cls; setText(s, text); return s; };
        empty.append(mk('empty-state-icon', '∅'), mk('empty-state-title', 'No cues yet'),
          mk('empty-state-hint', 'Add one above, or drag an .xlsx / .csv / .json file onto the Import panel on the right — it attaches itself for you.'));
        list.appendChild(empty);
      }
      return;
    }
    if (!wrap) {
      // Server rendered the empty state; restore the table skeleton.
      list?.querySelector('.empty-state')?.remove();
      const table = document.createElement('table');
      table.className = 'table is-sticky is-striped';
      const thead = document.createElement('thead');
      const hr = document.createElement('tr');
      for (const h of ['#', 'Cue', 'Speaker', 'Dur', 'Start', 'End', '']) {
        const th = document.createElement('th');
        setText(th, h);
        hr.appendChild(th);
      }
      thead.appendChild(hr);
      const tb = document.createElement('tbody');
      table.append(thead, tb);
      list?.appendChild(table);
      tbody = tb;
    }
    const fragment = document.createDocumentFragment();
    for (const c of rows) fragment.appendChild(this.cueRow(c, scheduleByPos.get(c.pos)));
    tbody.replaceChildren(fragment);
    this.reiconRows();
    this._cueSig = ''; // force the schedule refresh on the next setSnapshot
  }

  cueRow(c, schedRow) {
    const tr = document.createElement('tr');
    tr.dataset.pos = String(c.pos);
    const td = (cls) => { const t = document.createElement('td'); if (cls) t.className = cls; return t; };
    // # — the A3/U1 plain number (reorder lives in the ▲▼ buttons).
    const pos = td('tp-cue-pos');
    const num = document.createElement('span');
    num.className = 'mono';
    setText(num, String(c.pos).padStart(2, '0'));
    pos.appendChild(num);
    // Cue label + badges + notes.
    const cueTd = td();
    const labelWrap = document.createElement('div');
    labelWrap.className = 'tp-cue-label';
    if (c.kind === 'break' || c.kind === 'hold') {
      const b = document.createElement('span');
      b.className = 'badge badge-accent';
      setText(b, c.kind === 'break' ? 'BREAK' : 'HOLD');
      labelWrap.appendChild(b);
      labelWrap.appendChild(document.createTextNode(' '));
    }
    labelWrap.appendChild(document.createTextNode(c.label ?? ''));
    const alertBadge = document.createElement('span');
    alertBadge.className = 'badge tp-alert-badge';
    alertBadge.hidden = true;
    labelWrap.appendChild(alertBadge);
    if (c.autoContinue) {
      const b = document.createElement('span');
      b.className = 'badge';
      b.title = 'Auto-continues to the next cue at zero';
      setText(b, 'AUTO');
      labelWrap.appendChild(b);
    }
    cueTd.appendChild(labelWrap);
    const tags = document.createElement('div');
    tags.className = 'cluster is-gap-2xs';
    // tags arrive as the raw string (server stores "VT GFX"); split to match
    // views.tagsOf — for..of over the string painted one badge per LETTER.
    for (const t of String(c.tags || '').split(/\s+/).filter(Boolean)) {
      const b = document.createElement('span');
      b.className = 'badge badge-accent';
      setText(b, t);
      tags.appendChild(b);
    }
    if (c.alert1MS > 0) {
      const b = document.createElement('span');
      b.className = 'badge';
      b.title = 'Alert 1 threshold';
      b.dataset.alert1 = c.alertColor1 || '';
      setText(b, `⚠1 ${fmtDuration(c.alert1MS)}`);
      tags.appendChild(b);
    }
    if (c.alert2MS > 0) {
      const b = document.createElement('span');
      b.className = 'badge';
      b.title = 'Alert 2 threshold';
      b.dataset.alert2 = c.alertColor2 || '';
      setText(b, `⚠2 ${fmtDuration(c.alert2MS)}`);
      tags.appendChild(b);
    }
    if (c.timerKind && c.timerKind !== 'COUNTDOWN') {
      const b = document.createElement('span');
      b.className = 'badge';
      setText(b, c.timerKind);
      tags.appendChild(b);
    }
    cueTd.appendChild(tags);
    if (c.notes) {
      const n = document.createElement('div');
      n.className = 'tp-cue-notes';
      setText(n, c.notes);
      cueTd.appendChild(n);
    }
    // Speaker.
    const speaker = td('text-truncate');
    if (c.speaker) {
      speaker.appendChild(this.icon('icon-microphone'));
      speaker.appendChild(document.createTextNode(' ' + c.speaker));
    }
    // Dur (+hold).
    const dur = td('tp-cue-dur');
    dur.appendChild(document.createTextNode(fmtDuration(c.durationMS)));
    if (c.holdMS > 0) {
      const hold = document.createElement('span');
      hold.className = 'tp-cue-notes';
      setText(hold, ` +${fmtDuration(c.holdMS)} hold`);
      dur.appendChild(hold);
    }
    // Start/End from the computed schedule (same math as the server frag).
    const start = td('tp-cue-start mono');
    const end = td('tp-cue-end mono');
    if (schedRow) {
      const anchor = this.snap?.runtime?.dayStartTS || this.serverNow();
      setText(start, fmtTimeOfDay(anchor + (schedRow.startMS || 0)));
      setText(end, fmtTimeOfDay(anchor + (schedRow.endMS || 0)));
    } else {
      setText(start, '—');
      setText(end, '—');
    }
    // Row actions — same data-cmd contracts as the server frag.
    const btns = td('tp-cue-rowbtns');
    const group = document.createElement('div');
    group.className = 'btn-group';
    const mkBtn = (cmd, cls, title, text, iconId, extra = {}) => {
      const b = document.createElement('button');
      b.className = `btn btn-sm ${cls}`;
      b.dataset.cmd = cmd;
      b.dataset.pos = String(c.pos);
      for (const [k, v] of Object.entries(extra)) b.dataset[k] = v;
      b.title = title;
      b.setAttribute('aria-label', title);
      if (iconId) {
        b.appendChild(this.icon(iconId));
      } else if (text) {
        setText(b, text);
      }
      group.appendChild(b);
    };
    const ariaPos = String(c.pos).padStart(2, '0');
    mkBtn('go', 'btn-go', `GO — start cue ${ariaPos}`, 'Go');
    mkBtn('jump', 'btn-ghost', "Cue it up (playhead here, don't start yet)", null, 'icon-chevron-right', { start: 'false' });
    mkBtn('cueMove', 'btn-icon btn-ghost', 'Move up', null, 'icon-chevron-up', { dir: 'up' });
    mkBtn('cueMove', 'btn-icon btn-ghost', 'Move down', null, 'icon-chevron-down', { dir: 'down' });
    // Inspector (data-insp, not a data-cmd button — initInspector owns it).
    const insp = document.createElement('button');
    insp.className = 'btn btn-sm btn-icon btn-ghost';
    insp.dataset.insp = String(c.pos);
    insp.title = 'Open cue inspector (all fields)';
    insp.setAttribute('aria-label', `Edit cue ${ariaPos} — full inspector`);
    insp.appendChild(this.icon('icon-edit'));
    group.appendChild(insp);
    mkBtn('cueDup', 'btn-icon btn-ghost', 'Duplicate cue — copy lands right after this row', null, 'icon-copy');
    mkBtn('cueDel', 'btn-icon btn-danger', 'Delete cue', null, 'icon-trash');
    btns.appendChild(group);
    tr.append(pos, cueTd, speaker, dur, start, end, btns);
    return tr;
  }

  /** Small inline icon, matching the template's <svg class="icon"><use/> */
  icon(id) {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    svg.setAttribute('class', 'icon');
    svg.setAttribute('aria-hidden', 'true');
    const use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
    use.setAttribute('href', `/ftl/dist/icons/xbmc.svg#${id}`);
    svg.appendChild(use);
    return svg;
  }

  /** Fresh rows ship the template sprite name; retarget per active theme. */
  reiconRows() {
    try { applyIconTheme(document.documentElement.getAttribute('data-theme') || 'blue-future'); } catch { /* */ }
  }

  renderMessage() {
    const shown = (this.snap?.messages || []).filter(m => m.shownAt).sort((a, b) => b.shownAt - a.shownAt)[0];
    this.renderMessagesList();
    const el = this.el.dMessage;
    if (el) {
      if (shown) {
        el.hidden = false;
        const box = el.firstElementChild || el;
        setText(box, shown.text);
        // UX2: the message's own colour rides the LEFT edge only (spec
        // message-overlay pattern) — the body text stays theme ink, and an
        // unset colour keeps the neutral accent edge the CSS paints.
        box.style.borderColor = '';
        if (shown.color) box.style.borderInlineStartColor = shown.color;
      } else {
        el.hidden = true;
      }
    }
    const banner = $('#tp-live-msg');
    if (banner) {
      if (shown) {
        banner.hidden = false;
        const box = banner.firstElementChild || banner;
        setText(box, shown.text);
        box.style.borderColor = '';
        if (shown.color) box.style.borderInlineStartColor = shown.color;
      } else {
        banner.hidden = true;
      }
    }
  }

  /** fast path: digits, meter, needle, state */
  paint() {
    const snap = this.snap;
    if (!snap) return;
    const view = clockView(snap, this.serverNow());
    // C2 layout: idle paints BLANK (LED-dark kiosk convention) — the old
    // '--:--' placeholder at rail scale read as four glowing blocks. The
    // READY chip + lamp carry the state; the readout stays dark.
    const text = view.state === 'idle' && view.remaining == null ? '' :
      view.remaining == null ? '--:--' : fmtRemaining(view.remaining);
    const cue = activeCue(snap);

    const paintClock = (el) => {
      if (!el) return;
      // C2: compare against the LIVE node, not just the cache — the cache
      // initialises '' and equalled the new idle-blank text, so the first
      // frame skipped and the server's '--:--' placeholder survived forever
      // (drill-found). The cache is now maintained for real.
      if (el.textContent !== text) { setText(el, text); this._lastPaint = text; }
      setState(el, view.state);
    };

    if (this.page === 'dashboard') {
      paintClock(this.el.clock);
      setState(this.el.nowPanel, view.state);
      setText(this.el.stateChip, {
        idle: 'READY', armed: 'ARMED', running: 'RUNNING', overtime: 'OVERTIME',
        paused: 'PAUSED', held: 'HELD', blank: 'BLANK',
        alert1: 'ALERT 1', alert2: 'ALERT 2',
      }[view.state] || view.state.toUpperCase());
      const lamp = this.el.lampRun;
      if (lamp) {
        lamp.classList.toggle('is-on', view.state === 'running');
        lamp.classList.toggle('is-warn', view.state === 'paused' || view.state === 'held' || view.state === 'alert1');
        lamp.classList.toggle('is-error', view.state === 'overtime' || view.state === 'alert2');
      }
      if (this.el.meter && cue) {
        const pct = Math.min(100, Math.max(0, (cue.durationMS ? elapsedMS(snap, this.serverNow()) / cue.durationMS : 0) * 100));
        this.el.meter.style.setProperty('--meter-level', pct.toFixed(1) + '%');
      }
      this.paintDelta(view);
      setText(this.el.tod, fmtTimeOfDay(this.serverNow()));
      // keep rate control in step with the (possibly server/mesh-applied) rate
      // — paused while the inline editor owns the readout (double-click).
      const slider = $('#tp-rate'), rateOut = $('#tp-rate-out');
      if (!this._rateEditing) {
        if (slider && document.activeElement !== slider) {
          slider.value = String(Math.round((snap.runtime.rate || 1) * 100));
        }
        if (rateOut) setText(rateOut, '×' + (snap.runtime.rate || 1).toFixed(2));
      }
      this.renderDaybarSegs();
      const p = this.el.needle && snap.runtime.dayStartTS && this.schedule && this.schedule.totalMS > 0
        ? Math.min(100, Math.max(0,
          (this.serverNow() - snap.runtime.dayStartTS) / this.schedule.totalMS * 100))
        : null;
      if (this.el.needle) this.el.needle.hidden = p == null;
      if (p != null) this.el.needle.style.left = p.toFixed(3) + '%';
      // live alert badge on the active row
      const tr = this.el.rows().find(r => Number(r.dataset.pos) === snap.runtime.activePos);
      if (tr) {
        const badge = $('.tp-alert-badge', tr);
        if (badge) {
          const label = view.state === 'alert1' ? 'ALERT 1' : view.state === 'alert2' ? 'ALERT 2' : view.state === 'overtime' ? 'OVERTIME' : '';
          setText(badge, label);
          badge.hidden = !label;
        }
      }
    } else {
      paintClock(this.el.dClock);
      setState(this.el.stage, view.state);
      // Stage urgency chip (UX2): painted only for states that must read at a
      // glance; idle/armed/running stay quiet (clock colour + status word
      // already carry them — one loud thing: the clock).
      const chip = this.el.dChip;
      if (chip) {
        const rem = view.remaining;
        const chipText = view.state === 'overtime' ? `OVERTIME ${fmtRemaining(rem)}`
          : view.state === 'alert1' ? `WRAP UP ${fmtRemaining(rem)}`
          : view.state === 'alert2' ? `ALERT 2 ${fmtRemaining(rem)}`
          : { held: 'HELD', paused: 'PAUSED', blank: 'BLANK' }[view.state] || '';
        chip.hidden = !chipText;
        if (chipText) {
          setText(chip, chipText);
          setState(chip, view.state);
        } else {
          delete chip.dataset.state;
        }
      }
      const st = this.el.dStatus;
      if (st) {
        // State word first so HELD/overtime read at TV distance, then the
        // show identity, link state, next cue. (P8's "F fullscreen" moved to
        // its own corner chip — #tp-fs-chip, see initFullscreenHint.)
        const bits = [
          view.state.toUpperCase(),
          snap.show?.title || '',
          statusLabel(mesh),
          this.el.dNextLabel && snap.runtime.nextPos ? `Next: ${this.el.dNextLabel.textContent}` : '',
        ];
        setText(st, bits.filter(Boolean).join('  ·  '));
      }
    }
  }

  /** A2: the live over/under chip ("+0:35 vs plan") — recomputed each frame
     from the schedule rows + the projected real end, exactly mirroring the
     server's ComputeScheduleRuntime math (views.DeltaMS is the first paint;
     this keeps it honest between oob swaps). Countdown cues only: CLOCK /
     COUNTSTOP have no scheduled-duration meaning to drift from. */
  paintDelta(view) {
    const d = this.el.delta;
    if (!d) return;
    const snap = this.snap;
    const cue = activeCue(snap);
    const kind = cue?.timerKind || 'COUNTDOWN';
    const onClock = !!snap.runtime.activePos && (cue?.durationMS || 0) > 0
      && kind === 'COUNTDOWN' && view.remaining != null
      && ['running', 'paused', 'held', 'overtime', 'alert1', 'alert2'].includes(view.state);
    const row = onClock ? this.schedule?.rows?.find(r => r.pos === snap.runtime.activePos) : null;
    let delta = null;
    if (row && snap.runtime.dayStartTS && row.endMS != null) {
      const endTS = snap.runtime.dayStartTS + row.endMS;
      const rate = snap.runtime.rate > 0 ? snap.runtime.rate : 1;
      const actualEnd = view.remaining > 0 ? this.serverNow() + view.remaining / rate : this.serverNow();
      delta = actualEnd - endTS;
    }
    if (delta == null) { d.hidden = true; return; }
    const sec = Math.round(delta / 1000) * 1000;
    if (Math.abs(sec) < 30000) {
      d.textContent = 'on plan';
      d.dataset.state = 'ontime';
    } else {
      d.textContent = (sec > 0 ? '+' : '−') + fmtDuration(Math.abs(sec)) + ' vs plan';
      d.dataset.state = sec > 0 ? 'over' : 'under';
    }
    d.hidden = false;
  }

  start() {
    const loop = () => { this.paint(); requestAnimationFrame(loop); };
    requestAnimationFrame(loop);
    setInterval(() => { if (this.snap) this.renderStatic(); }, 2000); // slow refresh
  }
}

function statusLabel(mesh) {
  if (mesh.serverOnline()) return `Server link up · ${mesh.openPeerIds().length + 1} connected`;
  if (mesh.isMaster()) return `Server down · show keeps running from here (${mesh.openPeerIds().length + 1} device${mesh.openPeerIds().length === 0 ? '' : 's'})`;
  if (mesh.masterId) return 'Server down · mesh holds the show';
  return 'Server down · reconnecting…';
}

/* --------------------------------------------------------------- mesh UI -- */

let mesh = null;
let clockUI = null;
let prevOnline = null; // last known server-link state, for transition toasts

function initMesh(showId, role, page) {
  clockUI = new ClockUI(page);
  mesh = new Mesh({
    showId,
    role,
    // F1: display tabs self-register a stable screen name (URL ?screen=,
    // else localStorage, else generated). Controls never carry one.
    screen: role === 'controls' ? '' : screenName(),
    onSnapshot: (fresh) => {
      clockUI.setSnapshot(mesh.snap);
      updateSharePanel(mesh.snap);
      updateConnection();
    },
    onStatusChange: updateConnection,
    onFrame: (m) => {
      // DEBUG SEAM (drills): mirror the live mesh for page-level inspection.
      try { window.__tpmesh = mesh; } catch { /* */ }
      switch (m.t) {
        case 'oob':
          applyOOB(m);
          break;
        case 'schedule':
          clockUI?.setSchedule(m);
          break;
        case 'message': {
          if (mesh.snap && m.message) {
            const list = mesh.snap.messages.filter(x => x.id !== m.message.id);
            if (m.message.shownAt || m.message.text) list.push(m.message);
            mesh.snap.messages = list;
            mesh.snap.updatedAt = Math.max(mesh.snap.updatedAt, m.updatedAt || 0) + 1;
            clockUI?.renderMessage();
          }
          break;
        }
        case 'screens':
          screensCache.screens = m.screens || [];
          renderScreens();
          break;
        case 'peers':
          screensRefreshSoon(); // live counts changed (a screen joined/left)
          break;
        case 'display':
          if (m.theme && m.theme !== document.documentElement.getAttribute('data-theme')) {
            // Swap the bundle too, not just the attribute — dist bundles are
            // per-theme token blocks (REVIEW-2 R6); no persistence: the
            // operator's push must not overwrite the display's local pick.
            applyTheme(m.theme);
          }
          break;
        case 'timer':
        case 'cue':
          if (m.runtime && mesh.snap && (m.updatedAt ?? 0) >= mesh.snap.updatedAt) {
            Object.assign(mesh.snap.runtime, m.runtime);
            mesh.snap.updatedAt = m.updatedAt ?? mesh.snap.updatedAt;
            if (m.serverTime) mesh.clockOffset = m.serverTime - Date.now();
            clockUI?.setSnapshot(mesh.snap);
          }
          break;
        default: break;
      }
    },
    onLog: (level, msg) => { if (level === 'error') toast(msg, 'danger'); },
    // Offline-edit merge notice (OFFLINE-EDIT.md): the sync response reports
    // remote survivors; the existing oob/state fanout already repaints.
    onMerged: (n) => toast(`Merged ${n} remote change${n === 1 ? '' : 's'}`, 'info'),
  });
  mesh.start();
  updateConnection();
}

function updateConnection() {
  if (!mesh) return;
  applyWaiting(mesh.wsStatus); // orphaned /d/ surfaces raise their overlay
  const online = mesh.serverOnline();
  // U1: say the transition out loud once — an operator staring at the cue
  // table learns the show now runs on the mesh (or that the server is back).
  if (prevOnline === true && !online) toast(statusLabel(mesh), 'info');
  if (prevOnline === false && online) toast('Server link back — everything resyncs automatically', 'success');
  prevOnline = online;
  const lampWs = $('#lamp-ws'), lampMesh = $('#lamp-mesh');
  const label = $('#conn-label');
  if (lampWs) {
    lampWs.classList.toggle('is-on', mesh.serverOnline());
    lampWs.classList.toggle('is-error', !mesh.serverOnline());
    lampWs.title = `Server link: ${mesh.wsStatus}`;
  }
  if (lampMesh) {
    const linked = mesh.openPeerIds().length > 0;
    lampMesh.classList.toggle('is-on', linked);
    lampMesh.classList.toggle('is-warn', !linked);
    lampMesh.title = 'Display mesh (WebRTC)';
  }
  if (label) {
    setText(label, statusLabel(mesh));
    const code = $('#conn-code');
    if (code) setText(code, mesh.snap?.show?.code ? fmtCode(mesh.snap.show.code) : fmtCode(mesh.showId));
  }
  const peersCount = $('#peers-count');
  if (peersCount) setText(peersCount, String(mesh.openPeerIds().length + 1));
}

/* --------------------------------------------------------- share panel -- */

function updateSharePanel(snap) {
  const qr = $('#share-qr');
  if (qr && qr.dataset.srcTpl) {
    const url = `${location.origin}/d/${qr.dataset.show}`;
    qr.src = qr.dataset.srcTpl.replace('__DATA__', encodeURIComponent(url));
    qr.dataset.srcTpl = '';
    const link = $('#share-display-link');
    if (link) link.href = url;
    const mirror = $('#share-url-mirror');
    if (mirror) mirror.value = url;
  }
  const code = $('#share-code');
  if (code) setText(code, fmtCode(snap?.show?.code ?? mesh.showId));
}

function initCueFilter() {
  // A3: wire the running-order filter. The DOM update lives in
  // renderRows; this only hooks the box (input → re-filter, Esc → clear).
  const input = $('#tp-cue-filter');
  if (!input) return;
  input.addEventListener('input', () => clockUI?.renderRows());
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      input.value = '';
      clockUI?.renderRows();
      input.blur();
      e.preventDefault();
    }
  });
}

function initSharePanel() {
  $('#share-copy')?.addEventListener('click', async () => {
    const input = $('#share-url-mirror');
    const url = input?.value || $('#share-display-link')?.href;
    try {
      await navigator.clipboard.writeText(url);
      toast('Display link copied', 'success');
    } catch {
      input?.select();
      document.execCommand?.('copy');
      toast('Link selected — press Ctrl+C', 'info');
    }
  });
}

/* ------------------------------------------------------------ commands -- */

// Transport actions that must never double-fire across a rapid operator
// tap (mouse click + Space echo, jittery double-tap). A repeat of the SAME
// action inside the grace window is swallowed; a different action — or a
// different cue position — still goes through immediately.
const TRANSPORT_GRACE_MS = 150;
let lastTransportKey = '';
let lastTransportAt = 0;

function sendCommand(action, args = {}, opts = {}) {
  if (!mesh) return;
  // B3: capture the inverse BEFORE the effect leaves the page.
  if (!opts.noCapture) undo.capture(action, args, clockUI?.snap || mesh.snap);
  if (['go', 'start', 'pause', 'reset', 'next', 'prev'].includes(action)) {
    const key = `${action}:${args.pos ?? ''}`;
    const now = Date.now();
    if (key === lastTransportKey && now - lastTransportAt < TRANSPORT_GRACE_MS) return;
    lastTransportKey = key;
    lastTransportAt = now;
  }
  mesh.sendCommand(action, args);
}

/* --- U1: quick length adjust on the cue ON THE CLOCK ----------------------
   Each tap queues ±ms against the active cue and one debounced cueEdit
   carries the accumulated delta, computed against the LATEST snapshot at
   flush time — so two fast taps are "+1m", not "... write the same stale
   duration twice". cueEdit {pos, durationMS} is existing protocol
   (CONTRACT-UI §4); no new command. */
let pendingAdjust = null;
let adjustTimer = null;

function queueAdjust(delta) {
  const cue = mesh?.snap ? activeCue(mesh.snap) : null;
  if (!cue) {
    toast('No cue on the clock to adjust', 'info');
    return;
  }
  const p = pendingAdjust && pendingAdjust.pos === cue.pos
    ? pendingAdjust
    : { pos: cue.pos, delta: 0 };
  p.delta += delta;
  pendingAdjust = p;
  clearTimeout(adjustTimer);
  adjustTimer = setTimeout(flushAdjust, 300);
}

function flushAdjust() {
  const p = pendingAdjust;
  pendingAdjust = null;
  if (!p || !mesh?.snap) return;
  const cue = (mesh.snap.cues || []).find(c => c.pos === p.pos);
  if (!cue) return;
  const dur = Math.max(0, (cue.durationMS || 0) + p.delta);
  sendCommand('cueEdit', { pos: cue.pos, durationMS: dur });
  toast(`Cue ${String(cue.pos).padStart(2, '0')} now ${fmtDuration(dur)}`, 'info');
}

/**
 * Two-tap delete for the cue table: the first tap ARMS the bin (visual
 * state + rollback in 2.5 s), only a second tap inside that window sends
 * cueDel. No modal maze; a stressed misclick costs nothing.
 */
function confirmDelete(btn) {
  if (btn.dataset.u1armed === '1') {
    clearTimeout(btn._u1armer);
    delete btn.dataset.u1armed;
    btn.classList.remove('tp-del-armed');
    btn.title = 'Delete cue';
    return true;
  }
  btn.dataset.u1armed = '1';
  btn.classList.add('tp-del-armed');
  btn.title = 'Tap again to delete this cue';
  toast('Delete armed — tap the bin again to remove this cue', 'info');
  btn._u1armer = setTimeout(() => {
    delete btn.dataset.u1armed;
    btn.classList.remove('tp-del-armed');
    btn.title = 'Delete cue';
  }, 2500);
  return false;
}

// E3 blackout: one body flag drives the .tp-blanked overlay (CSS-only
// visibility). Server renders data-blanked on first paint; every snapshot
// adopt repaints it, so oob swaps can never desync the screens.
function paintBlanked(on) {
  const blanked = on === true || on === 'true';
  if (document.body) document.body.dataset.blanked = blanked ? 'true' : 'false';
  const btn = document.getElementById('tp-blank');
  if (btn) btn.setAttribute('aria-pressed', blanked ? 'true' : 'false');
}

function initCommandButtons() {  document.addEventListener('click', (e) => {
    // Forms own their submits: without this guard every click INSIDE a
    // data-cmd form (focusing the text input, hitting Add) bubbles to this
    // delegation via closest() and fires a GHOST command with empty args —
    // an "addMsg needs text" err on every message-field focus plus a bogus
    // undo inverse resolving against the next real row. The submit handler
    // below is the only sender for forms.
    if (e.target.closest('form[data-cmd]')) return;
    const btn = e.target.closest('[data-cmd]');
    if (!btn) {
      // E3 blackout toggle (lives in the oob-swapped frag-current, so it
      // must delegate like every other dashboard button — a direct
      // listener dies with the first #tp-now re-render).
      const blankBtn = e.target.closest('#tp-blank');
      if (blankBtn) {
        // Instant both ways (owner decision: no confirm on blank) — the
        // pressed state + STANDBY screens make the state obvious, and
        // release is one tap away.
        sendCommand(document.body?.dataset.blanked === 'true' ? 'unblank' : 'blank', {});
        return;
      }
      // A6 "Day starts now" (lives in the oob-swapped #tp-daybar — same
      // delegation rule). Re-anchoring mid-show confirms (times shift).
      const dayBtn = e.target.closest('#tp-day-start');
      if (dayBtn) {
        const snap = clockUI?.snap;
        if (snap?.runtime?.dayStartTS) {
          const fmt = fmtTimeOfDay(snap.runtime.dayStartTS);
          if (!confirm(`Day already anchored at ${fmt}. Move the anchor to now (times shift)?`)) return;
        }
        sendCommand('settings', { ts: Date.now() });
        toast('Day anchored to now', 'info');
        return;
      }
      return;
    }
    const action = btn.dataset.cmd;
    // U1: quick adjust never reaches the wire here — it accumulates.
    if (btn.dataset.adjust) {
      queueAdjust(Number(btn.dataset.adjust));
      return;
    }
    // U1: destructive deletes need the two-tap arming first.
    if (action === 'cueDel' && !confirmDelete(btn)) return;
    const args = {};
    if (btn.dataset.pos) args.pos = Number(btn.dataset.pos);
    if (btn.dataset.start !== undefined) args.start = btn.dataset.start !== 'false';
    if (btn.dataset.id) args.id = Number(btn.dataset.id);
    if (btn.dataset.dir) args.dir = btn.dataset.dir; // cueMove up|down (REVIEW-3 R1)
    if (action === 'rate' && btn.dataset.rate) args.rate = Number(btn.dataset.rate);
    // U1: button-carried message payloads (the WRAP UP! quick button
    // speaks addMsg through the click path, no form submit involved).
    if (btn.dataset.text) args.text = btn.dataset.text;
    if (btn.dataset.color) args.color = btn.dataset.color;
    if (btn.dataset.show !== undefined) args.show = btn.dataset.show !== 'false';
    sendCommand(action, args);
  });

  // E6: duration preset chips fill the quick-add input (operator still
  // names the row and hits Add — chips never create rows by themselves).
  document.addEventListener('click', (e) => {
    const chip = e.target.closest('[data-preset-mss]');
    if (!chip) return;
    const form = chip.closest('form[data-cmd="cueAdd"]');
    const mss = form?.querySelector('input[name="mss"]');
    if (!mss) return;
    mss.value = chip.dataset.presetMss || chip.getAttribute('data-preset-mss') || '';
    form.querySelector('input[name="label"]')?.focus();
  });

  // E3 blackout paint: one body flag drives the .tp-blanked overlay on
  // every page (dashboard + all display variants + board). Called from
  // setSnapshot so server oob swaps can never desync it.
  paintBlanked(false);
  // NOTE: no initBlankToggle call — #tp-blank is handled by the
  // document-delegated click listener above (oob-proof).

  // Forms that mutate via WS commands: addMsg, cueAdd …
  document.addEventListener('submit', async (e) => {
    // B4 scheduled day-start form (oob-swapped #tp-daybar — delegates like
    // everything else up here).
    const dsf = e.target.closest?.('#tp-day-start-form');
    if (dsf) {
      e.preventDefault();
      const hhmm = dsf.querySelector('#tp-day-begins')?.value.trim() || '';
      try {
        const res = await fetch(`/api/shows/${document.body.dataset.show}/daystart`, {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ hhmm }),
        });
        const j = await res.json();
        if (!j.ok) { toast(j.error || 'rejected', 'danger'); return; }
        toast(j.anchored ? `Day anchored to ${hhmm} — saved` : 'Schedule saved (clears automation)', 'info');
      } catch {
        toast('network error', 'danger');
      }
      return;
    }
    const form = e.target.closest('form[data-cmd]');
    if (!form) return;
    e.preventDefault();
    const args = {};
    for (const [k, v] of new FormData(form)) {
      if (typeof v === 'string' && v.trim() !== '') args[k] = v.trim();
    }
    const sub = e.submitter;
    if (sub?.dataset) {
      if (sub.dataset.text) args.text = sub.dataset.text;
      if (sub.dataset.color) args.color = sub.dataset.color;
      if (sub.dataset.show !== undefined) args.show = sub.dataset.show !== 'false';
    }
    if (args.mss !== undefined) { // "30"|"1:30"|"30s" → durationMS
      const ms = parseDur(args.mss);
      if (ms == null || ms < 0) { toast('Duration must be like 30, 1:30 or 30s', 'danger'); return; }
      args.durationMS = ms;
      delete args.mss;
    }
    const action = form.dataset.cmd;
    sendCommand(action, args);
    if (action !== 'cueEdit') form.reset();
  });

  const rate = $('#tp-rate');
  if (rate) {
    rate.title = 'Drag to set rate · double-click to reset ×1.00';
  }
}

/* --- rate control: delegated (oob-proof) ---------------------------------
   #tp-rate* live inside the oob-swapped #tp-now — the old direct
   listeners died with the first re-render, so the slider only worked
   until the first transport action. Document-level delegation survives. */
let ratePending = null;
const ratePush = debounce(() => {
  if (ratePending != null) sendCommand('rate', { rate: ratePending });
  ratePending = null;
}, 250);

function initRateDelegation() {
  if (initRateDelegation.bound) return;
  initRateDelegation.bound = true;
  // Capture the value AT INPUT TIME: paint() re-syncs the slider from the
  // runtime every frame when it isn't focused, so reading rate.value
  // inside the debounced callback could ship a stale ×1.00 instead of the
  // dragged value (caught live by the double-click-reset drill).
  document.addEventListener('input', (e) => {
    const slider = e.target.closest?.('#tp-rate');
    if (!slider) return;
    ratePending = Number(slider.value) / 100;
    ratePush();
  });
  document.addEventListener('dblclick', (e) => {
    if (e.target.closest?.('#tp-rate')) { resetRate(); return; }
    if (e.target.closest?.('#tp-rate-out')) beginRateEdit();
  });
  let lastTap = 0;
  document.addEventListener('pointerdown', (e) => {
    if (e.pointerType !== 'touch' || !e.target.closest?.('#tp-rate-out')) return;
    const now = Date.now();
    if (now - lastTap < 450) beginRateEdit();
    lastTap = now;
  });
}

/** "30" (minutes) | "30s" (seconds) | "1:30" (h:mm) | "1:00:05" (h:mm:ss)
    → milliseconds, or null. Mirrors Go views.ParseDuration (owner
    decision 2026-10-05: bare = minutes, two-part = hours:minutes). */
function parseDur(text) {
  const s = String(text).trim();
  const secm = /^(\d+)[sS]$/.exec(s);
  if (secm) return Number(secm[1]) * 1000;
  if (/^-?\d+$/.test(s)) return Number(s) * 60000;
  const parts = s.split(':').map(Number);
  if (parts.some(n => !Number.isFinite(n) || n < 0)) return null;
  if (parts.length === 2) return (parts[0] * 60 + parts[1]) * 60000;
  if (parts.length === 3) return ((parts[0] * 60 + parts[1]) * 60 + parts[2]) * 1000;
  return null;
}

/* ------------------------------------------------------------ keyboard -- */

function performUndo() {
  const msg = undo.perform();
  if (msg) toast(msg, 'info');
  else toast('Nothing to undo', 'info');
}

/** B3: the UNDO button appears only while the ledger has effects; rides
    every snapshot repaint and every capture. */
function updateUndoButton() {
  const btn = $('#tp-undo');
  if (btn) btn.style.display = undo.depth() > 0 ? '' : 'none';
}

function initUndoButton() {
  document.addEventListener('click', (e) => {
    if (e.target.closest?.('[data-undo]')) {
      e.preventDefault();
      performUndo();
    }
  });
  updateUndoButton();
}

/** U1: toggle the operator key-map popover (same panel the hint opens). */
function toggleKbdHelp() {
  const el = $('#tp-kbd-help');
  if (!el) return;
  if (typeof el.togglePopover === 'function') {
    try { el.togglePopover(); } catch { /* display lock */ }
  }
}

function initKeyboard() {
  document.addEventListener('keydown', (e) => {
    const t = e.target;
    // Let focused interactive elements keep native Space/Enter activation and
    // never hijack typing fields (REVIEW-2 O6): a11y + operator safety.
    if (t && (t.matches?.('input, textarea, select, [contenteditable="true"], a, button, [role="button"], summary') || t.isContentEditable)) return;
    // No key repeat — a held Space must not machine-gun GO through the
    // running order (REVIEW-2 O5); operators press it.
    if (e.repeat) return;
    // B3: Ctrl/Cmd+Z = undo the last operator edit (when not typing).
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && (e.key === 'z' || e.key === 'Z')) {
      e.preventDefault();
      performUndo();
      return;
    }
    switch (e.key) {
      case ' ': e.preventDefault(); sendCommand('go'); break;
      case 'ArrowRight': case 'ArrowDown': e.preventDefault(); sendCommand('next'); break;
      case 'ArrowLeft': case 'ArrowUp': e.preventDefault(); sendCommand('prev'); break;
      case 'p': case 'P': e.preventDefault(); sendCommand('pause'); break; // toggles
      case 'r': case 'R': e.preventDefault(); sendCommand('reset'); break;
      case '?': e.preventDefault(); toggleKbdHelp(); break; // open/close the key map
      case '/': // A3: filter the running order
        if (clockUI?.page === 'dashboard') {
          e.preventDefault();
          $('#tp-cue-filter')?.focus();
        }
        break;
      case 'f': case 'F': if (clockUI?.page === 'display') toggleFullscreen(); break;
      default: return;
    }
  });
}

/* --------------------------------------------------------- display page -- */

function toggleFullscreen() {
  if (document.fullscreenElement) document.exitFullscreen();
  else document.documentElement.requestFullscreen?.().catch(() => {});
}

// Display links (data-kiosk) open in a NEW browser WINDOW, not a tab: the
// features string forces a window in all major browsers. The window name
// embeds the show code so repeated clicks reuse the same display window.
// target=_blank stays as the no-JS fallback.
function initKioskLinks() {
  document.addEventListener('click', (e) => {
    const a = e.target?.closest?.('a[data-kiosk]');
    if (!a || !a.href) return;
    e.preventDefault();
    const m = a.href.match(/\/[cd]\/([A-Za-z0-9]+)/);
    const name = 'timerpi-d-' + (m ? m[1].toUpperCase() : 'x');
    window.open(
      a.href, name,
      `width=${screen.availWidth},height=${screen.availHeight},menubar=no,toolbar=no,location=no,status=no,scrollbars=no`
    );
  });
}

function initDisplayExtras() {
  // Stray tap → fullscreen, but ONLY enter: a stagehand poking the TV must
  // not pop it back to windowed mid-show (REVIEW-2 O10; leave via F/Esc/UA).
  document.addEventListener('click', (e) => {
    if (!e.target.closest('button, a, input, select, label, summary') && !document.fullscreenElement) {
      document.documentElement.requestFullscreen?.().catch(() => {});
    }
  });
  // auto-hide cursor
  let hideT;
  const wake = () => {
    document.body.classList.remove('tp-hide-cursor');
    clearTimeout(hideT);
    hideT = setTimeout(() => document.body.classList.add('tp-hide-cursor'), 3000);
  };
  ['mousemove', 'mousedown', 'keydown', 'touchstart'].forEach(ev => document.addEventListener(ev, wake, { passive: true }));
  wake();
}

/* Fullscreen hint chip (UX2): a small corner chip on any fresh browser load —
   "F / tap — fullscreen" — that hides once fullscreen is entered and never
   flickers: its visibility is driven ONLY by fullscreenchange (one source of
   truth, no timers), so it cannot ping-pong. It lives under <body>, outside
   every oob-swapped subtree and outside the theme bundle swap (applyTheme only
   re-points the <link>), so both a stage oob swap and a {"t":"display"} theme
   push leave it intact; CSS positions it per display surface (timerpi.css). */
function initFullscreenHint() {
  let chip = $('#tp-fs-chip');
  if (!chip) {
    chip = document.createElement('span');
    chip.id = 'tp-fs-chip';
    chip.className = 'tp-fs-chip';
    chip.dataset.fs = 'window';
    chip.textContent = 'F / tap — fullscreen';
    document.body.appendChild(chip);
  }
  const sync = () => {
    chip.dataset.fs = document.fullscreenElement ? 'full' : 'window';
  };
  document.addEventListener('fullscreenchange', sync);
  sync();
}

/* ------------------------------------------------------------- homepage -- */

/* ------------------------------------------------------ recent (local) -- */

const RECENT_KEY = 'tp.recent.shows';

function readRecent() {
  try { return JSON.parse(localStorage.getItem(RECENT_KEY)) || []; } catch { return []; }
}

function writeRecent(list) {
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(list.slice(0, 8))); } catch { /* private mode */ }
}

/** Privacy (2026-10-03): the server never serves a code/title list, so the
    "Recent" panel is THIS browser's ledger only. Every dashboard/display
    snapshot recording here is idempotent (dedup by code, newest first). */
function recordRecent(code, title) {
  if (!code) return;
  const list = readRecent().filter((e) => e.code !== code);
  list.unshift({ code, title: (title || '').slice(0, 60), ts: Date.now() });
  writeRecent(list);
}

function renderRecent() {
  const ul = $('#tp-recent-list');
  if (!ul) return;
  const empty = $('#tp-recent-empty');
  const list = readRecent();
  if (empty) empty.hidden = list.length > 0;
  ul.textContent = '';
  list.forEach((e, i) => {
    // textContent-only build (this file's own rule): show titles are
    // server content — an innerHTML template here is XSS from any show
    // whose title contains markup. Codes are filtered to alphanumerics.
    const code = String(e.code || '').replace(/[^A-Za-z0-9]/g, '');
    const li = document.createElement('li');
    const item = document.createElement('div');
    item.className = 'list-item';
    item.style.alignItems = 'center';
    const title = document.createElement('span');
    title.className = 'list-item-title';
    title.textContent = e.title || '(untitled)';
    const meta = document.createElement('span');
    meta.className = 'list-item-meta';
    const codeWrap = document.createElement('span');
    const codeBadge = document.createElement('span');
    codeBadge.className = 'badge mono';
    codeBadge.textContent = code ? fmtCode(code) : '';
    codeWrap.appendChild(codeBadge);
    const cluster = document.createElement('span');
    cluster.className = 'cluster is-gap-xs';
    const open = document.createElement('a');
    open.className = 'badge'; open.href = '/c/' + code; open.textContent = 'Open';
    const disp = document.createElement('a');
    disp.className = 'badge badge-accent'; disp.href = '/d/' + code;
    disp.setAttribute('data-kiosk', ''); disp.textContent = 'Display ↗';
    const forget = document.createElement('button');
    forget.className = 'badge tp-recent-forget';
    forget.title = 'Forget this show on this device';
    forget.setAttribute('aria-label', `Forget ${code}`);
    forget.textContent = '✕';
    forget.addEventListener('click', () => {
      writeRecent(readRecent().filter((_, j) => j !== i));
      renderRecent();
    });
    cluster.append(open, disp, forget);
    meta.append(codeWrap, cluster);
    item.append(title, meta);
    li.appendChild(item);
    ul.appendChild(li);
  });
}

/* ------------------------------------------------------------- show pw -- */

function initShowClone() {
  // E1: duplicate the day — POST clone, then open the new dashboard.
  const form = $('#show-clone-form');
  if (!form || form.dataset.bound) return;
  form.dataset.bound = '1';
  const code = document.body.dataset.show || mesh?.showId || '';
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const title = $('#show-clone-title')?.value || '';
    try {
      const res = await fetch(`/api/shows/${code}/clone`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ title }),
      });
      const out = await res.json();
      if (!res.ok || !out.code) { toast(out.error || 'could not clone the show', 'danger'); return; }
      location.href = `/c/${out.code}`;
    } catch {
      toast('network error', 'danger');
    }
  });
}

/* ------------------------------------------------------------- show pw -- */

function initShowPass() {
  // Extra per-show password (privacy tier 2, routes/showauth.go): the
  // setter's own browser receives the unlock cookie + token immediately.
  const form = $('#show-pass-form');
  if (!form) return;
  const code = document.body.dataset.show || mesh?.showId || '';
  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const pw = $('#show-pass')?.value || '';
    try {
      const res = await fetch(`/api/shows/${code}/passphrase`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ pw }),
      });
      const out = await res.json();
      if (!res.ok) { toast(out.error || 'could not set the password', 'danger'); return; }
      if (out.token) {
        try { localStorage.setItem('tp.show.' + code, out.token); } catch { /* */ }
      } else {
        try { localStorage.removeItem('tp.show.' + code); } catch { /* */ }
      }
      toast(out.enabled
        ? 'Show locked — other devices must enter it once'
        : 'Show password removed', 'info');
      form.reset();
    } catch {
      toast('network error', 'danger');
    }
  });
}

/* ------------------------------------------------------- screens (F1/F2) --
 * The operator's screens panel: every display tab self-registers a stable
 * name (mesh.js screenName); rows list live presence and carry a theme +
 * board assignment each. Actions POST to routes/screens.go; registry
 * changes push back here as {t:"screens"} frames (live count also
 * refreshes on peers churn). Presets snapshot the whole registry and move
 * between appliances as plain JSON files.
 */

const screensCache = { screens: [], themes: [], boards: [] };
let presetsCache = [];

const screensCode = () => document.body.dataset.show || '';
const screensSel = (row, field) => row.querySelector(`[data-field="${field}"]`);

function renderScreens() {
  const host = document.getElementById('tp-screens');
  if (!host) return;
  const badge = document.getElementById('tp-screens-count');
  if (badge) badge.textContent = `${screensCache.screens.filter((s) => s.connected).length} live`;
  host.textContent = '';
  if (!screensCache.screens.length) {
    const p = document.createElement('p');
    p.className = 'text-muted';
    p.textContent = 'No screens yet — open /d/<code> on a display; it registers itself here.';
    host.appendChild(p);
    return;
  }
  for (const s of screensCache.screens) {
    const row = document.createElement('div');
    row.className = 'tp-screen-row';
    row.dataset.name = s.name;

    const name = document.createElement('button');
    name.type = 'button';
    name.className = 'tp-screen-name';
    name.textContent = s.name;
    name.title = 'Click to rename this screen';

    const dot = document.createElement('span');
    dot.className = 'badge' + (s.connected ? ' badge-accent' : '');
    dot.textContent = s.connected ? `LIVE ×${s.sessions}` : 'offline';

    const theme = document.createElement('select');
    theme.className = 'select input-sm';
    theme.dataset.field = 'theme';
    theme.setAttribute('aria-label', `Theme for ${s.name}`);
    theme.appendChild(new Option('Default theme', ''));
    for (const t of screensCache.themes) theme.appendChild(new Option(t, t));
    if (s.theme && !screensCache.themes.includes(s.theme)) {
      theme.appendChild(new Option(`${s.theme} (not installed)`, s.theme));
    }
    theme.value = s.theme || '';
    theme.dataset.stored = theme.value;

    const board = document.createElement('select');
    board.className = 'select input-sm';
    board.dataset.field = 'board';
    board.setAttribute('aria-label', `Board for ${s.name}`);
    board.appendChild(new Option('Show board', ''));
    for (const b of screensCache.boards) board.appendChild(new Option(b.name, String(b.id)));
    if (s.boardId > 0 && !screensCache.boards.some((b) => String(b.id) === String(s.boardId))) {
      board.appendChild(new Option(`board ${s.boardId} (deleted?)`, String(s.boardId)));
    }
    board.value = s.boardId ? String(s.boardId) : '';
    board.dataset.stored = board.value;

    const mk = (act, label, cls, title) => {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = cls;
      btn.dataset.act = act;
      btn.textContent = label;
      btn.title = title;
      return btn;
    };
    row.append(name, dot, theme, board,
      mk('apply', 'Apply', 'btn btn-sm btn-primary', 'Save this theme/board for this screen'),
      mk('match', 'Match all', 'btn btn-sm', "Copy this screen's config onto every screen"));
    for (const pr of (s.peers || [])) {
      const kick = mk('kick', '⏻', 'btn btn-sm btn-icon btn-ghost',
        `Disconnect ${pr.role} session ${pr.peerId} (stays down until that page reloads)`);
      kick.dataset.kick = pr.peerId;
      row.appendChild(kick);
    }
    row.appendChild(mk('forget', '✕', 'btn btn-sm btn-icon btn-ghost', 'Forget this screen'));
    host.appendChild(row);
  }
}

function renderPresets() {
  const host = document.getElementById('tp-presets');
  if (!host) return;
  host.textContent = '';
  if (!presetsCache.length) {
    const p = document.createElement('p');
    p.className = 'text-muted';
    p.textContent = 'No presets saved yet.';
    host.appendChild(p);
    return;
  }
  for (const pr of presetsCache) {
    const row = document.createElement('div');
    row.className = 'tp-screen-row';
    row.dataset.pid = String(pr.id);
    const name = document.createElement('span');
    name.textContent = pr.name;
    const mk = (pact, label, cls) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = cls;
      b.dataset.pact = pact;
      b.textContent = label;
      return b;
    };
    row.append(name,
      mk('apply', 'Apply', 'btn btn-sm btn-primary'),
      mk('export', 'Export', 'btn btn-sm'),
      mk('del', '✕', 'btn btn-sm btn-icon btn-ghost'));
    host.appendChild(row);
  }
}

async function screensRefresh() {
  if (!document.getElementById('screens-panel')) return;
  try {
    const j = await (await fetch(`/api/shows/${screensCode()}/screens`)).json();
    screensCache.screens = j.screens || [];
    renderScreens();
  } catch { /* stale panel beats broken; next push heals it */ }
}

async function presetsRefresh() {
  try {
    const j = await (await fetch(`/api/shows/${screensCode()}/presets`)).json();
    presetsCache = j.presets || [];
    renderPresets();
  } catch { /* keep last list */ }
}

const screensRefreshSoon = debounce(screensRefresh, 400);

// Selects restore missing stored values as synthetic options, so a
// mismatched list can never POST an accidental clear — but a failed boot
// fetch (empty lists, unrendered rows) falls back to the dataset copy.
function screenRowConfig(row) {
  const themeSel = screensSel(row, 'theme');
  const boardSel = screensSel(row, 'board');
  return {
    name: row.dataset.name,
    theme: themeSel?.value || themeSel?.dataset.stored || '',
    boardId: Number(boardSel?.value || boardSel?.dataset.stored || 0),
  };
}

async function screensPost(action, body) {
  try {
    const res = await fetch(`/api/shows/${screensCode()}/screens/${action}`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(body),
    });
    const j = await res.json().catch(() => ({}));
    if (!res.ok) { toast(j.error || `screen ${action} failed`, 'danger'); return false; }
    return true;
  } catch {
    toast('network error', 'danger');
    return false;
  }
}

function initScreens() {
  const panel = document.getElementById('screens-panel');
  if (!panel || initScreens.bound) return;
  initScreens.bound = true;

  (async () => {
    // Each fetch degrades on its own: a dead /api/theme must not stop the
    // screens list (rows still render; missing options are restored as
    // synthetic entries by renderScreens).
    const [t, b] = await Promise.all([
      fetch('/api/theme').then((r) => r.json()).catch(() => null),
      fetch(`/api/shows/${screensCode()}/boards`).then((r) => r.json()).catch(() => null),
    ]);
    if (t) screensCache.themes = t.themes || [];
    if (b) screensCache.boards = Array.isArray(b) ? b : (b.boards || []);
    screensRefresh();
    presetsRefresh();
  })();

  $('#tp-screens')?.addEventListener('click', async (e) => {
    const row = e.target.closest('.tp-screen-row');
    if (!row) return;
    const name = row.dataset.name;
    if (e.target.closest('.tp-screen-name')) {
      const res = await tpPrompt(null, name, { title: `Rename screen "${name}"`, ok: 'Rename', fields: [{ id: 'to', label: 'New name', value: name }] });
      const to = res?.to;
      if (to && to.trim() && to !== name) await screensPost('rename', { from: name, to: to.trim() });
      return;
    }
    const kickBtn = e.target.closest('[data-kick]');
    if (kickBtn) {
      if (!(await tpConfirm('That display stays down until its page is reloaded.', { title: `Disconnect session ${kickBtn.dataset.kick}?`, ok: 'Disconnect', danger: true }))) return;
      try {
        const res = await fetch(`/api/shows/${screensCode()}/sessions/${encodeURIComponent(kickBtn.dataset.kick)}`, { method: 'DELETE' });
        const j = await res.json().catch(() => ({}));
        toast(res.ok ? 'Session disconnected' : (j.error || 'disconnect failed'), res.ok ? 'info' : 'danger');
        if (res.ok) screensRefresh();
      } catch { toast('network error', 'danger'); }
      return;
    }
    const act = e.target.closest('[data-act]')?.dataset.act;
    if (!act) return;
    if (act === 'apply') {
      await screensPost('config', screenRowConfig(row));
    } else if (act === 'match') {
      // Persist this row's picks first, then copy them onto everyone.
      const ok = await screensPost('config', screenRowConfig(row));
      if (ok) await screensPost('match', { from: name });
    } else if (act === 'forget') {
      if (await tpConfirm('An open display tab re-registers on its next join.', { title: `Forget screen "${name}"?`, ok: 'Forget', danger: true })) {
        await screensPost('forget', { name });
      }
    }
  });

  $('#tp-presets')?.addEventListener('click', async (e) => {
    const row = e.target.closest('.tp-screen-row');
    const pact = e.target.closest('[data-pact]')?.dataset.pact;
    if (!row || !pact) return;
    const pid = row.dataset.pid;
    if (pact === 'apply') {
      try {
        const res = await fetch(`/api/shows/${screensCode()}/presets/${pid}/apply`, { method: 'POST' });
        const j = await res.json().catch(() => ({}));
        toast(res.ok ? 'Preset applied to all named screens' : (j.error || 'apply failed'), res.ok ? 'info' : 'danger');
      } catch { toast('network error', 'danger'); }
    } else if (pact === 'export') {
      location.href = `/api/shows/${screensCode()}/presets/${pid}/export`;
    } else if (pact === 'del') {
      if (!(await tpConfirm('The preset is removed for every operator of this show.', { title: 'Delete this preset?', ok: 'Delete', danger: true }))) return;
      try {
        await fetch(`/api/shows/${screensCode()}/presets/${pid}`, { method: 'DELETE' });
        presetsRefresh();
      } catch { toast('network error', 'danger'); }
    }
  });

  $('#tp-preset-save')?.addEventListener('submit', async (e) => {
    e.preventDefault();
    const name = e.target.querySelector('input[name="name"]')?.value.trim();
    if (!name) return;
    try {
      const res = await fetch(`/api/shows/${screensCode()}/presets`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      const j = await res.json().catch(() => ({}));
      if (!res.ok) { toast(j.error || 'save failed', 'danger'); return; }
      toast(`Preset "${name}" saved`, 'info');
      e.target.reset();
      presetsRefresh();
    } catch { toast('network error', 'danger'); }
  });

  $('#tp-preset-import')?.addEventListener('change', async (e) => {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    try {
      const text = await file.text();
      JSON.parse(text);
      const res = await fetch(`/api/shows/${screensCode()}/presets/import`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: text,
      });
      const j = await res.json().catch(() => ({}));
      toast(res.ok ? `Preset "${j.name}" imported` : (j.error || 'import failed'), res.ok ? 'info' : 'danger');
      if (res.ok) presetsRefresh();
    } catch {
      toast('not a readable JSON preset', 'danger');
    }
  });
}

/* ------------------------------------------------------- screens gallery --
 * GET /screens/:ident — a live preview card per registered screen (layout
 * map + current values, 4 s poll), the waiting room with Capture/Dismiss,
 * and a maximised layout-editor modal (iframe of the board compose view in
 * preview mode: it edits WITHOUT self-registering a phantom screen).
 * textContent-only everywhere (XSS rule); ftl-themes component classes.
 */
const gal = { screens: [], waiting: [], boards: [], themes: [] };

function galEl(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = String(text);
  return e;
}

function renderGallery() {
  const host = document.getElementById('tp-gal');
  if (!host) return;
  const liveN = gal.screens.filter((s) => s.connected).length;
  const count = document.getElementById('tp-gal-count');
  if (count) count.textContent = `${gal.screens.length} screens · ${liveN} live`;
  host.textContent = '';
  if (!gal.screens.length) {
    host.appendChild(galEl('p', 'text-muted', 'No screens yet — open /d/<code> on a display; it registers itself here.'));
  }
  gal.screens.forEach((s, idx) => {
    const card = galEl('div', 'tp-gal-card');
    card.dataset.idx = String(idx);
    const head = galEl('div', 'tp-gal-head');
    head.append(galEl('strong', null, s.name),
      galEl('span', 'badge' + (s.connected ? ' badge-accent' : ''), s.connected ? `LIVE ×${s.sessions}` : 'offline'));
    if (s.theme) head.appendChild(galEl('span', 'badge', s.theme));
    card.appendChild(head);

    const prev = galEl('div', 'tp-gal-prev');
    for (const w of (s.widgets || [])) {
      const box = galEl('span', 'tp-gal-w', w.type);
      box.style.left = `${(w.x / 12) * 100}%`;
      box.style.width = `${(w.w / 12) * 100}%`;
      const y = Math.min(w.y, 11);
      box.style.top = `${(y / 12) * 100}%`;
      box.style.height = `${(Math.min(w.h, 12 - y) / 12) * 100}%`;
      prev.appendChild(box);
    }
    if (!(s.widgets || []).length) prev.appendChild(galEl('span', 'tp-gal-empty', 'stage (no board layout)'));
    card.appendChild(prev);

    const info = galEl('div', 'tp-gal-info');
    info.append(galEl('span', 'mono', s.previewClock || '—'),
      galEl('span', 'tp-gal-label', s.previewLabel || 'no cue'));
    card.appendChild(info);
    const bar = galEl('div', 'tp-gal-pct');
    const fill = document.createElement('i');
    fill.style.width = `${s.previewPct || 0}%`;
    bar.appendChild(fill);
    card.appendChild(bar);

    const ctr = galEl('div', 'tp-gal-ctr cluster is-gap-2xs');
    const themeSel = document.createElement('select');
    themeSel.className = 'select input-sm';
    themeSel.dataset.field = 'theme';
    themeSel.setAttribute('aria-label', `Theme for ${s.name}`);
    themeSel.appendChild(new Option('Default theme', ''));
    for (const t of gal.themes) themeSel.appendChild(new Option(t, t));
    if (s.theme && !gal.themes.includes(s.theme)) themeSel.appendChild(new Option(`${s.theme} (not installed)`, s.theme));
    themeSel.value = s.theme || '';
    themeSel.dataset.stored = themeSel.value;
    const boardSel = document.createElement('select');
    boardSel.className = 'select input-sm';
    boardSel.dataset.field = 'board';
    boardSel.setAttribute('aria-label', `Board for ${s.name}`);
    boardSel.appendChild(new Option('Show board', ''));
    for (const b of gal.boards) boardSel.appendChild(new Option(b.name, String(b.id)));
    if (s.boardId > 0 && !gal.boards.some((b) => String(b.id) === String(s.boardId))) {
      boardSel.appendChild(new Option(`board ${s.boardId} (deleted?)`, String(s.boardId)));
    }
    boardSel.value = s.boardId ? String(s.boardId) : '';
    boardSel.dataset.stored = boardSel.value;
    const mkBtn = (act, label, cls, title) => {
      const b = galEl('button', cls, label);
      b.type = 'button';
      b.dataset.act = act;
      if (title) b.title = title;
      return b;
    };
    ctr.append(themeSel, boardSel,
      mkBtn('apply', 'Apply', 'btn btn-sm btn-primary', 'Save theme/board for this screen'),
      mkBtn('edit', 'Edit layout', 'btn btn-sm', 'Open the maximised layout editor'));
    for (const pr of (s.peers || [])) {
      const kick = mkBtn('kick', '⏻', 'btn btn-sm btn-icon btn-ghost',
        `Disconnect ${pr.role} session ${pr.peerId} (stays down until reloaded)`);
      delete kick.dataset.act;
      kick.dataset.kick = pr.peerId;
      ctr.appendChild(kick);
    }
    ctr.appendChild(mkBtn('forget', '✕', 'btn btn-sm btn-icon btn-ghost', 'Forget this screen'));
    card.appendChild(ctr);
    host.appendChild(card);
  });
}

function renderWaiting() {
  const host = document.getElementById('tp-waiting-list');
  if (!host) return;
  const count = document.getElementById('tp-waiting-count');
  if (count) count.textContent = String(gal.waiting.length);
  host.textContent = '';
  if (!gal.waiting.length) {
    host.appendChild(galEl('p', 'text-muted', 'None — when a show is deleted its displays land here, then you can capture them into a live show.'));
    return;
  }
  for (const w of gal.waiting) {
    const row = galEl('div', 'tp-wait-row');
    row.append(galEl('strong', null, w.name),
      galEl('span', 'mono text-muted', w.host),
      galEl('span', 'badge', `seen ${w.seenAgo}`));
    const cap = galEl('button', 'btn btn-sm btn-primary', 'Capture to this show');
    cap.type = 'button';
    cap.dataset.wact = 'capture';
    cap.dataset.wid = String(w.id);
    const dis = galEl('button', 'btn btn-sm btn-ghost', 'Dismiss');
    dis.type = 'button';
    dis.dataset.wact = 'dismiss';
    dis.dataset.wid = String(w.id);
    row.append(cap, dis);
    host.appendChild(row);
  }
}

async function galPost(path, opts) {
  try {
    const res = await fetch(path, opts);
    const j = await res.json().catch(() => ({}));
    if (!res.ok) toast(j.error || `request failed (${res.status})`, 'danger');
    return j;
  } catch {
    toast('network error', 'danger');
    return {};
  }
}

function initGallery() {
  if (initGallery.bound) return;
  initGallery.bound = true;
  const code = document.body.dataset.show || '';
  const dlg = document.getElementById('tp-screen-edit');
  const frame = document.getElementById('tp-screen-edit-frame');
  document.getElementById('tp-screen-edit-close')?.addEventListener('click', () => dlg?.close());
  dlg?.addEventListener('close', () => { if (frame) frame.src = 'about:blank'; });

  const pull = async () => {
    const [s, w, b, t] = await Promise.all([
      fetch(`/api/shows/${code}/screens`).then((r) => r.json()).catch(() => null),
      fetch('/api/waiting').then((r) => r.json()).catch(() => null),
      fetch(`/api/shows/${code}/boards`).then((r) => r.json()).catch(() => null),
      fetch('/api/theme').then((r) => r.json()).catch(() => null),
    ]);
    if (s) gal.screens = s.screens || [];
    if (w) gal.waiting = w.waiting || [];
    if (b) gal.boards = Array.isArray(b) ? b : (b.boards || []);
    if (t) gal.themes = t.themes || [];
    renderGallery();
    renderWaiting();
  };
  pull();
  setInterval(pull, 4000);

  document.getElementById('tp-gal')?.addEventListener('click', async (e) => {
    const card = e.target.closest('.tp-gal-card');
    if (!card) return;
    const s = gal.screens[Number(card.dataset.idx)];
    if (!s) return;
    const kick = e.target.closest('[data-kick]');
    if (kick) {
      if (!(await tpConfirm('That display stays down until its page is reloaded.', { title: `Disconnect session ${kick.dataset.kick}?`, ok: 'Disconnect', danger: true }))) return;
      await galPost(`/api/shows/${code}/sessions/${encodeURIComponent(kick.dataset.kick)}`, { method: 'DELETE' });
      pull();
      return;
    }
    const act = e.target.closest('[data-act]')?.dataset.act;
    if (act === 'apply') {
      const theme = card.querySelector('[data-field="theme"]');
      const board = card.querySelector('[data-field="board"]');
      const r = await galPost(`/api/shows/${code}/screens/config`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          name: s.name,
          theme: theme?.value || theme?.dataset.stored || '',
          boardId: Number(board?.value || board?.dataset.stored || 0),
        }),
      });
      if (r.ok) toast('Applied to screen', 'success');
    } else if (act === 'edit') {
      if (frame) frame.src = `/d/${code}?view=board&edit=1&preview=1${s.boardId ? `&board=${s.boardId}` : ''}`;
      const title = document.getElementById('tp-screen-edit-title');
      if (title) title.textContent = `Layout editor — ${s.name}`;
      if (typeof dlg?.showModal === 'function') dlg.showModal();
      else dlg?.setAttribute('open', '');
    } else if (act === 'forget') {
      if (!(await tpConfirm('An open display re-registers on its next join.', { title: `Forget screen "${s.name}"?`, ok: 'Forget', danger: true }))) return;
      await galPost(`/api/shows/${code}/screens/forget`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ name: s.name }),
      });
      pull();
    }
  });

  // PLAN §11.2: Capture opens a modal — Name / Theme / Location / Layout —
  // and the captured display leaves the waiting list immediately.
  const capDlg = document.getElementById('tp-capture');
  let capWid = 0;
  const capNextName = () => {
    const used = new Set(gal.screens.map((s) => s.name));
    let n = 1;
    while (used.has(`Screen ${n}`)) n++;
    return `Screen ${n}`;
  };
  const capFill = (w) => {
    document.getElementById('tp-capture-host').textContent = w ? w.host : '';
    document.getElementById('tp-capture-name').value = capNextName();
    const themeSel = document.getElementById('tp-capture-theme');
    themeSel.textContent = '';
    themeSel.appendChild(galEl('option', null, 'Operator default')).value = '';
    for (const t of gal.themes) {
      const o = themeSel.appendChild(galEl('option', null, t.label + (t.scheme === 'light' ? ' light' : '')));
      o.value = t.dataTheme;
    }
    themeSel.value = '';
    document.getElementById('tp-capture-room').value = '';
    const boardSel = document.getElementById('tp-capture-board');
    boardSel.textContent = '';
    boardSel.appendChild(galEl('option', null, 'Show default')).value = '0';
    for (const b of gal.boards) {
      const o = boardSel.appendChild(galEl('option', null, b.name || `Board ${b.id}`));
      o.value = String(b.id);
    }
    boardSel.value = '0';
  };
  document.getElementById('tp-capture-cancel')?.addEventListener('click', () => capDlg?.close());
  document.getElementById('tp-capture-go')?.addEventListener('click', async () => {
    const body = JSON.stringify({
      code,
      name: document.getElementById('tp-capture-name').value,
      theme: document.getElementById('tp-capture-theme').value,
      room: document.getElementById('tp-capture-room').value,
      boardId: Number(document.getElementById('tp-capture-board').value || 0),
    });
    const r = await galPost(`/api/waiting/${capWid}/capture`, {
      method: 'POST', headers: { 'content-type': 'application/json' }, body,
    });
    if (r.ok) {
      toast(`Captured as "${r.name}" — it joins within seconds`, 'success');
      capDlg?.close();
      pull();
      // The display polls every 2 s and then joins; front-load refreshes so
      // the new screen shows up here the moment it lands.
      for (const delay of [1000, 2000, 3500, 5000]) setTimeout(pull, delay);
    }
  });

  document.getElementById('tp-waiting-list')?.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-wact]');
    if (!btn) return;
    if (btn.dataset.wact === 'capture') {
      capWid = Number(btn.dataset.wid);
      capFill(gal.waiting.find((w) => w.id === capWid));
      if (typeof capDlg?.showModal === 'function') capDlg.showModal();
      else capDlg?.setAttribute('open', '');
      return; // the modal drives the rest
    } else if (btn.dataset.wact === 'dismiss') {
      await galPost(`/api/waiting/${btn.dataset.wid}`, { method: 'DELETE' });
    }
    pull();
  });
}

/* -------------------------------------------------- inline rate editor -- */

function resetRate() {
  const slider = $('#tp-rate');
  if (!slider) return;
  slider.value = '100';
  sendCommand('rate', { rate: 1 });
}

function beginRateEdit() {
  const out = $('#tp-rate-out');
  if (!out || clockUI?._rateEditing) return;
  clockUI._rateEditing = true;
  const editor = document.createElement('input');
  editor.type = 'number';
  editor.inputMode = 'decimal';
  editor.min = '0.5'; editor.max = '2'; editor.step = '0.05';
  editor.className = 'input readout-sm mono tp-rate-editor';
  editor.style.width = '4.2rem';
  editor.value = (clockUI.snap?.runtime?.rate ?? 1).toFixed(2);
  editor.setAttribute('aria-label', 'Rate multiplier');
  out.textContent = '';
  out.appendChild(editor);
  editor.focus();
  editor.select();

  let ended = false;
  const done = (commit) => {
    if (ended) return;
    ended = true;
    if (!commit) { clockUI._rateEditing = false; editor.remove(); return; }
    let v = parseFloat(editor.value);
    if (Number.isFinite(v)) v = Math.min(2, Math.max(0.5, v));
    clockUI._rateEditing = false;
    editor.remove();
    if (Number.isFinite(v)) sendCommand('rate', { rate: v });
  };
  editor.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); done(true); }
    if (e.key === 'Escape') { e.preventDefault(); done(false); }
  });
  editor.addEventListener('blur', () => done(true));
  // A stray second dblclick must not commit half-typed values.
  editor.addEventListener('dblclick', (e) => e.stopPropagation());
}

function initRateExtras() {
  // Everything rate is document-delegated (see initRateDelegation): the
  // controls live in the oob-swapped #tp-now and direct listeners die
  // with the first re-render. This only sets first-paint affordances.
  const rate = $('#tp-rate');
  if (rate) rate.title = 'Drag to set · double-click resets ×1.00';
  const out = $('#tp-rate-out');
  if (out) out.title = 'Double-click (or double-tap) to type a rate';
}

/* ------------------------------------------------ inline cue cell edit -- */

/** A3/inline-edit: cuelist oob swaps are deferred while a cell editor is
    open (a live edit on another device would otherwise rip the input out
    of the DOM mid-typing and lose the operator's text). Latest wins; the
    finish() path replays it. */
let pendingCuelistSwap = null;

/** Replay the cuelist swap deferred while a cell editor was open, then
    keep row state current (filter/classes) on the fresh rows. */
function flushPendingSwap() {
  if (!pendingCuelistSwap) return;
  const m = pendingCuelistSwap;
  pendingCuelistSwap = null;
  applyOOB(m);
  clockUI?.renderRows();
}

function applyOOB(m) {
  if (m.target === '#cuelist' && document.querySelector('#cuelist tbody input')) {
    pendingCuelistSwap = m;
    return;
  }
  const target = m.target && $(m.target);
  if (!target) return;
  const tpl = document.createElement('template');
  tpl.innerHTML = m.html;
  const frag = tpl.content.firstElementChild;
  if (frag) {
    target.replaceWith(frag);
    window.htmx?.process(frag);
    // The swap replaced #tp-now / #tp-daybar / #cuelist — every cached
    // reference inside them is detached. Re-collect so paint/render
    // keep writing into live nodes (REVIEW-3 R3).
    clockUI?._collect();
  }
}

function initInlineEdit() {
  // A-requests: every editable table cell (cue label, speaker, duration)
  // edits inline — dblclick with a mouse, two taps with touch. Computed
  // cells (# / start / end) and the button column are deliberately NOT
  // editable (server-computed truth).
  document.addEventListener('dblclick', (e) => {
    const td = e.target.closest?.('#cuelist tbody td');
    if (td) startCellEdit(td);
  });
  let last = { t: 0, td: null };
  document.addEventListener('pointerdown', (e) => {
    if (e.pointerType !== 'touch') return;
    const td = e.target.closest?.('#cuelist tbody td');
    if (!td) { last = { t: 0, td: null }; return; }
    const now = Date.now();
    if (td === last.td && now - last.t < 450) {
      startCellEdit(td);
      last = { t: 0, td: null };
    } else {
      last = { t: now, td };
    }
  });
}

function startCellEdit(td) {
  if (!td || td.querySelector('input')) return;
  const tr = td.closest('tr');
  if (!tr || !clockUI?.snap) return;
  const pos = Number(tr.dataset.pos);
  const cue = clockUI.snap.cues.find((c) => c.pos === pos);
  if (!cue) return;

  let field, value, host = td, text;
  if (td.querySelector('.tp-cue-label')) {
    field = 'label'; value = cue.label || ''; host = td.querySelector('.tp-cue-label'); text = 'Label';
  } else if (td.classList.contains('tp-cue-dur')) {
    field = 'durationMS'; value = cue.durationMS || 0; text = 'Duration (H:MM — 30 = 30 min, 30s = seconds)';
  } else if ([...td.parentElement.children].indexOf(td) === 2) {
    field = 'speaker'; value = cue.speaker || ''; text = 'Speaker';
  } else {
    return; // # / start / end / buttons: not operator-owned
  }

  const oldHTML = host.innerHTML;
  const input = document.createElement('input');
  input.type = 'text';
  input.className = 'input input-sm mono';
  input.style.width = '100%';
  input.setAttribute('aria-label', `${text} for cue ${pos}`);
  input.value = field === 'durationMS'
    ? fmtDurText(value)   // round-trip in the format operators type
    : value;
  host.textContent = '';
  host.appendChild(input);
  input.focus();
  input.select();

  let ended = false;
  const finish = (commit) => {
    if (ended) return;
    ended = true;
    const v = input.value.trim();
    input.blur?.();
    input.remove();
    host.innerHTML = oldHTML; // the next oob repaint (or this) refills truth
    if (!commit) {
      flushPendingSwap();
      return;
    }
    if (field === 'durationMS') {
      const ms = parseDur(v);
      if (ms == null || ms < 0) { toast('That is not a duration — try 5:30', 'danger'); flushPendingSwap(); return; }
      sendCommand('cueEdit', { pos, durationMS: ms });
    } else {
      if (field === 'label' && v === '') { toast('Cue label cannot be empty', 'danger'); flushPendingSwap(); return; }
      sendCommand('cueEdit', { pos, [field]: v });
    }
    // Replay the cuelist swap deferred while the editor was open — make
    // sure the LAST state wins, not a stale one from before our own edit.
    flushPendingSwap();
  };
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); finish(true); }
    if (e.key === 'Escape') { e.preventDefault(); finish(false); }
  });
  input.addEventListener('blur', () => finish(true));
  input.addEventListener('dblclick', (e) => e.stopPropagation());
}

/** milliseconds → "m:ss" / "h:mm:ss" for the duration editor's initial text. */
function fmtDurText(ms) {
  const total = Math.round(ms / 1000);
  const h = Math.floor(total / 3600), m = (total % 3600) / 60 | 0, s = total % 60;
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
    : `${m}:${String(s).padStart(2, '0')}`;
}

/* -------------------------------------------------------- day controls -- */

function initDayStart() {
  // A6/B4 controls live in the oob-swapped #tp-daybar: the "starts now"
  // button is handled by the document-delegated click listener and the
  // scheduled-start form by the delegated submit listener (direct
  // bindings would both die on re-render AND double-fire before it).
}


/* ------------------------------------------------------- audience panel -- */

// initAudiencePanel — the operator's interaction surface (PLAN §11.3
// phase 5): create items, per-item Show / Results / Hide transport wired to
// the hidden→open→results state machine, and the moderation queue for
// audience submissions (approve = open, hide = silent again, delete).
// Client-rendered like the gallery (operator-only page, 3 s poll); every
// mutation fans out via /api BroadcastPoll so boards + phones stay live.
const aud = { items: [] };

function audEl(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = String(text);
  return e;
}

function initAudiencePanel() {
  const host = document.getElementById('tp-aud-items');
  if (!host) return;
  const code = document.body.dataset.show || '';

  aud.pull = async () => {
    try {
      const j = await (await fetch(`/api/shows/${code}/polls`)).json();
      if (j && j.ok) { aud.items = j.polls || []; renderAudiencePanel(); }
    } catch { /* offline — the next poll retries */ }
  };

  const kindSel = document.getElementById('tp-aud-kind');
  const optsRow = document.getElementById('tp-aud-opts-row');
  kindSel?.addEventListener('change', () => {
    optsRow.hidden = !(kindSel.value === 'poll' || kindSel.value === 'quiz');
  });
  document.getElementById('tp-aud-create')?.addEventListener('submit', async (e) => {
    e.preventDefault();
    const kind = kindSel.value;
    const question = document.getElementById('tp-aud-question').value.trim();
    const optsRaw = document.getElementById('tp-aud-opts').value.trim();
    const options = optsRaw ? optsRaw.split(',').map((x) => x.trim()).filter(Boolean) : [];
    const r = await fetch(`/api/shows/${code}/polls`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ kind, question, options }),
    });
    const j = await r.json().catch(() => ({}));
    if (r.ok && j.ok) {
      document.getElementById('tp-aud-question').value = '';
      document.getElementById('tp-aud-opts').value = '';
      toast('Created (hidden — Show when ready)', 'success');
      aud.pull();
    } else {
      toast(j.error || 'create failed', 'danger');
    }
  });

  host.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-aud]');
    if (!btn) return;
    const id = Number(btn.dataset.audid);
    const act = btn.dataset.aud;
    if (act === 'del') {
      if (!(await tpConfirm('The item and its votes are removed for everyone.', { title: 'Delete this item?', ok: 'Delete', danger: true }))) return;
      await fetch(`/api/shows/${code}/polls/${id}`, { method: 'DELETE' });
    } else if (act === 'state') {
      await fetch(`/api/shows/${code}/polls/${id}/state`, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ state: btn.dataset.val }),
      });
    }
    aud.pull();
  });

  aud.pull();
  setInterval(aud.pull, 3000);
}

function renderAudiencePanel() {
  const host = document.getElementById('tp-aud-items');
  if (!host) return;
  host.textContent = '';
  const count = document.getElementById('tp-aud-count');
  if (count) count.textContent = String(aud.items.length);

  const top = aud.items.filter((p) => !p.parent);
  const children = aud.items.filter((p) => p.parent);

  if (!top.length) {
    host.appendChild(audEl('p', 'text-muted', 'No items yet — create a poll, Q&A or word cloud above.'));
  }
  for (const p of top) {
    host.appendChild(audItemCard(p));
    // Moderation queue: submitted words/ideas still hidden under this item
    // (moderation by silence — they surface only when approved).
    const subs = children.filter((c) => c.parent === p.id && c.state === 'hidden');
    if (subs.length) {
      const mod = audEl('div', 'tp-aud-mod stack is-gap-2xs');
      mod.appendChild(audEl('div', 'text-muted', `${subs.length} waiting for approval:`));
      for (const c of subs) mod.appendChild(audSubRow(c));
      host.appendChild(mod);
    }
  }
  // Orphan submissions (a question asked while nothing was open).
  for (const c of children) {
    if (c.state === 'hidden' && !top.some((p) => p.id === c.parent)) {
      host.appendChild(audSubRow(c));
    }
  }
}

function audItemCard(p) {
  const card = audEl('div', 'tp-aud-item');
  const head = audEl('div');
  head.style.display = 'flex';
  head.style.alignItems = 'baseline';
  head.style.gap = '.5rem';
  head.appendChild(audEl('strong', null, p.question || `(${p.kind})`));
  head.appendChild(audEl('span', 'badge', p.kind));
  head.appendChild(audEl('span', 'badge' + (p.state === 'open' ? ' badge-accent' : ''), p.state));
  card.appendChild(head);
  if (p.state !== 'hidden') {
    if ((p.kind === 'poll' || p.kind === 'quiz') && p.options?.length) {
      const total = Math.max(1, p.total || 0);
      p.options.forEach((label, i) => {
        const n = (p.counts || [])[i] || 0;
        card.appendChild(audEl('div', 'text-muted', `${label} — ${n} · ${Math.round((n / total) * 100)}%`));
      });
      card.appendChild(audEl('div', 'text-muted', `${p.total || 0} votes`));
    }
    if ((p.kind === 'qa' || p.kind === 'ideas') && p.upvotes) {
      card.appendChild(audEl('div', 'text-muted', `${p.upvotes} likes`));
    }
    if (p.kind === 'wordcloud') {
      const n = aud.items.filter((c) => c.parent === p.id && c.state === 'open').length;
      card.appendChild(audEl('div', 'text-muted', `${n} words on the wall`));
    }
  }
  const verbs = audEl('div', 'tp-aud-verbs');
  const mk = (label, val, primary) => {
    const b = audEl('button', 'btn btn-sm' + (primary ? ' btn-primary' : ''), label);
    b.type = 'button';
    b.dataset.aud = 'state';
    b.dataset.audid = String(p.id);
    b.dataset.val = val;
    if (p.state === val) b.disabled = true;
    return b;
  };
  verbs.append(mk('Show', 'open', true), mk('Results', 'results', false), mk('Hide', 'hidden', false));
  const del = audEl('button', 'btn btn-sm btn-ghost', 'Delete');
  del.type = 'button';
  del.dataset.aud = 'del';
  del.dataset.audid = String(p.id);
  verbs.appendChild(del);
  card.appendChild(verbs);
  return card;
}

// audSubRow — one moderated submission with Approve / Hide / Delete.
function audSubRow(c) {
  const row = audEl('div', 'tp-aud-sub');
  row.appendChild(audEl('span', null, c.question));
  const verbs = audEl('span', 'tp-aud-verbs');
  const approve = audEl('button', 'btn btn-sm btn-primary', 'Approve');
  approve.type = 'button';
  approve.dataset.aud = 'state';
  approve.dataset.audid = String(c.id);
  approve.dataset.val = 'open';
  const hide = audEl('button', 'btn btn-sm', 'Hide');
  hide.type = 'button';
  hide.dataset.aud = 'state';
  hide.dataset.audid = String(c.id);
  hide.dataset.val = 'hidden';
  const del = audEl('button', 'btn btn-sm btn-ghost', 'Delete');
  del.type = 'button';
  del.dataset.aud = 'del';
  del.dataset.audid = String(c.id);
  verbs.append(approve, hide, del);
  row.appendChild(verbs);
  return row;
}

/* ------------------------------------------------------------ day memo -- */

function initDayNotes() {
  // A7: the show's day memo, autosaved verbatim on a debounce. Server-gated
  // like other show content; offline browsers queue nothing (note says so).
  const ta = $('#tp-day-notes'), status = $('#tp-notes-status');
  if (!ta || !clockUI) return;
  let pending = null;
  const push = debounce(() => {
    if (pending == null) return;
    const text = pending; pending = null;
    const code = document.body.dataset.show;
    fetch(`/api/shows/${code}/notes`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ text }),
    }).then((r) => r.json()).then((j) => {
      if (j.ok) { if (status) status.textContent = 'Saved ' + fmtTimeOfDay(Date.now()); }
      else { if (status) status.textContent = j.error || 'Save failed — retried next edit'; }
      pendingFlush();
    }).catch(() => {
      if (status) status.textContent = 'Offline — this note syncs on your next edit';
      pendingFlush();
    });
  }, 800);
  function pendingFlush() {
    if (pending != null) push(); // changed again while saving
  }
  ta.addEventListener('input', () => {
    pending = ta.value;
    if (status) status.textContent = 'Saving…';
    push();
  });
}

/* --------------------------------------------------------- inspector --- */

/** B1: the full cue editor. Opens from the row's pencil (`data-insp`),
    fills every field from the snapshot (the source of truth), and commits
    ONE cueEdit — the server already validates each domain and the oob
    repaint refreshes the row. Colors validate with the same regex the
    server uses (#rgb/#rgba/#rrggbb/#rrgbbaa). */
function initInspector() {
  const dlg = $('#tp-inspector');
  if (!dlg) return;
  let openPos = 0;

  document.addEventListener('click', (e) => {
    const btn = e.target.closest?.('[data-insp]');
    if (!btn) return;
    e.preventDefault();
    openInspector(Number(btn.dataset.insp));
  });

  function field(id) { return dlg.querySelector('#' + id); }

  function fill(cue, label) {
    field('tp-insp-pos').textContent = `${String(cue.pos).padStart(2, '0')} — ${label}`;
    field('tp-insp-label').value = cue.label || '';
    field('tp-insp-speaker').value = cue.speaker || '';
    field('tp-insp-duration').value = fmtDurText(cue.durationMS || 0);
    field('tp-insp-hold').value = cue.holdMS ? fmtDurText(cue.holdMS) : '';
    field('tp-insp-tags').value = cue.tags || '';
    field('tp-insp-kind').value = cue.kind === 'break' ? 'break' : 'session';
    field('tp-insp-timerKind').value = cue.timerKind || 'COUNTDOWN';
    field('tp-insp-endAction').value = cue.endAction || 'HOLD';
    field('tp-insp-autoContinue').checked = !!cue.autoContinue;
    field('tp-insp-alert1').value = cue.alert1MS ? fmtDurText(cue.alert1MS) : '';
    field('tp-insp-alert1Color').value = cue.alertColor1 || '';
    field('tp-insp-alert2').value = cue.alert2MS ? fmtDurText(cue.alert2MS) : '';
    field('tp-insp-alert2Color').value = cue.alertColor2 || '';
    field('tp-insp-color').value = cue.color || '';
    field('tp-insp-startAt').value = cue.startAt || '';
    field('tp-insp-notes').value = cue.notes || '';
    const err = field('tp-insp-error');
    err.hidden = true;
  }

  function openInspector(pos) {
    const snap = clockUI?.snap;
    const cue = snap?.cues.find((c) => c.pos === pos);
    if (!cue) { toast('Cue not in this snapshot yet', 'danger'); return; }
    openPos = pos;
    fill(cue, cue.label || '');
    if (typeof dlg.showModal === 'function') dlg.showModal();
    else dlg.setAttribute('open', '');
    field('tp-insp-label').focus();
  }

  const hexOK = (v) => v === '' || /^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/.test(v);

  function save() {
    const err = field('tp-insp-error');
    const fail = (msg) => { err.textContent = msg; err.hidden = false; };
    const args = { pos: openPos };
    const label = field('tp-insp-label').value.trim();
    if (!label) return fail('Cue label cannot be empty');
    args.label = label;
    args.speaker = field('tp-insp-speaker').value.trim();
    const dur = parseDur(field('tp-insp-duration').value);
    if (dur == null || dur < 0) return fail('Duration is not a time — try 5:30');
    args.durationMS = dur;
    const hold = field('tp-insp-hold').value.trim();
    if (hold) {
      const ms = parseDur(hold);
      if (ms == null || ms < 0) return fail('Hold is not a time — try 1:00');
      args.holdMS = ms;
    } else {
      args.holdMS = 0;
    }
    args.tags = field('tp-insp-tags').value;
    args.kind = field('tp-insp-kind').value;
    args.timerKind = field('tp-insp-timerKind').value;
    args.endAction = field('tp-insp-endAction').value;
    args.autoContinue = field('tp-insp-autoContinue').checked;
    for (const [msKey, colorKey, labelTxt] of [
      ['tp-insp-alert1', 'tp-insp-alert1Color', 'Alert 1'],
      ['tp-insp-alert2', 'tp-insp-alert2Color', 'Alert 2'],
    ]) {
      const txt = field(msKey).value.trim();
      if (txt) {
        const ms = parseDur(txt);
        if (ms == null || ms < 0) return fail(`${labelTxt} is not a time — try 5:00`);
        args[{ 'tp-insp-alert1': 'alert1MS', 'tp-insp-alert2': 'alert2MS' }[msKey]] = ms;
      } else {
        args[{ 'tp-insp-alert1': 'alert1MS', 'tp-insp-alert2': 'alert2MS' }[msKey]] = 0;
      }
      const color = field(colorKey).value.trim();
      if (!hexOK(color)) return fail(`${labelTxt} colour must be a hex value like #7C3AED`);
      args[{ 'tp-insp-alert1Color': 'alertColor1', 'tp-insp-alert2Color': 'alertColor2' }[colorKey]] = color || '';
    }
    const rowColor = field('tp-insp-color').value.trim();
    if (!hexOK(rowColor)) return fail('Row accent must be a hex value like #7C3AED');
    args.color = rowColor || '';
    // E5: wall-clock auto-start. Empty clears; otherwise strict HH:MM, and
    // a time already past today asks first (it would fire at the next idle).
    const startAt = field('tp-insp-startAt').value.trim();
    if (startAt && !/^([01]\d|2[0-3]):[0-5]\d$/.test(startAt)) return fail('Auto-start must be HH:MM (24-hour)');
    if (startAt) {
      const [hh, mm] = startAt.split(':').map(Number);
      const occ = new Date(); occ.setHours(hh, mm, 0, 0);
      if (occ.getTime() <= Date.now() && !window.confirm(`${startAt} already passed today — arm it to fire at the next idle moment?`)) return;
    }
    args.startAt = startAt;
    args.notes = field('tp-insp-notes').value;

    sendCommand('cueEdit', args);
    closeInspector();
    toast('Cue saved', 'info');
  }

  function closeInspector() {
    if (typeof dlg.close === 'function') dlg.close();
    else dlg.removeAttribute('open');
  }

  field('tp-insp-save')?.addEventListener('click', save);
  field('tp-insp-cancel')?.addEventListener('click', closeInspector);
  dlg.querySelector('form')?.addEventListener('submit', (e) => e.preventDefault());
}

/* --------------------------------------------------- B2 drag reorder --- */

function initDragReorder() {
  // The `#` cell is the REAL drag handle (U1 removed the old fake one; this
  // does pointer-drag properly): press+move lifts the row, a live insertion
  // line picks the drop slot, the drop commits ONE full-slot cueMove
  // {pos,to}. Mouse + touch via pointer events; scroll stays free until a
  // drag starts (touch-action:none lives on the handle cell). Listeners are
  // DOCUMENT-level because oob swaps replace the whole tbody — bound-to-
  // tbody handlers would die with the old rows.
  let drag = null;

  const dropSlotFor = (y, tb) => {
    const rows = [...tb.querySelectorAll('tr[data-pos]')];
    for (const tr of rows) {
      const r = tr.getBoundingClientRect();
      if (y < r.top + r.height / 2) return Number(tr.dataset.pos);
    }
    return rows.length + 1;
  };

  const paintLine = (slot) => {
    if (!drag) return;
    drag.canEl?.remove();
    drag.canEl = null;
    const tb = document.querySelector('#cuelist tbody');
    if (!tb) return;
    const wrap = tb.closest('.tp-cuelist-wrap') || tb.parentElement;
    const rows = [...tb.querySelectorAll('tr[data-pos]')];
    if (!wrap || !rows.length) return;
    const line = document.createElement('div');
    line.className = 'tp-drag-line';
    // slot == rows.length+1 means "append at the very end": draw BELOW
    // the last row, not above it (the old code anchored the last row's
    // top for every slot, lying about end-drops).
    const atEnd = slot > rows.length;
    const anchor = rows[Math.min(slot, rows.length) - 1];
    const ar = anchor.getBoundingClientRect();
    const wr = wrap.getBoundingClientRect();
    line.style.top = atEnd ? `${ar.bottom - wr.top + 2}px` : `${ar.top - wr.top - 2}px`;
    line.style.left = '0px';
    line.style.right = '0px';
    wrap.appendChild(line);
    drag.canEl = line;
  };

  const cleanupDrag = () => {
    if (!drag) return;
    document.body.classList.remove('tp-drag-body');
    document.querySelector('#cuelist tbody')?.classList.remove('tp-drag-on');
    drag.row?.classList.remove('tp-dragging');
    drag.canEl?.remove();
    drag = null;
  };

  document.addEventListener('pointerdown', (e) => {
    if (e.button !== undefined && e.button !== 0) return;
    const handle = e.target.closest?.('td.tp-cue-pos');
    const tr = handle && handle.closest('#cuelist tr[data-pos]');
    if (!tr) return;
    drag = {
      pos: Number(tr.dataset.pos),
      from: Number(tr.dataset.pos),
      row: tr,
      startX: e.clientX,
      startY: e.clientY,
      canEl: null,
      active: false,
      pointerId: e.pointerId,
      slot: Number(tr.dataset.pos),
    };
  });

  document.addEventListener('pointermove', (e) => {
    if (!drag || e.pointerId !== drag.pointerId) return;
    if (!drag.active) {
      const dx = Math.abs(e.clientX - drag.startX);
      const dy = Math.abs(e.clientY - drag.startY);
      if (dx < 8 && dy < 8) return;
      drag.active = true;
      drag.row.classList.add('tp-dragging');
      document.querySelector('#cuelist tbody')?.classList.add('tp-drag-on');
      document.body.classList.add('tp-drag-body');
      try { drag.row.setPointerCapture(e.pointerId); } catch { /* synthetic */ }
    }
    e.preventDefault();
    const tb = document.querySelector('#cuelist tbody');
    if (!tb) return;
    // Autoscroll long days: near the visible edges of the scroll wrap,
    // walk the list so slots past the fold stay reachable.
    const wrap = tb.closest('.tp-cuelist-wrap') || tb.parentElement;
    if (wrap) {
      const wr = wrap.getBoundingClientRect();
      const MARGIN = 56, STEP = 12;
      if (e.clientY < wr.top + MARGIN) wrap.scrollTop -= STEP;
      else if (e.clientY > wr.bottom - MARGIN) wrap.scrollTop += STEP;
    }
    drag.slot = dropSlotFor(e.clientY, tb);
    paintLine(drag.slot);
  });

  document.addEventListener('pointerup', (e) => {
    if (!drag || e.pointerId !== drag.pointerId) return;
    const d = drag;
    cleanupDrag();
    if (!d.active) return; // plain click on the numeral
    suppressDragClick = true; // swallow the synthetic click after a real drag
    if (!d.row.isConnected) {
      // An oob swap replaced the tbody mid-drag (concurrent remote edit):
      // from/slot now address a dead DOM — abort loudly, don't mis-move.
      toast('Row changed during the drag — try again', 'info');
      return;
    }
    // Server MoveCue(from,to) lands the row AT `to` in the final order,
    // but the slot scan counts the still-present dragged row: dropping
    // below it overshoots by one, so compensate down-drags (up-drags and
    // appends are already exact).
    let to = Number(d.slot);
    if (to > d.from) to -= 1;
    if (!to || to === d.from) return; // dropped back home
    sendCommand('cueMove', { pos: d.from, to });
  });

  document.addEventListener('pointercancel', cleanupDrag);
  window.addEventListener('blur', cleanupDrag);
}

// Swallows the synthetic click that follows a real drag's pointerup, so a
// drop never also triggers row click affordances (inline edit arms etc.).
let suppressDragClick = false;
document.addEventListener('click', (e) => {
  if (suppressDragClick) {
    suppressDragClick = false;
    e.stopPropagation();
    e.preventDefault();
  }
}, true);

function initHome() {  const form = $('#create-show');
  if (form) {
    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const title = $('#show-title')?.value.trim() || 'Untitled show';
      const btn = form.querySelector('button');
      btn?.classList.add('htmx-request');
      try {
        const res = await fetch('/api/shows', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ title }),
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
        const out = await res.json();
        // Agent L (scope change): the share CODE is the only public
        // address; pre-backfill safety keeps the old numeric fallback.
        location.href = `/c/${out.code || out.id}`;
      } catch (err) {
        const errEl = $('#create-error');
        if (errEl) { errEl.textContent = `Could not create show: ${err}`; errEl.hidden = false; }
        btn?.classList.remove('htmx-request');
      }
    });
  }
  // Agent L: join-by-code box — placeholder XXXX-XXXX; canonicalize client
  // side (dash/space strip, uppercase, typo maps I→1 L→1 O→0 U→V — the
  // server timerpi/gen.go rule set), let the server 404 anything else.
  const join = $('#join-show');
  join?.addEventListener('submit', (e) => {
    e.preventDefault();
    const input = $('#join-code');
    const errEl = $('#join-error');
    let code = (input?.value || '').toUpperCase().replace(/[^0-9A-Z]/g, '')
      .replace(/I/g, '1').replace(/L/g, '1').replace(/O/g, '0').replace(/U/g, 'V');
    input.value = code.length === 8 ? `${code.slice(0, 4)}-${code.slice(4)}` : code;
    if (code.length !== 8) {
      if (errEl) {
        errEl.textContent = 'Session codes are 8 characters, like K7QP-M3XB.';
        errEl.hidden = false;
      }
      input?.focus();
      return;
    }
    location.href = `/c/${code}`;
  });
}

/* ------------------------------------------------------ import dropzone -- */

/**
 * U1: the whole Import panel is a drop target — drop a file and it fills
 * #import-file, exactly like picking it by hand. No route/protocol
 * change: the form still posts through htmx when the operator presses
 * Import (or presses Enter).
 */
function initImportDrop() {
  const zone = $('.tp-import');
  const input = $('#import-file');
  if (!zone || !input) return;
  const over = (e) => {
    if (e.dataTransfer?.types?.includes('Files')) e.preventDefault();
    zone.classList.add('is-dropping');
  };
  const leave = () => { zone.classList.remove('is-dropping'); };
  zone.addEventListener('dragover', over);
  zone.addEventListener('dragenter', over);
  zone.addEventListener('dragleave', leave);
  zone.addEventListener('drop', (e) => {
    e.preventDefault();
    leave();
    const file = e.dataTransfer?.files?.[0];
    if (!file) return;
    if (!/\.(xlsx|xls|csv|json)$/i.test(file.name)) {
      toast('Drop an .xlsx / .xls / .csv / .json file', 'danger');
      return;
    }
    try {
      const dt = new DataTransfer();
      dt.items.add(file);
      input.files = dt.files;
    } catch { /* no DataTransfer: the picker still works */ }
    toast(`“${file.name}” attached — check Format and Mode, then Import`, 'info');
  });
}

/* ----------------------------------------------------------------- boot -- */

document.addEventListener('DOMContentLoaded', () => {
  const body = document.body;
  const page = body.dataset.page || 'home';
  const showId = body.dataset.show || '';
  const role = body.dataset.role || 'controls';

  // Safety net: if htmx ever performs an oob swap itself (fragments shipped
  // with hx-swap-oob), the swapped-in nodes must be re-reached too — and
  // the A3 filter must re-apply to the fresh rows immediately.
  document.addEventListener('htmx:oobAfterSwap', () => {
    clockUI?._collect();
    clockUI?.renderRows();
    // Fresh <use> nodes carry template-default sprite paths — retarget them
    // to the ACTIVE theme's icon bundle (B7-default correctness).
    applyIconTheme(document.documentElement.getAttribute('data-theme') || 'blue-future');
  });

  initTheme();
  initClientLog();
  initCommandButtons();
  initKeyboard();
  initSharePanel();
  initKioskLinks();
  initImportDrop();

  if (page === 'home') { initHome(); renderRecent(); }
  if (page === 'screens') initGallery();
  // C2 (2026-10-04): BOARD pages join the mesh via board.js — they ship
  // their own display-role client with full snapshot adoption. Booting
  // timerpi.js's mesh too meant TWO WS sessions per screen (double join,
  // second election, peer-count inflation).
  const isBoard = page === 'display' && body.dataset.view === 'board';
  if (page === 'dashboard' || page === 'display') {
    try { window.__tpmesh = mesh; } catch { /* */ }
    // The paint loop belongs to the mesh client: board pages are owned by
    // board.js (its own tick), and booting ours meant TWO joins per screen.
    // clockUI is only constructed inside initMesh — never touch it here.
    if (!isBoard) {
      initMesh(showId, page === 'display' ? 'display' : 'controls', page);
      clockUI.start();
    }
  }
  if (page === 'dashboard') { initCueFilter(); initInlineEdit(); initRateExtras(); initRateDelegation(); initShowPass(); initShowClone(); initScreens(); initDayStart(); initDayNotes(); initAudiencePanel(); initInspector(); initUndoButton(); initDragReorder(); }
  if (page === 'display') initDisplayExtras();
  if (page === 'display') initFullscreenHint();

  // Offline indicator toggling (display + dashboard)
  setInterval(() => {
    const off = $('#tp-offline');
    if (off && mesh) off.classList.toggle('is-visible', !mesh.serverOnline() && !mesh.isMaster() && mesh.openPeerIds().length === 0 && !!mesh.snap);
  }, 1000);
});
