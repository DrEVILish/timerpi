package timerpi

import (
	"errors"
	"testing"
	"time"
)

// at builds a local wall-clock instant (ms) on day d of October 2026.
func at(d, h, m int) int64 {
	return time.Date(2026, 10, d, h, m, 0, 0, time.Local).UnixMilli()
}

func TestClockAtCrossesMidnight(t *testing.T) {
	anchor := at(6, 18, 0) // a day that starts at 18:00
	if got := ClockAt("23:30", anchor, anchor); got != at(6, 23, 30) {
		t.Errorf("23:30 → %v", time.UnixMilli(got))
	}
	if got := ClockAt("00:15", anchor, anchor); got != at(7, 0, 15) {
		t.Errorf("00:15 after an 18:00 start should be tomorrow, got %v", time.UnixMilli(got))
	}
	if got := ClockAt("00:00", anchor, anchor); got != at(7, 0, 0) {
		t.Errorf("0:00 → %v", time.UnixMilli(got))
	}
	if got := ClockAt("09:00", 0, at(6, 8, 0)); got != at(6, 9, 0) {
		t.Errorf("unanchored 09:00 → %v", time.UnixMilli(got))
	}
}

// BUGLOG RW25: at 23:50 a session set for 00:15 must not start; it starts
// at 00:15.
func TestAutoStartAfterMidnight(t *testing.T) {
	cues := []Cue{{Pos: 1, Label: "Evening"}, {Pos: 2, Label: "Late", StartAt: "00:15"}}
	anchor := at(6, 18, 0)
	if p := autoStartDue(cues, 1, anchor, at(6, 23, 50)); p != 0 {
		t.Errorf("fired at 23:50 for 00:15: pos %d", p)
	}
	if p := autoStartDue(cues, 1, anchor, at(7, 0, 16)); p != 2 {
		t.Errorf("didn't fire at 00:16: pos %d", p)
	}
}

func rolloverEngine(t *testing.T, now *int64, dayStart string, cues []Cue) *Engine {
	t.Helper()
	e, err := NewEngine(1, EngineDeps{
		Now:  func() int64 { return *now },
		Show: func() (Show, error) { return Show{ID: 1, DayStart: dayStart}, nil },
		Cues: func() ([]Cue, error) { return cues, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// BUGLOG RW26: the next morning an idle room starts a new day — re-anchored
// to its scheduled start, playhead back to the top — but a show running or
// paused past midnight, or within 4 h of its planned end, keeps its day.
func TestDayRollover(t *testing.T) {
	cues := []Cue{{ID: 1, Pos: 1, Label: "A", DurationMS: 4 * 3600_000, TimerKind: TimerCountdown, EndAction: EndHold},
		{ID: 2, Pos: 2, Label: "B", DurationMS: 4 * 3600_000, TimerKind: TimerCountdown, EndAction: EndHold}}

	// Day 1 starts 09:00 and plans 8 h (ends 17:00).
	now := at(6, 9, 0)
	e := rolloverEngine(t, &now, "09:00", cues)
	if err := e.SetDayStart(at(6, 9, 0)); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(2); err != nil {
		t.Fatal(err)
	}
	_ = e.Pause()

	// 00:30: paused after midnight, never rolls.
	now = at(7, 0, 30)
	_ = e.Tick(now)
	if rt := e.Runtime(); rt.DayStartTS != at(6, 9, 0) || rt.ActivePos != 2 {
		t.Fatalf("paused show rolled over: %+v", rt)
	}
	// 08:00 next day, now idle: planned end 17:00 + 4 h has passed → day 2.
	_ = e.Reset()
	now = at(7, 8, 0)
	e.lastRollCheck = 0
	_ = e.Tick(now)
	rt := e.Runtime()
	if rt.DayStartTS != at(7, 9, 0) || rt.ActivePos != 0 || rt.Running {
		t.Fatalf("new day: anchor %v pos %d", time.UnixMilli(rt.DayStartTS), rt.ActivePos)
	}
	if err := e.Go(); err != nil || e.Runtime().ActivePos != 1 {
		t.Fatalf("GO on the new day starts the first session: %v %+v", err, e.Runtime())
	}
}

// An evening show: starts 18:00, plans 8 h (to 02:00). Idle at 03:00 it's
// still within the 4 h grace, so the day stays.
func TestNoRolloverWithinGrace(t *testing.T) {
	cues := []Cue{{ID: 1, Pos: 1, Label: "Night", DurationMS: 8 * 3600_000, TimerKind: TimerCountdown, EndAction: EndHold}}
	now := at(6, 18, 0)
	e := rolloverEngine(t, &now, "18:00", cues)
	_ = e.SetDayStart(at(6, 18, 0))
	now = at(7, 3, 0)
	_ = e.Tick(now)
	if rt := e.Runtime(); rt.DayStartTS != at(6, 18, 0) {
		t.Fatalf("rolled within the grace: %v", time.UnixMilli(rt.DayStartTS))
	}
}

// BUGLOG RS23: a day start far from now is refused; 0 clears.
func TestDayStartValidated(t *testing.T) {
	now := at(6, 9, 0)
	e := rolloverEngine(t, &now, "", nil)
	if err := e.ApplyCmd("daystart", map[string]any{"ts": float64(1)}); !errors.Is(err, ErrBadArgs) {
		t.Errorf("ts 1 accepted: %v", err)
	}
	if err := e.ApplyCmd("daystart", map[string]any{"ts": float64(at(6, 8, 30))}); err != nil {
		t.Errorf("today's start refused: %v", err)
	}
	if err := e.ApplyCmd("daystart", map[string]any{"ts": float64(0)}); err != nil {
		t.Errorf("clear refused: %v", err)
	}
}
