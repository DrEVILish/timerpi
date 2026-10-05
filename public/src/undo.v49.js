/* undo.js — B3: the operator's undo ledger.

Scope (deliberate): the actions an operator can do BY MISTAKE in one
tap while a show is on the floor — cue DELETE, single-cue EDIT (inspector or
inline), quick-ADD, and DUP (A4, inverse = delete the copy). Transport (GO/pause) is intentionally NOT undoable:
un-doing a cue start would fight the engine's alert/anchor state rather than
help; operators reset instead.

The ledger is client-side (up to 10 deep, newest effect popped first) and
works both online (WS cue commands) and offline while this browser is mesh
master (engine.js mirrors the same commands locally) — it rides the SAME
send path, so no special casing. Deletion restores by re-creating the cue
and walking it back up to its slot with cueMove; the new row has a fresh id,
and per-cue updatedAt stamps make the tombstone merge treat it as a new row
(caveat: inserted BETWEEN other offline devices' edits merges as an
interleave — documented OFFLINE-EDIT §4/§6).
*/

const UNDO_DEPTH = 10;

export function createUndo(bus) {
  const stack = [];
  let pendingAdd = null; // { count, label } waiting for its row to exist

  /** Capture the inverse of an outbound command. Call BEFORE send.
      snap: the CURRENT snapshot (pre-command truth); action/args as sent. */
  function capture(action, args, snap) {
    if (!snap || !args) return;
    if (action === 'cueEdit') {
      const cue = snap.cues.find((c) => c.pos === args.pos);
      if (!cue) return;
      stack.push({
        kind: 'cueEdit', pos: cue.pos,
        before: {
          label: cue.label, speaker: cue.speaker, durationMS: cue.durationMS,
          holdMS: cue.holdMS || 0, tags: cue.tags, kind: cue.kind,
          timerKind: cue.timerKind, endAction: cue.endAction,
          autoContinue: !!cue.autoContinue,
          alert1MS: cue.alert1MS || 0, alert2MS: cue.alert2MS || 0,
          alertColor1: cue.alertColor1 || '', alertColor2: cue.alertColor2 || '',
          color: cue.color || '', notes: cue.notes || '',
        },
      });
    } else if (action === 'cueMove') {
      if (args.to != null && args.to !== args.pos) {
        // Full-slot splice: the moved item ends AT `to`, so the inverse is
        // one splice back (to→pos).
        stack.push({ kind: 'cueMove', from: args.to, to: args.pos });
      } else if (args.pos) {
        const dir = args.dir === 'down' ? 'down' : 'up';
        // ±1 move: after up the row sits one earlier, so its inverse is a
        // down; vice versa.
        const movedTo = dir === 'up' ? args.pos - 1 : args.pos + 1;
        stack.push({ kind: 'cueMove', from: movedTo, to: args.pos });
      }
    } else if (action === 'cueDel') {
      const cue = snap.cues.find((c) => c.pos === args.pos);
      if (!cue) return;
      stack.push({ kind: 'cueDel', cue: { ...cue } });
    } else if (action === 'cueDup') {
      // A4: the copy lands at pos+1 (DB.DuplicateCue semantics, mirrored by
      // engine.js offline master). Inverse = delete that ONE row. bounds
      // caveat is the ledger's usual one — a command that shifts positions
      // landed on the undo stack itself, so undo pops IT first and pos+1
      // still points at the copy when this entry runs.
      const cue = snap.cues.find((c) => c.pos === args.pos);
      if (!cue) return;
      stack.push({ kind: 'cueDup', pos: args.pos + 1, label: cue.label });
    } else if (action === 'cueAdd') {
      pendingAdd = { count: snap.cues.length, label: args.label || '' };
    }
    while (stack.length > UNDO_DEPTH) stack.shift();
  }

  /** Hook from the snapshot pump: resolves a queued add-inverse once the
      appended row lands (appended at the end; count tells us when). */
  function observe(snap) {
    if (!pendingAdd || !snap) return;
    const n = snap.cues.length;
    if (n === pendingAdd.count + 1
        && (!pendingAdd.label || (snap.cues[n - 1] || {}).label === pendingAdd.label)) {
      const row = snap.cues[n - 1];
      stack.push({ kind: 'cueDel', cue: { ...row } });
    }
    pendingAdd = null; // matched, or the moment passed — don't undo blind later
  }

  /** Undo the newest captured effect. Returns a toast text or null. */
  function perform() {
    const op = stack.pop();
    if (!op) return null;
    if (op.kind === 'cueMove') {
      bus.send('cueMove', { pos: op.from, to: op.to });
      return `Undone — row back to slot ${op.to}`;
    }
    if (op.kind === 'cueEdit') {
      bus.send('cueEdit', { pos: op.pos, ...op.before });
      return `Undone — cue ${String(op.pos).padStart(2, '0')} restored`;
    }
    if (op.kind === 'cueDup') {
      bus.send('cueDel', { pos: op.pos });
      return `Undone — duplicate of "${op.label}" removed`;
    }
    if (op.kind === 'cueDel') {
      // Re-create the row (appends at the end) and slide it back up to its
      // original slot. Positions move under us if edits happened since —
      // we use the CURRENT snapshot's count for the arithmetic, which keeps
      // the destination slot as close to truth as the protocol allows.
      const snap = bus.snap();
      const n = snap ? snap.cues.length : op.cue.pos;
      bus.send('cueAdd', {
        label: op.cue.label, durationMS: op.cue.durationMS, kind: op.cue.kind,
        speaker: op.cue.speaker, tags: op.cue.tags,
      });
      const appended = n + 1;
      for (let p = appended; p > op.cue.pos; p--) {
        bus.send('cueMove', { pos: p, dir: 'up' });
      }
      return `Undone — cue restored at ${String(op.cue.pos).padStart(2, '0')}`;
    }
    return null;
  }

  return { capture, observe, perform, depth: () => stack.length };
}
