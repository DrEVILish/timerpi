package routes_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"timerpi/timerpi"
)

// E4 over REST: structural/automation endpoints log with the api actor;
// the JSON tail serves newest-first; the dashboard renders the panel.
func TestActionLogRestE4(t *testing.T) {
	ts := newAPITest(t)

	code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/moveto", []byte(`{"pos":1,"to":1}`), "application/json")
	if code != http.StatusOK && code != http.StatusBadRequest {
		t.Fatalf("moveto: %d", code)
	}
	// Seed one cue so the move (and its log row) is real.
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "A", DurationMS: 60_000}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	code, _ = ts.call("POST", "/api/shows/"+ts.showCode+"/moveto", []byte(`{"pos":1,"to":1}`), "application/json")
	if code != http.StatusOK {
		t.Fatalf("moveto same-slot: %d", code)
	}

	code, body := ts.call("GET", "/api/shows/"+ts.showCode+"/actions?limit=5", nil, "")
	if code != http.StatusOK {
		t.Fatalf("actions tail: %d %s", code, body)
	}
	var out struct {
		Actions []struct {
			Actor  string `json:"actor"`
			Action string `json:"action"`
			Detail string `json:"detail"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Actions) == 0 || out.Actions[0].Action != "cueMove" || out.Actions[0].Actor != "api" {
		t.Fatalf("tail = %+v, want api cueMove first", out.Actions)
	}

	// No Recent Actions section ships on the dashboard (owner decision
	// 2026-10-05) — the log lives in the DB + JSON tail for debugging.
	_, body = ts.call("GET", "/c/"+ts.showCode, nil, "")
	if strings.Contains(string(body), `id="actions-panel"`) {
		t.Error("dashboard ships a removed actions panel")
	}
}
