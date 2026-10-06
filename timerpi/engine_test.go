package timerpi

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Test scaffolding: fake clock + static cue sheet

type fakeClock struct{ ms int64 }

func (c *fakeClock) Now() int64       { return c.ms }
func (c *fakeClock) advance(ms int64) { c.ms += ms }

// testCues is the shared sheet: 1 Welcome (10 min), 2 Keynote (20 min,
// overtime), 3 Break (5 min), 4 Slot (1 min, autocontinue), 5 Last (100 s).
func testCues() []Cue {
	return []Cue{
		{ID: 1, ShowID: 1, Pos: 1, Label: "Welcome", DurationMS: 600000, Kind: KindSession,
			Tags: "VT GFX", Speaker: "Leslie", TimerKind: TimerCountdown, EndAction: EndHold},
		{ID: 2, ShowID: 1, Pos: 2, Label: "Keynote", DurationMS: 1200000, Kind: KindSession,
			Speaker: "Ron", TimerKind: TimerCountdown, EndAction: EndOvertime},
		{ID: 3, ShowID: 1, Pos: 3, Label: "Break", DurationMS: 300000, Kind: KindBreak,
			TimerKind: TimerCountdown, EndAction: EndHold, HoldMS: 120000},
		{ID: 4, ShowID: 1, Pos: 4, Label: "Slot", DurationMS: 60000, Kind: KindSession,
			AutoContinue: true, TimerKind: TimerCountdown, EndAction: EndHold},
		{ID: 5, ShowID: 1, Pos: 5, Label: "Last", DurationMS: 100000, Kind: KindSession,
			TimerKind: TimerCountdown, EndAction: EndHold,
			Alert1MS: 60000, AlertColor1: DefaultAlertColor1,
			Alert2MS: 20000, AlertColor2: DefaultAlertColor2},
	}
}

func newTestEngine(t *testing.T, startMS int64, mutateCues func([]Cue)) (*Engine, *fakeClock) {
	t.Helper()
	clk := &fakeClock{ms: startMS}
	cues := testCues()
	if mutateCues != nil {
		mutateCues(cues)
	}
	e, err := NewEngine(1, EngineDeps{
		Now:      clk.Now,
		Show:     func() (Show, error) { return Show{ID: 1, Title: "Pawnee Townhall"}, nil },
		Cues:     func() ([]Cue, error) { return cues, nil },
		Messages: func() ([]Message, error) { return nil, nil },
		Save:     func(Runtime) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, clk
}

// timerOf is a small helper: engine's Timer frame + a failure message.
func timerOf(t *testing.T, e *Engine) TimerFrame {
	t.Helper()
	tf, err := e.Timer()
	if err != nil {
		t.Fatalf("Timer: %v", err)
	}
	return tf
}

// ---------------------------------------------------------------------------
// Start / Pause / Resume / Reset timing math (mocked clock)

func TestStartPauseResumeTiming(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, nil)

	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	rt := e.Runtime()
	if !rt.Running || rt.Paused || rt.AnchorTS != t0 || rt.ActivePos != 1 {
		t.Fatalf("start state wrong: %+v", rt)
	}
	if rt.PrevPos != 0 || rt.NextPos != 2 || rt.EndAction != EndHold {
		t.Fatalf("neighbors/endaction wrong: %+v", rt)
	}

	clk.advance(10_000)
	if got := timerOf(t, e); got.RemainingMS != 590_000 {
		t.Fatalf("after 10s: remaining = %d, want 590000", got.RemainingMS)
	}

	// Pause freezes the display regardless of the clock.
	if err := e.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	clk.advance(30_000)
	tf := timerOf(t, e)
	if tf.RemainingMS != 590_000 || !tf.Paused {
		t.Fatalf("paused display = %+v, want frozen 590000", tf)
	}

	// Resume re-anchors; only wall time after the resume counts.
	if err := e.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	clk.advance(5_000)
	if got := timerOf(t, e); got.RemainingMS != 585_000 {
		t.Fatalf("after resume+5s: remaining = %d, want 585000", got.RemainingMS)
	}

	// Reset arms the cue again (no anchor, full duration).
	if err := e.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	rt = e.Runtime()
	if rt.Running || rt.Paused || rt.AnchorTS != 0 || rt.PausedElapsedMS != 0 || rt.ActivePos != 1 {
		t.Fatalf("reset state wrong: %+v", rt)
	}
	if got := timerOf(t, e); got.RemainingMS != 600_000 {
		t.Fatalf("after reset: remaining = %d, want 600000", got.RemainingMS)
	}
}

// TestRateReAnchorContinuity: changing rate must never jump the displayed
// time, and subsequent time must scale by the new rate (0.5 / 1.0 / 2.0).
func TestRateReAnchorContinuity(t *testing.T) {
	for _, rate := range []float64{0.5, 1.0, 2.0} {
		t.Run(fmt.Sprintf("rate=%g", rate), func(t *testing.T) {
			t0 := int64(1_700_000_000_000)
			e, clk := newTestEngine(t, t0, nil)
			if err := e.Start(1); err != nil { // 600000 ms cue
				t.Fatalf("Start: %v", err)
			}
			clk.advance(10_000) // 10 s at rate 1 → 590000 left
			before := timerOf(t, e)
			if before.RemainingMS != 590_000 {
				t.Fatalf("setup: remaining = %d", before.RemainingMS)
			}

			// Re-anchor at the same instant: the displayed value must not move.
			if err := e.SetRate(rate); err != nil {
				t.Fatalf("SetRate: %v", err)
			}
			after := timerOf(t, e)
			if after.RemainingMS != before.RemainingMS || after.Rate != rate {
				t.Fatalf("continuity: before %+v after %+v", before, after)
			}

			// 5 s of wall time then scales by the new rate.
			clk.advance(5_000)
			want := 590_000 - int64(5_000*rate)
			if got := timerOf(t, e); got.RemainingMS != want {
				t.Fatalf("scaled remaining = %d, want %d", got.RemainingMS, want)
			}

			// Rate while paused keeps the frozen value, resume scales on.
			if err := e.Pause(); err != nil {
				t.Fatalf("Pause: %v", err)
			}
			frozen := timerOf(t, e)
			if err := e.SetRate(2.0); err != nil {
				t.Fatalf("SetRate paused: %v", err)
			}
			if got := timerOf(t, e); got.RemainingMS != frozen.RemainingMS {
				t.Fatalf("paused rate change moved display: %d → %d",
					frozen.RemainingMS, got.RemainingMS)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Zero crossing per EndAction

func TestZeroCrossingEndActions(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	cases := []struct {
		name      string
		pos       int64
		endAction string
		mutate    func([]Cue)
		check     func(t *testing.T, e *Engine, tf TimerFrame, rt Runtime)
	}{
		{
			name: "hold", pos: 1, endAction: EndHold,
			check: func(t *testing.T, e *Engine, tf TimerFrame, rt Runtime) {
				if rt.Running || tf.RemainingMS != 0 || tf.Overtime || tf.Blank {
					t.Fatalf("HOLD after zero: tf=%+v rt=%+v", tf, rt)
				}
				// Stays frozen at zero as time keeps flowing.
			},
		},
		{
			name: "overtime", pos: 2, endAction: EndOvertime,
			check: func(t *testing.T, e *Engine, tf TimerFrame, rt Runtime) {
				if !rt.Running || !tf.Overtime || tf.RemainingMS != -30_000 {
					t.Fatalf("OVERTIME after zero: tf=%+v rt=%+v", tf, rt)
				}
			},
		},
		{
			name: "blank", pos: 3, endAction: EndBlank,
			mutate: func(cues []Cue) { cues[2].EndAction = EndBlank },
			check: func(t *testing.T, e *Engine, tf TimerFrame, rt Runtime) {
				if rt.Running || !tf.Blank || tf.RemainingMS != 0 {
					t.Fatalf("BLANK after zero: tf=%+v rt=%+v", tf, rt)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, clk := newTestEngine(t, t0, tc.mutate)
			if err := e.Start(tc.pos); err != nil {
				t.Fatalf("Start: %v", err)
			}
			switch tc.pos {
			case 1: // 10 min cue: 10 s past zero
				clk.advance(610_000)
			case 2: // 20 min cue: 30 s past zero
				clk.ms = t0 + 1_230_000
			case 3: // 5 min break cue: 10 s past zero
				clk.advance(310_000)
			}
			if err := e.Tick(clk.Now()); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			tc.check(t, e, timerOf(t, e), e.Runtime())
		})
	}
}

// ---------------------------------------------------------------------------
// Alert transitions

func TestAlertTransitions(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, nil)
	if err := e.Start(5); err != nil { // 100 s cue, alert1 @60 s, alert2 @20 s
		t.Fatalf("Start: %v", err)
	}

	steps := []struct {
		advance int64
		want    int
		color   string
	}{
		{0, 0, ""},             // 100000 left
		{45_000, 1, "#ffaa00"}, // 55000 left → alert1
		{40_000, 2, "#ff4444"}, // 15000 left → alert2
	}
	for i, s := range steps {
		clk.advance(s.advance)
		if err := e.Tick(clk.Now()); err != nil {
			t.Fatalf("step %d Tick: %v", i, err)
		}
		tf := timerOf(t, e)
		if tf.AlertState != s.want || tf.AlertColor != s.color {
			t.Fatalf("step %d: alert = %d/%q, want %d/%q", i, tf.AlertState, tf.AlertColor, s.want, s.color)
		}
	}

	// Alerts never fire on a freshly armed cue or on other cues' sheets.
	if err := e.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if tf := timerOf(t, e); tf.AlertState != 0 {
		t.Fatalf("armed cue alert = %d, want 0", tf.AlertState)
	}
}

// ---------------------------------------------------------------------------
// No auto-advance (owner, 2026-10-06, STATUS U42)

func TestNoAutoAdvance(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	// Cue 4 is stored with AutoContinue: it still holds at zero.
	e, clk := newTestEngine(t, t0, nil)
	if err := e.Start(4); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clk.advance(60_000)
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 4 || rt.Running {
		t.Fatalf("cue 4 advanced by itself: %+v", rt)
	}
}

// ---------------------------------------------------------------------------
// Transport: Next / Prev / Jump / Go

func TestTransportNextPrevJumpGo(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, nil)

	// GO from idle starts the first cue.
	if err := e.Go(); err != nil {
		t.Fatalf("Go idle: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 1 || !rt.Running {
		t.Fatalf("go from idle: %+v", rt)
	}

	// Next arms cue 2 (running=false, anchored nowhere).
	if err := e.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	rt := e.Runtime()
	if rt.ActivePos != 2 || rt.Running || rt.AnchorTS != 0 || rt.PrevPos != 1 || rt.NextPos != 3 {
		t.Fatalf("next: %+v", rt)
	}

	// GO fires the armed cue.
	if err := e.Go(); err != nil {
		t.Fatalf("Go armed: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 2 || !rt.Running || rt.AnchorTS == 0 {
		t.Fatalf("go armed: %+v", rt)
	}

	// Jump arms an arbitrary cue and stops the running one.
	if err := e.Jump(4); err != nil {
		t.Fatalf("Jump: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 4 || rt.Running || rt.PausedElapsedMS != 0 {
		t.Fatalf("jump: %+v", rt)
	}

	// Prev walks back.
	if err := e.Prev(); err != nil {
		t.Fatalf("Prev: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 3 || rt.PrevPos != 2 || rt.NextPos != 4 {
		t.Fatalf("prev: %+v", rt)
	}

	// Running cue: GO starts the next one.
	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clk.advance(1_000)
	if err := e.Go(); err != nil {
		t.Fatalf("Go running: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 2 || rt.PausedElapsedMS != 0 {
		t.Fatalf("go running: %+v", rt)
	}

	// Jump to an unknown position is rejected.
	if err := e.Jump(99); !errors.Is(err, ErrUnknownPos) {
		t.Fatalf("jump unknown: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ApplyCmd dispatch

func TestApplyCmd(t *testing.T) {
	e, _ := newTestEngine(t, 1_700_000_000_000, nil)
	if err := e.ApplyCmd("start", map[string]any{"pos": float64(1)}); err != nil {
		t.Fatalf("cmd start: %v", err)
	}
	if err := e.ApplyCmd("pause", nil); err != nil {
		t.Fatalf("cmd pause: %v", err)
	}
	if rt := e.Runtime(); !rt.Paused {
		t.Fatalf("pause cmd ignored: %+v", rt)
	}
	if err := e.ApplyCmd("rate", map[string]any{"rate": 2.0}); err != nil {
		t.Fatalf("cmd rate: %v", err)
	}
	if rt := e.Runtime(); rt.Rate != 2.0 {
		t.Fatalf("rate cmd ignored: %+v", rt)
	}
	if err := e.ApplyCmd("warp", nil); !errors.Is(err, ErrUnknownCmd) {
		t.Fatalf("unknown cmd: %v", err)
	}
	if err := e.ApplyCmd("rate", map[string]any{"rate": 0.0}); !errors.Is(err, ErrBadRate) {
		t.Fatalf("bad rate: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Pubsub

func TestSubscribeFanout(t *testing.T) {
	e, _ := newTestEngine(t, 1_700_000_000_000, nil)

	var mu sync.Mutex
	var got []Snapshot
	cancel := e.Subscribe(func(s Snapshot) {
		mu.Lock()
		got = append(got, s)
		mu.Unlock()
	})

	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	mu.Lock()
	if len(got) != 1 || got[0].Runtime.ActivePos != 1 || got[0].Show.Title != "Pawnee Townhall" {
		mu.Unlock()
		t.Fatalf("subscriber saw %+v", got)
	}
	mu.Unlock()

	cancel() // no further deliveries
	if err := e.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("after cancel: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// DisplayedRemaining parity: the exact client formula

func TestDisplayedRemainingFormula(t *testing.T) {
	c := &Cue{Pos: 1, DurationMS: 60_000, TimerKind: TimerCountdown}
	rt := Runtime{Running: true, AnchorTS: 1000, Rate: 1.0}

	// remaining = duration - ((now - anchor) * rate + pausedElapsed)
	if got, _, _ := DisplayedRemaining(c, rt, 21_000); got != 40_000 {
		t.Fatalf("formula: %d, want 40000", got)
	}
	rt.Rate = 2.0
	if got, _, _ := DisplayedRemaining(c, rt, 21_000); got != 20_000 {
		t.Fatalf("rate formula: %d, want 20000", got)
	}
	rt.Paused = true
	rt.PausedElapsedMS = 30_000
	if got, _, _ := DisplayedRemaining(c, rt, 99_999); got != 30_000 {
		t.Fatalf("paused formula: %d, want 30000", got)
	}
	// COUNTSTOP counts up; CLOCK shows nothing.
	up := &Cue{Pos: 1, DurationMS: 60_000, TimerKind: TimerCountStop}
	if got, _, _ := DisplayedRemaining(up, rt, 99_999); got != 30_000 {
		t.Fatalf("countstop: %d, want 30000", got)
	}
	clockCue := &Cue{TimerKind: TimerClock}
	if got, ot, al := DisplayedRemaining(clockCue, rt, 99_999); got != 0 || ot || al != 0 {
		t.Fatalf("clock: %d %v %d", got, ot, al)
	}
}

// ---------------------------------------------------------------------------
// End-state wire shape (REVIEW-3 #2/#7 server half): the recorded runtime a
// zero-crossed cue leaves behind + the snapshot/Timer mirror. The client
// (public/src/engine.js) mirrors these predicates — the definition of
// record is the DisplayedRemaining doc block in snapshot.go.

// TestHoldEndStateSnapshotShape: held ≠ paused — Running=false, Paused
// stays FALSE, frozen at PausedElapsedMS=DurationMS; the alert state stays
// saturated at the deepest threshold while held (unchanged-when-held).
func TestHoldEndStateSnapshotShape(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, nil)
	if err := e.Start(5); err != nil { // 100 s cue, alert1 @60 s, alert2 @20 s
		t.Fatalf("Start: %v", err)
	}
	clk.ms = t0 + 200_000 // 100 s past zero
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	rt := e.Runtime()
	if rt.Running || rt.Paused || rt.PausedElapsedMS != 100_000 || rt.ActivePos != 5 {
		t.Fatalf("held runtime = %+v, want running=false paused=false elapsed=100000", rt)
	}
	// The HELD predicate the client mirrors: !running && elapsed >= duration.
	if rt.Running || rt.PausedElapsedMS < 100_000 {
		t.Fatalf("HELD predicate broken: %+v", rt)
	}

	snap, err := e.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	v := snap.Runtime
	if v.Running || v.Paused || v.PausedElapsedMS != 100_000 ||
		v.RemainingMS != 0 || v.Overtime || v.Blank {
		t.Fatalf("held snapshot runtime = %+v", v)
	}
	// Saturation persists on the held cue: deepest configured state.
	if v.AlertState != 2 || v.AlertColor != "#ff4444" {
		t.Fatalf("held alert = %d/%q, want 2/#ff4444", v.AlertState, v.AlertColor)
	}
	tf := timerOf(t, e)
	if tf.RemainingMS != 0 || tf.Paused || tf.Blank || tf.Overtime || tf.AlertState != 2 {
		t.Fatalf("held Timer frame = %+v", tf)
	}

	// Frozen: more wall time changes nothing (no replay crossing, no alert
	// flicker, no drift into Paused=true).
	clk.ms = t0 + 500_000
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	snap2, _ := e.Snapshot()
	if rt2 := e.Runtime(); rt2 != rt {
		t.Fatalf("held state drifted: %+v vs %+v", rt2, rt)
	}
	if snap2.Runtime != v {
		t.Fatalf("held snapshot runtime view drifted: %+v vs %+v", snap2.Runtime, v)
	}
}

// TestBlankEndStateSnapshotShape: the same recorded end-state as HOLD
// (running=false, paused=false, elapsed=duration) plus the snapshot blank
// flag with RemainingMS pinned to 0.
func TestBlankEndStateSnapshotShape(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, func(cues []Cue) { cues[2].EndAction = EndBlank })
	if err := e.Start(3); err != nil { // 5 min break cue
		t.Fatalf("Start: %v", err)
	}
	clk.ms = t0 + 310_000 // 10 s past zero
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	rt := e.Runtime()
	if rt.Running || rt.Paused || rt.PausedElapsedMS != 300_000 {
		t.Fatalf("blank runtime = %+v, want held shape elapsed=duration", rt)
	}
	snap, err := e.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	v := snap.Runtime
	if !v.Blank || v.RemainingMS != 0 || v.Running || v.Paused || v.Overtime {
		t.Fatalf("blank snapshot runtime = %+v", v)
	}
	if tf := timerOf(t, e); !tf.Blank || tf.RemainingMS != 0 || tf.Overtime {
		t.Fatalf("blank Timer frame = %+v", tf)
	}
}

// TestCountStopCountsUpAfterZero: COUNTSTOP "remaining" is the elapsed
// counting UP past zero — runtime never end-states, no overtime flag, and
// alerts stay dead even with thresholds configured.
func TestCountStopCountsUpAfterZero(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, func(cues []Cue) {
		cues[0].TimerKind = TimerCountStop
		cues[0].Alert1MS = 1000 // configured thresholds must stay dead
		cues[0].Alert2MS = 1000
	})
	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clk.ms = t0 + 700_000 // 100 s past the cue's 10 min duration
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if rt := e.Runtime(); !rt.Running || rt.Paused {
		t.Fatalf("countstop end-stated: %+v", rt)
	}
	snap, err := e.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	v := snap.Runtime
	if v.RemainingMS != 700_000 || v.Overtime || v.AlertState != 0 || v.AlertColor != "" {
		t.Fatalf("countstop snapshot = %+v, want remaining 700000 counting up, no alerts", v)
	}
	if tf := timerOf(t, e); tf.RemainingMS != 700_000 || tf.Overtime || tf.AlertState != 0 {
		t.Fatalf("countstop Timer = %+v", tf)
	}

	// Direct formula check past zero (engine.go:130-132 mirror).
	up := &Cue{Pos: 1, DurationMS: 60_000, TimerKind: TimerCountStop, Alert1MS: 1000, Alert2MS: 1000}
	if got, ot, al := DisplayedRemaining(up, Runtime{Running: true, AnchorTS: 1000, Rate: 1.0}, 99_999); got != 98_999 || ot || al != 0 {
		t.Fatalf("countstop formula past zero: %d %v %d", got, ot, al)
	}
}

// TestAlertSaturationUnchangedWhenHeld traps the chosen REVIEW-3 D4
// semantic: the saturation logic keys on the ESTIMATED time, not a live
// crossing — the same DisplayedRemaining input shape a held cue presents.
func TestAlertSaturationUnchangedWhenHeld(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, nil)
	if err := e.Start(5); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clk.ms = t0 + 200_000
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	before := timerOf(t, e).AlertState
	// Replay the exact held shape through the pure function: saturated.
	held := Runtime{PausedElapsedMS: 100_000}
	if _, _, got := DisplayedRemaining(&testCues()[4], held, 9_999_999); got != 2 || before != 2 {
		t.Fatalf("saturation on held shape = %d (live %d)", got, before)
	}
	// An armed cue (elapsed 0) is state 0 — armed/never-crossed stays clean.
	if _, _, got := DisplayedRemaining(&testCues()[4], Runtime{}, 9_999_999); got != 0 {
		t.Fatalf("armed alert = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Schedule computation (breaks + holds)

func TestComputeSchedule(t *testing.T) {
	day := int64(1_700_000_000_000)
	s := ComputeSchedule(testCues(), day, 1.0)

	// Planned: Welcome 10 min + hold 0 → Break at 10 min? No: pos3 is the
	// Break row itself (5 min) with hold 2 min; pos4 Slot 1 min; pos5 100 s.
	want := []struct {
		pos     int64
		startMS int64
		endMS   int64
		holdMS  int64
		cumHold int64
		brk     bool
	}{
		{1, 0, 600_000, 0, 0, false},
		{2, 600_000, 1_800_000, 0, 0, false},
		{3, 1_800_000, 2_100_000, 0, 0, true}, // stored hold 2 min moves nothing (U42)
		{4, 2_100_000, 2_160_000, 0, 300_000, false},
		{5, 2_160_000, 2_260_000, 0, 300_000, false},
	}
	if len(s.Rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(s.Rows), len(want))
	}
	for i, w := range want {
		r := s.Rows[i]
		if r.Pos != w.pos || r.StartMS != w.startMS || r.EndMS != w.endMS ||
			r.HoldMS != w.holdMS || r.CumHoldMS != w.cumHold || r.Break != w.brk {
			t.Fatalf("row %d = %+v, want pos=%d start=%d end=%d hold=%d cum=%d brk=%v",
				i, r, w.pos, w.startMS, w.endMS, w.holdMS, w.cumHold, w.brk)
		}
		if r.StartTS != day+w.startMS || r.EndTS != day+w.endMS {
			t.Fatalf("row %d ts: %d..%d", i, r.StartTS, r.EndTS)
		}
	}
	if s.TotalMS != 2_260_000 || s.EndTS != day+2_260_000 {
		t.Fatalf("total: %+v", s)
	}
	if s.HoldsMS != 0 || s.BreaksMS != 300_000 {
		t.Fatalf("holds/breaks: %d/%d, want 0/300000", s.HoldsMS, s.BreaksMS)
	}
}

func TestComputeScheduleRuntime(t *testing.T) {
	day := int64(1_700_000_000_000)
	cues := testCues()

	// Started on time: the projected actual end is the scheduled end (delta 0).
	rt := Runtime{ActivePos: 1, Running: true, AnchorTS: day, Rate: 1.0, DayStartTS: day}
	s := ComputeScheduleRuntime(cues, rt, day+100_000)
	if s.Rows[0].ActualEndTS != day+600_000 || s.Rows[0].DeltaMS != 0 {
		t.Fatalf("on time: %+v", s.Rows[0])
	}

	// Started 100 s late (anchor after the scheduled start): over by 100 s.
	late := Runtime{ActivePos: 2, Running: true, AnchorTS: day + 700_000, Rate: 1.0, DayStartTS: day}
	s2 := ComputeScheduleRuntime(cues, late, day+800_000)
	if s2.Rows[1].ActualEndTS != day+1_900_000 || s2.Rows[1].DeltaMS != 100_000 {
		t.Fatalf("late start: %+v", s2.Rows[1])
	}

	// OVERTIME in progress: no projected end; delta is how far past the
	// scheduled end we already are.
	over := Runtime{ActivePos: 2, Running: true, AnchorTS: day + 600_000, Rate: 1.0, DayStartTS: day}
	s3 := ComputeScheduleRuntime(cues, over, day+1_900_000)
	if s3.Rows[1].ActualEndTS != 0 || s3.Rows[1].DeltaMS != 100_000 {
		t.Fatalf("overtime delta: %+v", s3.Rows[1])
	}
}
