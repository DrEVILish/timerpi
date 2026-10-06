package routes_test

import (
	"strings"
	"testing"

	"timerpi/timerpi"
)

// STATUS U42 (owner, 2026-10-06): auto-start, auto-continue and Hold after
// are gone from the room page: no inspector inputs, no row chips.
func TestAutoOptionsGoneU42(t *testing.T) {
	ts := newAPITest(t)
	cue, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Timed", DurationMS: 60_000})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	cue.StartAt = "09:30"
	cue.AutoContinue = true
	cue.HoldMS = 60_000
	if _, err := ts.db.UpdateCue(ts.showID, cue); err != nil {
		t.Fatalf("set options: %v", err)
	}
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	for _, sub := range []string{`id="tp-insp-startAt"`, `id="tp-insp-autoContinue"`, `id="tp-insp-hold"`, "⏰ 09:30", ">AUTO<", " hold<"} {
		if strings.Contains(string(body), sub) {
			t.Errorf("room page still shows %q", sub)
		}
	}
}
