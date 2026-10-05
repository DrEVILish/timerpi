package timerpi

import (
	"testing"
)

// Owner decision 2026-10-05: GO on a held-at-zero cue restarts its full
// duration (the explicit Start(pos) still refuses ErrNoTimeLeft — that
// guard is covered by TestEngineStartHeldCueIgnored and stays).
func TestGoRestartsHeldCue(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, nil)
	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clk.ms += 600_001 // cue1 is 600 s: run it to zero and hold
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.Running || rt.ActivePos != 1 {
		t.Fatalf("setup: not held at cue1: %+v", rt)
	}
	if err := e.Go(); err != nil {
		t.Fatalf("Go on held: %v", err)
	}
	rt := e.Runtime()
	if rt.ActivePos != 1 || !rt.Running {
		t.Fatalf("GO did not restart cue1: %+v", rt)
	}
	tf := timerOf(t, e)
	if tf.RemainingMS != 600_000 {
		t.Fatalf("restarted remaining = %d, want full 600000", tf.RemainingMS)
	}
	// And it holds again at the next zero (restarted, not resurrected once).
	clk.ms += 600_001
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.Running {
		t.Fatalf("restarted cue did not hold at zero: %+v", rt)
	}
}
