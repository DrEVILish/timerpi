package routes_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"timerpi/timerpi"
)

// snapshotOf fetches GET /api/shows/:code as a map (snapshot() only knows
// the harness show).
func snapshotOf(t *testing.T, ts *apiTest, code string) map[string]any {
	t.Helper()
	c, body := ts.call("GET", "/api/shows/"+code, nil, "")
	if c != 200 {
		t.Fatalf("snapshot %s: %d %s", code, c, body)
	}
	var snap map[string]any
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatalf("snapshot json: %v (%s)", err, body)
	}
	return snap
}

// POST /:ident/clone duplicates the day: fresh code, same cues in order,
// notes + day-start carried over, no passphrase, stopped runtime.
func TestCloneShowE1(t *testing.T) {
	ts := newAPITest(t)

	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Opener", DurationMS: 60_000}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Closer", DurationMS: 120_000}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := ts.db.SetShowNotes(ts.showID, "water at 14:00"); err != nil {
		t.Fatalf("notes: %v", err)
	}

	code, body := ts.call("POST", "/api/shows/"+ts.showCode+"/clone", []byte(`{"title":"Friday II"}`), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("clone: %d %s", code, body)
	}
	var out struct {
		Code     string `json:"code"`
		Title    string `json:"title"`
		CueCount int    `json:"cueCount"`
		Control  string `json:"control"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !timerpi.ValidCode(out.Code) || out.Code == ts.showCode {
		t.Errorf("clone code = %q (want a fresh valid code)", out.Code)
	}
	if out.Title != "Friday II" || out.CueCount != 2 || out.Control != "/c/"+out.Code {
		t.Errorf("clone body = %+v", out)
	}

	// The clone's running order matches the source, source untouched.
	snap := snapshotOf(t, ts, out.Code)
	var labels []string
	for _, c := range snap["cues"].([]any) {
		labels = append(labels, c.(map[string]any)["label"].(string))
	}
	if len(labels) != 2 || labels[0] != "Opener" || labels[1] != "Closer" {
		t.Errorf("clone cues = %v", labels)
	}
	if show := snap["show"].(map[string]any); show["notes"] != "water at 14:00" {
		t.Errorf("clone notes = %v", show["notes"])
	}
	if src := ts.snapshot()["cues"].([]any); len(src) != 2 {
		t.Errorf("source cues changed by clone: %v", src)
	}
}

// Empty title defaults to "Copy of <src>"; the clone never inherits the
// passphrase (re-lock is explicit).
func TestCloneShowDefaultsE1(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Only", DurationMS: 60_000}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, _ := postJSONWithCookies(t, ts, "/api/shows/"+ts.showCode+"/passphrase", `{"pw":"secret"}`)
	if res.StatusCode != 200 {
		t.Fatalf("lock source: %d", res.StatusCode)
	}
	var cook *http.Cookie
	for _, ck := range res.Cookies() {
		if strings.HasPrefix(ck.Name, "tp_show_") {
			cook = ck
		}
	}
	if cook == nil {
		t.Fatal("no unlock cookie on lock")
	}

	res, raw := postJSONWithCookies(t, ts, "/api/shows/"+ts.showCode+"/clone", `{}`, cook)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("clone locked source: %d %s", res.StatusCode, raw)
	}
	var out struct {
		Code  string `json:"code"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Title != "Copy of API Test Show" {
		t.Errorf("default title = %q", out.Title)
	}
	// Unlocked: the clone answers snapshots with no cookie.
	if code, _ := ts.call("GET", "/api/shows/"+out.Code, nil, ""); code != 200 {
		t.Errorf("clone inherited the gate: %d", code)
	}
}

// The dashboard ships the clone affordance (template-ship contract for
// pure-JS features).
func TestCloneFormShipsE1(t *testing.T) {
	ts := newAPITest(t)
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	for _, sub := range []string{`id="show-clone-form"`, `id="show-clone-title"`, `Clone day`} {
		if !strings.Contains(string(body), sub) {
			t.Errorf("dashboard missing %q", sub)
		}
	}
}

// E6 presets ride the quick-add form (fill m:ss, operator still Adds).
func TestQuickAddPresetsShipE6(t *testing.T) {
	ts := newAPITest(t)
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	for _, sub := range []string{`data-preset-mss="1"`, `data-preset-mss="5"`, `data-preset-mss="10"`} {
		if !strings.Contains(string(body), sub) {
			t.Errorf("dashboard missing preset %q", sub)
		}
	}
}
