// The offline engine (engine.js, used while the server is unreachable)
// keeps the running cue through reorders, adds, duplicates and deletes:
// the playhead follows the cue, not its slot (parity with engine.go).
export const name = 'offline engine: reordering keeps the running cue';

import { applyCommand, activeCue } from '../../../public/src/engine.js';

export async function run(t) {
  const now = 1_000_000;
  const snap = {
    show: {}, messages: [], updatedAt: 0,
    cues: ['One', 'Two', 'Three', 'Four'].map((label, i) => ({ id: i + 1, pos: i + 1, label, durationMS: 600_000, timerKind: 'COUNTDOWN', endAction: 'HOLD' })),
    runtime: { activePos: 0, prevPos: 0, nextPos: 0, running: false, paused: false, anchorTS: 0, pausedElapsedMS: 0, rate: 1 },
  };
  applyCommand(snap, 'start', { pos: 3 }, now);
  const anchor = snap.runtime.anchorTS;
  const ok = (what, label) => {
    const c = activeCue(snap);
    t.check(`${what}: "${c?.label}" at ${snap.runtime.activePos}, running ${snap.runtime.running}`,
      c?.label === label && snap.runtime.running && snap.runtime.anchorTS === anchor);
  };
  ok('started', 'Three');
  applyCommand(snap, 'cueMove', { pos: 4, to: 1 }, now); ok('drag a row above it', 'Three');
  applyCommand(snap, 'cueMove', { pos: 4, to: 1 }, now); ok('drag the running cue to the top', 'Three');
  t.check(`it is at the top (${snap.cues.map((c) => c.label)})`, snap.runtime.activePos === 1 && snap.runtime.nextPos === 2);
  applyCommand(snap, 'cueMove', { pos: 1, dir: 'down' }, now); ok('step it down', 'Three');
  applyCommand(snap, 'cueAdd', { label: 'New', pos: 1 }, now); ok('insert a cue above it', 'Three');
  applyCommand(snap, 'cueDup', { pos: snap.runtime.activePos }, now); ok('duplicate it', 'Three');
  applyCommand(snap, 'cueDel', { pos: 1 }, now); ok('delete a cue above it', 'Three');
  const pos = snap.runtime.activePos;
  applyCommand(snap, 'cueDel', { pos }, now);
  t.check(`deleting it arms the cue in its slot (${activeCue(snap)?.label})`, !snap.runtime.running && snap.runtime.activePos === pos && activeCue(snap)?.label === 'Three');
}
