package routes_test

import (
	"strings"
	"testing"

	"timerpi/timerpi"
)

// E5 surfaces: the inspector ships the auto-start input; a cue with
// startAt renders the ⏰ chip on its row.
func TestAutoStartSurfacesE5(t *testing.T) {
	ts := newAPITest(t)
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	if !strings.Contains(string(body), `id="tp-insp-startAt"`) {
		t.Error("inspector missing tp-insp-startAt input")
	}

	cue, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Timed", DurationMS: 60_000})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	cue.StartAt = "09:30"
	if _, err := ts.db.UpdateCue(ts.showID, cue); err != nil {
		t.Fatalf("set startAt: %v", err)
	}
	_, body = ts.call("GET", "/c/"+ts.showCode, nil, "")
	if !strings.Contains(string(body), "⏰ 09:30") {
		t.Errorf("timed row missing AUTO chip: %.400s", body)
	}
}
