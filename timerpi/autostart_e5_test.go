package timerpi

import (
	"testing"
	"time"
)

// hhmmOf formats an epoch-ms instant as local HH:MM (E5 inputs are wall
// clock strings; deriving them from the fake clock keeps tests
// timezone-proof).
func hhmmOf(ms int64) string {
	return time.UnixMilli(ms).Local().Format("15:04")
}

// E5: an idle engine starts the first past-playhead cue whose startAt has
// come on Tick; nothing else moves.
func TestAutoStartFiresWhenIdle(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	past := hhmmOf(t0 - 60_000)
	future := hhmmOf(t0 + 3_600_000)
	e, clk := newTestEngine(t, t0, func(cues []Cue) {
		cues[1].StartAt = past   // cue2 overdue
		cues[2].StartAt = future // cue3 later
	})
	// E5 guard: the day must be UNDER WAY (ActivePos>0) — timers never
	// start a show nobody has begun (clones included).
	if err := e.Jump(1); err != nil {
		t.Fatalf("Jump: %v", err)
	}
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 2 || !rt.Running {
		t.Fatalf("autostart = %+v, want cue2 running", rt)
	}
	// Firing advanced the playhead: the next Tick must NOT also fire cue3
	// early (one cue per due time; cue3 waits for its own hour).
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 2 {
		t.Fatalf("second tick moved to %+v, want still cue2", rt)
	}
}

// E5: a running engine is never yanked — overdue timers wait for idle.
func TestAutoStartNeverYanksRunning(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	past := hhmmOf(t0 - 60_000)
	e, clk := newTestEngine(t, t0, func(cues []Cue) {
		cues[1].StartAt = past
	})
	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 1 || !rt.Running {
		t.Fatalf("running cue yanked: %+v", rt)
	}
}

// E5: a paused engine is operator-held — timers wait for resume/idle.
func TestAutoStartSkipsPaused(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	past := hhmmOf(t0 - 60_000)
	e, clk := newTestEngine(t, t0, func(cues []Cue) {
		cues[1].StartAt = past
	})
	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 1 || !rt.Paused {
		t.Fatalf("paused engine disturbed: %+v", rt)
	}
}

// E5: garbage startAt (hand-edited DB, pre-validation rows) never fires
// and never errors the tick.
func TestAutoStartIgnoresGarbage(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, func(cues []Cue) {
		cues[0].StartAt = "not-a-time"
	})
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.Running || rt.ActivePos != 0 {
		t.Fatalf("garbage startAt fired: %+v", rt)
	}
}

// E5: two overdue cues past the playhead fire in order (no skipping to
// the later one); a cue AT/BEFORE the playhead never re-arms by clock.
func TestAutoStartOrderNoSkip(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	past := hhmmOf(t0 - 60_000)
	e, clk := newTestEngine(t, t0, func(cues []Cue) {
		cues[1].StartAt = past
		cues[2].StartAt = past
	})
	if err := e.Jump(1); err != nil {
		t.Fatalf("Jump: %v", err)
	}
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 2 {
		t.Fatalf("skipped to %+v, want cue2 first", rt)
	}
	// A startAt on the now-active cue does not refire it.
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 2 || !rt.Running {
		t.Fatalf("refire disturbed cue2: %+v", rt)
	}
}

// E5 regression (review): a never-started show — e.g. a CLONE inheriting
// startAt rows — must stay idle no matter what time it is.
func TestAutoStartSilentOnFreshShow(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	past := hhmmOf(t0 - 60_000)
	e, clk := newTestEngine(t, t0, func(cues []Cue) {
		cues[0].StartAt = past
		cues[1].StartAt = past
	})
	for i := 0; i < 3; i++ {
		if err := e.Tick(clk.Now()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}
	if rt := e.Runtime(); rt.Running || rt.ActivePos != 0 {
		t.Fatalf("fresh show auto-started: %+v", rt)
	}
}

// E5: a startAt difference is cue content — a server-side timer edit
// beats an untimed incoming row and counts as remote.
func TestMergeStartAtCountsRemote(t *testing.T) {
	srv := mergeCue(1, 1, "Keynote", 300)
	srv.StartAt = "09:30"
	merged, remote := MergeCues([]Cue{srv}, []Cue{mergeCue(1, 1, "Keynote", 100)}, nil)
	if remote != 1 {
		t.Fatalf("remote = %d, want 1 (server timer edit)", remote)
	}
	if merged[0].StartAt != "09:30" {
		t.Fatalf("timer lost in merge: %+v", merged[0])
	}
}

// E5: held-at-zero counts as idle — the next timed cue advances the day.
func TestAutoStartAdvancesHeld(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	past := hhmmOf(t0 - 60_000)
	// Exhaust cue1 instantly (zero-length hold row) then tick past zero.
	e2, clk2 := newTestEngine(t, t0, func(cues []Cue) {
		cues[0].DurationMS = 0
		cues[1].StartAt = past
	})
	if err := e2.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clk2.ms += 1000
	if err := e2.Tick(clk2.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// The zero cue crosses to HOLD and the overdue cue2 fires in the same
	// tick — the day advances without stranding on the spent row.
	if rt := e2.Runtime(); rt.ActivePos != 2 || !rt.Running {
		t.Fatalf("held engine did not advance to timed cue2: %+v", rt)
	}
}
