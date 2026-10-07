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
	if err := ts.db.SetRoomPassword(ts.showID, "secret"); err != nil {
		t.Fatalf("lock source: %v", err)
	}
	status, raw := ts.call("POST", "/api/shows/"+ts.showCode+"/clone", []byte(`{}`), "application/json")
	if status != http.StatusCreated {
		t.Fatalf("clone locked source: %d %s", status, raw)
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
	// The clone is a room of the same event, without the source's password.
	src, _ := ts.db.GetShow(ts.showID)
	id, ok := timerpi.ResolveShowID(ts.db, out.Code)
	dst, _ := ts.db.GetShow(id)
	if !ok || dst.EventID != src.EventID || dst.RoomPW != "" {
		t.Errorf("clone: event %d (want %d), password inherited %v", dst.EventID, src.EventID, dst.RoomPW != "")
	}
}

// Duplicate lives on the SuperOperator dashboard's rooms table (STATUS
// U29: the room page's Setup tab is gone).
func TestCloneFormShipsE1(t *testing.T) {
	ts := newAPITest(t)
	_, body := ts.call("GET", "/e/"+ts.eventCode+"/admin", nil, "")
	if !strings.Contains(string(body), `data-room-dup`) {
		t.Error("SuperOperator dashboard has no Duplicate button")
	}
	_, room := ts.call("GET", "/c/"+ts.showCode, nil, "")
	if strings.Contains(string(room), `show-clone-form`) {
		t.Error("the room page still has the old Duplicate form")
	}
}

// STATUS U39: new cues are added from the running order's footer row,
// with the table's columns, tied to #tp-add-form outside the swapped list.
func TestAddRowShipsU39(t *testing.T) {
	ts := newAPITest(t)
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	for _, sub := range []string{`<form id="tp-add-form"`, `<tr class="is-editing">`} {
		if !strings.Contains(string(body), sub) {
			t.Errorf("dashboard missing %q", sub)
		}
	}
	for _, name := range []string{"kind", "label", "who", "mss", "timerKind", "endAction", "alert1", "alertColor1", "alert2", "alertColor2", "notes"} {
		if !strings.Contains(string(body), `name="`+name+`" form="tp-add-form"`) {
			t.Errorf("add row missing the %s field", name)
		}
	}
	if strings.Contains(string(body), "tp-quick-add") {
		t.Error("the old quick-add form is still in the header")
	}
}
