package timerpi

import (
	"sync/atomic"
	"testing"
)

// BUGLOG RW55: an engine reads its cues from the DB once, then only after
// a cue write — not on every 250 ms tick.
func TestEngineReadsCuesOnlyAfterWrites(t *testing.T) {
	d := openTestDB(t)
	sh := mustCreateShow(t, d, "Cache")
	if _, err := d.CreateCue(sh.ID, Cue{Label: "A", DurationMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	deps := d.EngineDeps(sh.ID)
	list := deps.Cues
	deps.Cues = func() ([]Cue, error) { reads.Add(1); return list() }
	e, err := NewEngine(sh.ID, deps)
	if err != nil {
		t.Fatal(err)
	}
	base := reads.Load()
	now := nowMS()
	for i := 0; i < 40; i++ { // ten seconds of ticks
		_ = e.Tick(now + int64(i)*250)
	}
	if n := reads.Load() - base; n > 1 {
		t.Errorf("40 idle ticks read the cues %d times", n)
	}
	if _, err := d.CreateCue(sh.ID, Cue{Label: "B", DurationMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	snap, err := e.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Cues) != 2 {
		t.Errorf("after a write the engine sees %d cues, want 2", len(snap.Cues))
	}
}
