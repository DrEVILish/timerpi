package routes_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"timerpi/boards"
	"timerpi/timerpi"
)

// Display type + rotation are stored per screen, validated, and rendered
// on the screen's page; templates give a screen its own layout.
func TestScreenLookAndTemplate(t *testing.T) {
	ts := newAPITest(t)
	if err := boards.Migrate(ts.db.DB); err != nil {
		t.Fatal(err)
	}
	cfg := func(body string) int {
		code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/screens/config", []byte(body), "application/json")
		return code
	}
	if code := cfg(`{"name":"Foyer","kind":"walkin","rotation":90}`); code != 200 {
		t.Fatalf("config look: %d", code)
	}
	if code := cfg(`{"name":"Foyer","rotation":45}`); code != 400 {
		t.Errorf("rotation 45 accepted: %d", code)
	}
	if code := cfg(`{"name":"Foyer","kind":"billboard"}`); code != 400 {
		t.Errorf("unknown display type accepted: %d", code)
	}
	scr, err := ts.db.GetScreenByName(ts.showID, "Foyer")
	if err != nil || scr.Kind != "walkin" || scr.Rotation != 90 {
		t.Fatalf("stored look: %+v %v", scr, err)
	}
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/screens/template", []byte(`{"name":"Foyer","template":"room-portrait"}`), "application/json"); code != 200 {
		t.Fatalf("template: %d %s", code, b)
	}
	scr, _ = ts.db.GetScreenByName(ts.showID, "Foyer")
	if scr.Template != "room-portrait" || scr.BoardID != 0 {
		t.Fatalf("screen should show the built-in directly: %+v", scr)
	}
	// The screen's page renders the built-in rotated, on its portrait canvas.
	_, page := ts.anon("GET", "/d/"+ts.showCode+"?view=board&tpl=room-portrait&screen=Foyer", nil, "")
	for _, want := range []string{`data-rotate="90"`, `data-orientation="portrait"`, `--b-rows: 16`, `data-kind="walkin"`} {
		if !strings.Contains(string(page), want) {
			t.Errorf("screen page missing %q", want)
		}
	}
	if code, _ := ts.anon("POST", "/api/shows/"+ts.showCode+"/screens/config", []byte(`{"name":"Foyer","rotation":0}`), "application/json"); code != 401 {
		t.Errorf("anonymous screen config: %d, want 401", code)
	}
}

// The walk-in feed shows every room of the event from the live engines,
// and is open to screens.
func TestWalkinFeed(t *testing.T) {
	ts := newAPITest(t)
	other := ts.newRoom("Room B")
	for _, c := range []timerpi.Cue{{Label: "Keynote", DurationMS: 600000, Speaker: "Ann"}, {Label: "Panel", DurationMS: 600000}} {
		if _, err := ts.db.CreateCue(ts.showID, c); err != nil {
			t.Fatal(err)
		}
	}
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/cmd/go", []byte(`{}`), "application/json"); code != 200 {
		t.Fatalf("go: %d %s", code, b)
	}
	code, raw := ts.anon("GET", "/api/shows/"+other.Code+"/walkin", nil, "")
	if code != 200 {
		t.Fatalf("walkin: %d %s", code, raw)
	}
	var out struct {
		Rooms []struct {
			Name string                  `json:"name"`
			Here bool                    `json:"here"`
			Now  *struct{ Label string } `json:"now"`
			Next *struct{ Label string } `json:"next"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Rooms) != 2 {
		t.Fatalf("walkin body: %s", raw)
	}
	a := out.Rooms[0]
	if a.Now == nil || a.Now.Label != "Keynote" || a.Next == nil || a.Next.Label != "Panel" || a.Here {
		t.Errorf("room A: %+v", a)
	}
	if !out.Rooms[1].Here {
		t.Errorf("room B should be 'here': %+v", out.Rooms[1])
	}
	if strings.Contains(string(raw), ts.showCode) {
		t.Error("walk-in feed leaked a room code")
	}
}

// Capture sets type + rotation, and only into a room the operator moderates.
func TestCaptureLookAndAccess(t *testing.T) {
	ts := newAPITest(t)
	if err := boards.Migrate(ts.db.DB); err != nil {
		t.Fatal(err)
	}
	ts.anon("POST", "/api/waiting/register", []byte(`{"name":"TV-9","host":"tv9.local"}`), "application/json")
	body := fmt.Sprintf(`{"code":%q,"name":"Poster","kind":"walkin","rotation":270,"template":"event-portrait"}`, ts.showCode)
	if code, _ := ts.anon("POST", "/api/waiting/1/capture", []byte(body), "application/json"); code != 401 {
		t.Errorf("anonymous capture: %d, want 401", code)
	}
	if code, b := ts.call("POST", "/api/waiting/1/capture", []byte(body), "application/json"); code != 200 {
		t.Fatalf("capture: %d %s", code, b)
	}
	scr, err := ts.db.GetScreenByName(ts.showID, "Poster")
	if err != nil || scr.Kind != "walkin" || scr.Rotation != 270 || scr.Template != "event-portrait" {
		t.Fatalf("captured look: %+v %v", scr, err)
	}
}
