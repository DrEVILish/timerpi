package importdocs

import (
	"encoding/json"
	"strings"
	"testing"

	"timerpi/timerpi"
)

// BUGLOG RW28: a JSON export re-imports with its timer kind, auto-start
// time and break location intact (stopwatch sessions used to come back as
// countdowns).
func TestJSONRoundTripKeepsTimerKindStartAndLocation(t *testing.T) {
	src := []timerpi.Cue{
		{Label: "Stopwatch", DurationMS: 60000, TimerKind: timerpi.TimerCountStop, StartAt: "09:30"},
		{Label: "Coffee", DurationMS: 900000, Kind: timerpi.KindBreak, TimerKind: timerpi.TimerClock, Location: "Great Hall"},
	}
	raw, err := json.Marshal(FromTimerpiCues(src))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseJSON(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := ToTimerpiCues(parsed)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got[0].TimerKind != timerpi.TimerCountStop || got[0].StartAt != "09:30" {
		t.Errorf("cue 1 = kind %q start %q, want COUNTSTOP 09:30", got[0].TimerKind, got[0].StartAt)
	}
	if got[1].TimerKind != timerpi.TimerClock || got[1].Location != "Great Hall" {
		t.Errorf("cue 2 = kind %q location %q, want CLOCK Great Hall", got[1].TimerKind, got[1].Location)
	}
}

// BUGLOG RW30: durations over 7 days are refused in every form, instead of
// overflowing int64 (MaxInt64 on the Pi, so schedule times went negative).
func TestDurationsOverSevenDaysRefused(t *testing.T) {
	for _, s := range []string{"99999999999999999999h", "169h", "10000:00:00", "1e400"} {
		if _, err := ParseDurationMS(s); err == nil {
			t.Errorf("ParseDurationMS(%q) accepted", s)
		}
	}
	if ms, err := ParseDurationMS("168h"); err != nil || ms != 7*24*3600*1000 {
		t.Errorf("168h = %d, %v; want exactly 7 days", ms, err)
	}
	for _, doc := range []string{`[{"label":"x","durationMS":1e30}]`, `[{"label":"x","hold":"200h"}]`} {
		if _, err := ParseJSON([]byte(doc)); err == nil || !strings.Contains(err.Error(), "7 days") {
			t.Errorf("ParseJSON(%s) err = %v, want the 7-day refusal", doc, err)
		}
	}
	if _, err := ParseCSV([]byte("label,duration\nTalk,500h\n")); err == nil {
		t.Error("CSV with a 500h duration accepted")
	}
	c := timerpi.Cue{Label: "x", DurationMS: timerpi.MaxDurationMS + 1}
	c.Normalize()
	if err := c.Validate(); err == nil {
		t.Error("Cue.Validate accepted a duration over 7 days")
	}
}
