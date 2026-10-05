// daystart_test.go — B4: the "HH:MM" parser and the construction-time
// auto-anchor (a rebooted appliance must not leave a scheduled day unanchored).
package timerpi

import (
	"testing"
	"time"
)

func TestDayStartTSFrom(t *testing.T) {
	now := time.Date(2026, 10, 3, 11, 20, 0, 0, time.Local).UnixMilli()
	cases := []struct {
		in   string // stored setting
		ok   bool   // anchors?
		h, m int
	}{
		{"09:00", true, 9, 0},
		{"20:30", true, 20, 30},
		{" 09:15 ", true, 9, 15},
		{"", false, 0, 0},
		{"24:00", false, 0, 0},
		{"09:60", false, 0, 0},
		{"abc", false, 0, 0},
		{"9", false, 0, 0},
		{":30", false, 0, 0},
	}
	for _, c := range cases {
		got := DayStartTSFrom(c.in, now)
		if !c.ok {
			if got != 0 {
				t.Errorf("DayStartTSFrom(%q) = %d, want 0", c.in, got)
			}
			continue
		}
		gt := time.UnixMilli(got)
		if got == 0 || gt.Hour() != c.h || gt.Minute() != c.m || gt.Day() != 3 {
			t.Errorf("DayStartTSFrom(%q) = %v, want %02d:%02d today", c.in, gt, c.h, c.m)
		}
	}
}

func TestEngineAutoDayStart(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	show, err := db.CreateShow("Scheduled Show")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.CreateCue(show.ID, Cue{Label: "A", DurationMS: 300_000}); err != nil {
		t.Fatalf("cue: %v", err)
	}
	// Store the scheduled start BEFORE the engine is ever built — the
	// reboot-shaped path (Engines.Get constructs fresh).
	if err := db.SetShowDayStart(show.ID, "09:00"); err != nil {
		t.Fatalf("set day start: %v", err)
	}
	engines := NewEngines(db)
	eng, err := engines.Get(show.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	rt := eng.Runtime()
	if rt.DayStartTS == 0 {
		t.Fatal("auto-anchor missing for a scheduled show")
	}
	lt := time.UnixMilli(rt.DayStartTS)
	if lt.Hour() != 9 || lt.Minute() != 0 {
		t.Fatalf("auto-anchored at %v, want 09:00", lt)
	}
}
