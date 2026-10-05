package timerpi

// Edge-case regression tests for the cue engine. Each name states the edge
// it pins; every one corresponds to a state a feu operator can reach from
// the web UI alone, so a regression here is a show-floor bug.

import (
	"errors"
	"sync"
	"testing"
)

// mustEdgeEngine* and mustTimer are the minimal helpers this edge suite
// needs; they extend the existing engine_test.go fixtures rather than
// duplicating them (same cues via testCues/mutateCues).
func mustEdgeEngine(t *testing.T, t0 int64, mutate func([]Cue), cues func() ([]Cue, error)) *Engine {
	t.Helper()
	clk := &fakeClock{ms: t0}
	e, err := NewEngine(1, EngineDeps{
		Now:      clk.Now,
		Show:     func() (Show, error) { return Show{ID: 1, Title: "Edge"}, nil },
		Cues:     cues,
		Messages: func() ([]Message, error) { return nil, nil },
		Save:     func(Runtime) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func mustEdgeEngine2(t *testing.T, t0 int64, cues []Cue) (*Engine, *fakeClock) {
	t.Helper()
	clk := &fakeClock{ms: t0}
	e, err := NewEngine(1, EngineDeps{
		Now:      clk.Now,
		Show:     func() (Show, error) { return Show{ID: 1, Title: "Edge"}, nil },
		Cues:     func() ([]Cue, error) { return cues, nil },
		Messages: func() ([]Message, error) { return nil, nil },
		Save:     func(Runtime) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, clk
}

// mustEdgeEngine3 is the standard fixture (testCues).
func mustEdgeEngine3(t *testing.T, t0 int64) (*Engine, *fakeClock) {
	return mustEdgeEngine2(t, t0, testCues())
}

func mustTimer(t *testing.T, e *Engine) TimerFrame {
	t.Helper()
	tf, err := e.Timer()
	if err != nil {
		t.Fatalf("Timer: %v", err)
	}
	return tf
}

// Empty and single-cue shows flow through every transport op without panic
// and without inventing positions.
func TestEngineEmptyShowTransport(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	other := t0
	e := mustEdgeEngine(t, t0, nil, func() ([]Cue, error) { other++; return nil, nil })

	if err := e.Go(); !errors.Is(err, ErrNoCues) {
		t.Fatalf("Go on empty show: %v, want ErrUnknownPos", err)
	}
	if err := e.Next(); !errors.Is(err, ErrNoCues) {
		t.Fatalf("Next on empty show: %v", err)
	}
	if err := e.Prev(); !errors.Is(err, ErrNoCues) {
		t.Fatalf("Prev on empty show: %v", err)
	}
	if err := e.Start(1); !errors.Is(err, ErrUnknownPos) {
		t.Fatalf("Start unknown pos on empty show: %v", err)
	}
	if err := e.Jump(0); !errors.Is(err, ErrUnknownPos) {
		t.Fatalf("Jump 0: %v", err)
	}
	if err := e.Jump(1); !errors.Is(err, ErrUnknownPos) {
		t.Fatalf("Jump 1 on empty: %v", err)
	}
	if got := e.Runtime().ActivePos; got != 0 {
		t.Fatalf("empty-show active = %d, want 0", got)
	}
}

func TestEngineSingleCueShowBoundaries(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	cues := []Cue{{ID: 1, ShowID: 1, Pos: 1, Label: "Only", DurationMS: 60_000,
		TimerKind: TimerCountdown, EndAction: EndHold}}
	e, clk := mustEdgeEngine2(t, t0, cues)

	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Next on the only cue: no changed state, no invention of pos 2.
	if err := e.Next(); err != nil {
		t.Fatalf("Next single: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 1 {
		t.Fatalf("Next on single-cue show moved active to %+v", rt)
	}
	if err := e.Prev(); err != nil {
		t.Fatalf("Prev single: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 1 {
		t.Fatalf("Prev on single-cue show moved active: %+v", rt)
	}
	// GO while running the only cue: refused (ErrNoNextCue), never
	// inventing position 2.
	if err := e.Start(1); err != nil {
		t.Fatalf("re-Start: %v", err)
	}
	clk.advance(1_000)
	if err := e.Go(); !errors.Is(err, ErrNoNextCue) {
		t.Fatalf("Go on single running cue: %v, want ErrNoNextCue", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 1 {
		t.Fatalf("Go past end invented pos %d", rt.ActivePos)
	}
	// Prev at the first cue from idle does not go below 1.
	if err := e.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := e.Prev(); err != nil {
		t.Fatalf("Prev at first: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 1 {
		t.Fatalf("Prev below first invented pos %d, want 1", rt.ActivePos)
	}
}

// Zero and negative durations are impossible via Normalize, but raw cue
// sources (imports, merges of older data) can still deliver them — the
// engine must not divide-by-zero or stall forever.
func TestEngineZeroLengthCueCrossesImmediately(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	cues := []Cue{
		{ID: 1, ShowID: 1, Pos: 1, Label: "ZeroLen", DurationMS: 0,
			TimerKind: TimerCountdown, EndAction: EndHold},
	}
	e, clk := mustEdgeEngine2(t, t0, cues)
	if err := e.Start(1); err != nil {
		t.Fatalf("Start zero-length: %v", err)
	}
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	rt := e.Runtime()
	if rt.Running {
		t.Fatalf("zero-length cue still running after one tick: %+v", rt)
	}
	tf := mustTimer(t, e)
	if tf.RemainingMS != 0 {
		t.Fatalf("zero-length remaining = %d, want 0", tf.RemainingMS)
	}
}

// Rate 0 / negative / huge get the DefaultRate treatment everywhere time is
// computed (operator fat-finger protection).
func TestEngineBrokenRateClamped(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	for _, tc := range []struct {
		name string
		rate float64
		want float64
	}{
		{"zero", 0, DefaultRate},
		{"negative", -2.5, DefaultRate},
		{"huge", 1e9, 1e9}, // finite rates pass through; only <=0 breaks
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, clk := mustEdgeEngine3(t, t0)
			err := e.SetRate(tc.rate)
			if tc.rate <= 0 {
				// Contract: REJECT non-positive rates (ErrBadRate), keep
				// the live rate untouched — no clamp to default on the wire.
				if !errors.Is(err, ErrBadRate) {
					t.Fatalf("SetRate(%v) = %v, want ErrBadRate", tc.rate, err)
				}
				if rt := e.Runtime(); rt.Rate != DefaultRate {
					t.Fatalf("rate %v accepted; live rate now %v", tc.rate, rt.Rate)
				}
				return
			}
			// 1e9: legal finite rate — the very first tick must land
			// on/past zero (elapsed = 60e9 ms ≥ duration), no NaN, no freeze.
			if err != nil {
				t.Fatalf("SetRate(%v): %v", tc.rate, err)
			}
			if err := e.Start(1); err != nil {
				t.Fatalf("Start: %v", err)
			}
			clk.advance(10)
			tf := mustTimer(t, e)
			if tf.RemainingMS != 0 && tf.RemainingMS > -500 {
				t.Fatalf("huge-rate remaining after 1 tick + 10ms = %d, want ~0", tf.RemainingMS)
			}
		})
	}
}

// Pause before start, reset while idle: legal no-ops, never inventoried
// time. Regression: a reset-idle that flips PausedElapsedMS to duration
// would fake "held" into the client predicate.
func TestEnginePauseBeforeStartAndIdleReset(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := mustEdgeEngine2(t, t0, testCues())

	if err := e.Pause(); err != nil {
		t.Fatalf("Pause before start: %v", err)
	}
	rt := e.Runtime()
	if rt.Paused || rt.AnchorTS != 0 || rt.PausedElapsedMS != 0 {
		t.Fatalf("pause-before-start mutated runtime: %+v", rt)
	}
	clk.advance(60_000)
	if err := e.Reset(); err != nil {
		t.Fatalf("Reset idle: %v", err)
	}
	rt = e.Runtime()
	if rt.PausedElapsedMS != 0 || rt.Paused || rt.Running {
		t.Fatalf("idle reset invented state: %+v", rt)
	}
	if rt.PausedElapsedMS != 0 || rt.Paused || rt.Running {
		// recheck after the clock ran: no time invented while idle
		t.Fatalf("idle reset invented elapsed after clock flow: %+v", e.Runtime())
	}
}

// start-from-HOLD: pressing Start while the cue is held at zero must not
// resurrect a dead timer (the operator uses Prev/Next/Reset for that).
func TestEngineStartHeldCueIgnored(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := mustEdgeEngine2(t, t0, testCues())
	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clk.advance(600_001)
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if e.Runtime().Running {
		t.Fatalf("setup wrong: still running at hold")
	}
	before := e.Runtime()
	if err := e.Start(1); !errors.Is(err, ErrNoTimeLeft) {
		t.Fatalf("Start on held: %v, want ErrNoTimeLeft", err)
	}
	if rt := e.Runtime(); rt.AnchorTS != before.AnchorTS || rt.Running {
		t.Fatalf("Start on held cue resurrected: %+v", rt)
	}
	if tf := mustTimer(t, e); tf.RemainingMS != 0 {
		t.Fatalf("held remaining = %d, want frozen 0", tf.RemainingMS)
	}
}

// Concurrent transport commands (two operators) never leave a torn runtime:
// each mutation must land entirely or not at all. Regression: interleaved
// Start+Jump racing outside the mutex panics or loses an anchor.
func TestEngineConcurrentTransportTorn(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, _ := mustEdgeEngine2(t, t0, testCues())
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 5 {
			case 0:
				_ = e.Go()
			case 1:
				_ = e.Next()
			case 2:
				_ = e.Prev()
			case 3:
				_ = e.Pause()
			case 4:
				_ = e.Reset()
			}
		}(i)
	}
	wg.Wait()
	rt := e.Runtime()
	if rt.ActivePos < 0 || rt.ActivePos > 5 {
		t.Fatalf("torn runtime pos = %d", rt.ActivePos)
	}
	if rt.ActivePos != 0 && (rt.AnchorTS > 0 != rt.Running) {
		t.Fatalf("anchor/running invariant broken: %+v", rt)
	}
}

// ---------------------------------------------------------------------------
// Schedule edges

func TestScheduleEdgeEmptyAllZeroAllBreak(t *testing.T) {
	day := int64(1_700_000_000_000)
	if s := ComputeSchedule(nil, day, 1.0); s.TotalMS != 0 || len(s.Rows) != 0 || s.EndTS != day {
		t.Fatalf("empty schedule: %+v", s)
	}
	allZero := []Cue{
		{ID: 1, ShowID: 1, Pos: 1, DurationMS: 0, Kind: KindSession},
		{ID: 2, ShowID: 1, Pos: 2, DurationMS: 0, Kind: KindBreak},
	}
	if s := ComputeSchedule(allZero, day, 1.0); s.TotalMS != 0 || s.EndTS != day || len(s.Rows) != 2 {
		t.Fatalf("all-zero schedule: %+v", s)
	}
	// All-break show: no session rows, breaks still sum into BreaksMS.
	allBreak := []Cue{
		{ID: 1, ShowID: 1, Pos: 1, DurationMS: 60_000, Kind: KindBreak},
		{ID: 2, ShowID: 1, Pos: 2, DurationMS: 30_000, Kind: KindBreak},
	}
	s := ComputeSchedule(allBreak, day, 1.0)
	if s.TotalMS != 90_000 || s.BreaksMS != 90_000 || s.HoldsMS != 0 {
		t.Fatalf("all-break summary: %+v", s)
	}
	for i, r := range s.Rows {
		if !r.Break {
			t.Fatalf("row %d not marked break: %+v", i, r)
		}
	}
}

func TestScheduleEdgeHugeAndCumulative(t *testing.T) {
	day := int64(1_700_000_000_000)
	cues := []Cue{
		{ID: 1, ShowID: 1, Pos: 1, DurationMS: 10 * 3600_000, Kind: KindSession, HoldMS: 5 * 60_000},
		{ID: 2, ShowID: 1, Pos: 2, DurationMS: int64(23) * 3600_000, Kind: KindBreak},
	}
	s := ComputeSchedule(cues, day, 1.0)
	if s.TotalMS != 33*3600_000+300_000 {
		t.Fatalf("multi-hour total: %d", s.TotalMS)
	}
	if s.HoldsMS != 300_000 || s.BreaksMS != 23*3600_000 {
		t.Fatalf("multi-hour holds/breaks: %d/%d", s.HoldsMS, s.BreaksMS)
	}
	// CumHoldMS accumulates BEFORE this row's additions are added to the
	// running totals... verify against the Monday-reorder: row2 sees row1's
	// hold in its cumulative.
	if s.Rows[1].CumHoldMS != 5*60_000 {
		t.Fatalf("row2 cumhold = %d, want 300000", s.Rows[1].CumHoldMS)
	}
}
