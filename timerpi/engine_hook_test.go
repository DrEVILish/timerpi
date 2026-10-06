// Engine outbound-fire hook tests (test-gap round): OnStart fires exactly
// when a mutation leaves a cue RUNNING (go/start), never on
// pause/resume/reset — this is the CuTePi media lockstep contract
// (PLAN §11.6): the playout peer must fire with the cue, not with
// transport scrubbing.
package timerpi

import (
	"testing"
	"time"
)

func TestEngineOnStartFires(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Fire Hook")
	for _, cue := range []Cue{
		{Label: "One", DurationMS: 60_000},
		{Label: "Two", DurationMS: 60_000},
	} {
		if _, err := d.CreateCue(show.ID, cue); err != nil {
			t.Fatalf("seed cue: %v", err)
		}
	}
	engines := NewEngines(d)
	var fired []int64
	engines.OnStart = func(showID, pos int64) {
		if showID != show.ID {
			t.Errorf("OnStart showID: want %d got %d", show.ID, showID)
		}
		fired = append(fired, pos)
	}
	e, err := engines.Get(show.ID)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	if err := e.Go(); err != nil {
		t.Fatalf("go: %v", err)
	}
	if len(fired) != 1 || fired[0] <= 0 {
		t.Fatalf("go did not fire with a positive pos: %v", fired)
	}
	// Transport scrubbing is silent: pause/resume/reset never re-fire.
	if err := e.Pause(); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := e.Resume(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := e.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("pause/resume/reset fired the media hook: %v", fired)
	}
	// The next GO starts the next cue → fires with its pos.
	if err := e.Go(); err != nil {
		t.Fatalf("go2: %v", err)
	}
	if len(fired) != 2 {
		t.Fatalf("second go did not fire: %v", fired)
	}
}

// The media hook fires for hand starts only: alert flips and a zero
// crossing (no auto-continue any more, STATUS U42) stay silent.
func TestEngineOnStartSilentWithoutHandStart(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Auto Fire")
	cues := []Cue{
		{Label: "One", DurationMS: 1000, EndAction: EndHold, AutoContinue: true},
		{Label: "Two", DurationMS: 60_000},
	}
	for _, cue := range cues {
		if _, err := d.CreateCue(show.ID, cue); err != nil {
			t.Fatalf("seed cue: %v", err)
		}
	}
	engines := NewEngines(d)
	var fired []int64
	engines.OnStart = func(showID, pos int64) { fired = append(fired, pos) }
	e, err := engines.Get(show.ID)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if err := e.Go(); err != nil {
		t.Fatalf("go: %v", err)
	}
	// Alert flips (warning thresholds crossing) must NOT fire the hook.
	if err := e.Tick(time.Now().UnixMilli() + 200); err != nil {
		t.Fatalf("tick alert: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("alert transition fired the hook: %v", fired)
	}
	// Past cue 1's zero crossing: it holds; nothing starts, nothing fires.
	if err := e.Tick(time.Now().UnixMilli() + 1500); err != nil {
		t.Fatalf("tick past zero: %v", err)
	}
	if len(fired) != 1 {
		t.Fatalf("zero crossing fired the hook: %v", fired)
	}
}
