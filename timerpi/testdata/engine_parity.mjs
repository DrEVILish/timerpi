// Runs the parity scenarios (stdin, JSON) through the browser's offline
// engine and prints the state after every step. Driven by
// TestEngineParityWithJS (engine_parity_test.go), which runs the same
// scenarios through the Go engine and compares.
import { applyCommand, tickZeroCrossing, elapsedMS } from '../../public/src/engine.js';

let input = '';
for await (const chunk of process.stdin) input += chunk;
const { t0, cues, scenarios } = JSON.parse(input);

const out = scenarios.map((sc) => {
  let now = t0;
  const snap = {
    show: {}, messages: [], updatedAt: 0,
    cues: cues.map((c, i) => ({ ...c, id: i + 1, pos: i + 1 })),
    runtime: { activePos: 0, prevPos: 0, nextPos: 0, running: false, paused: false, anchorTS: 0, pausedElapsedMS: 0, rate: 1, dayStartTS: 0 },
  };
  return sc.steps.map((st) => {
    now += st.advance || 0;
    if (st.action === 'tick') tickZeroCrossing(snap, now);
    else applyCommand(snap, st.action, st.args || {}, now);
    const r = snap.runtime;
    return {
      activePos: r.activePos, prevPos: r.prevPos, nextPos: r.nextPos,
      mode: r.paused ? 'paused' : r.running ? 'running' : 'stopped',
      elapsedMS: Math.floor(elapsedMS(snap, now)),
      rate: r.rate, dayStartTS: r.dayStartTS || 0,
      labels: snap.cues.map((c) => c.label).join(','),
    };
  });
});
process.stdout.write(JSON.stringify(out));
