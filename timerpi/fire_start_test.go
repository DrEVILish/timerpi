package timerpi

import "testing"

// BUGLOG RW24: the outbound media hook (OSC to CuTePi/QLab) fires only
// when Start/Go actually started a cue. A refused Start(99) or a GO past
// the last cue used to re-send the running cue's start.
func TestFireStartOnlyOnSuccessfulStart(t *testing.T) {
	e, _ := newTestEngine(t, 1_700_000_000_000, nil)
	var fired []int64
	e.onFire = func(pos int64) { fired = append(fired, pos) }

	if err := e.Start(5); err != nil {
		t.Fatalf("Start(5): %v", err)
	}
	if len(fired) != 1 || fired[0] != 5 {
		t.Fatalf("fired = %v, want [5]", fired)
	}
	if err := e.Start(99); err == nil {
		t.Fatal("Start(99) should fail")
	}
	if err := e.Go(); err == nil {
		t.Fatal("GO past the last cue should fail")
	}
	if len(fired) != 1 {
		t.Fatalf("failed start/GO fired the hook again: %v", fired)
	}
}

// BUGLOG RW30: engine args refuse floats outside exact int range.
func TestArgIntRefusesHugeFloats(t *testing.T) {
	if got := argInt(map[string]any{"pos": 1e30}, "pos"); got != 0 {
		t.Errorf("argInt(1e30) = %d, want 0", got)
	}
	if got := argInt(map[string]any{"pos": 3.0}, "pos"); got != 3 {
		t.Errorf("argInt(3) = %d", got)
	}
}
