package routes_test

import (
	"strings"
	"testing"

	"timerpi/routes"
	"timerpi/timerpi"
)

// BUGLOG RS7: names are clipped (an 8 MiB name used to be stored), event
// creation is budgeted per device, and an event holds at most 100 rooms.
func TestCreationLimits(t *testing.T) {
	ts := newAPITest(t)
	long := strings.Repeat("é", 5000)
	code, body := ts.anon("POST", "/api/events", []byte(`{"name":"`+long+`","password":"secret-pw"}`), "application/json")
	if code != 200 && code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	evs, _ := ts.db.ListEvents()
	for _, e := range evs {
		if len(e.Name) > 120 {
			t.Errorf("event name stored with %d bytes", len(e.Name))
		}
	}

	old := routes.SetEventCreateBudget(2)
	defer routes.SetEventCreateBudget(old)
	var last int
	for i := 0; i < 3; i++ {
		last, _ = ts.anon("POST", "/api/events", []byte(`{"name":"Spam","password":"secret-pw"}`), "application/json")
	}
	if last != 429 {
		t.Errorf("third event from one device in the window: %d, want 429", last)
	}

	for i := 0; i < 99; i++ { // the test event already has one room
		if code, b := ts.call("POST", "/api/events/"+ts.eventCode+"/rooms", []byte(`{"name":"R"}`), "application/json"); code != 201 {
			t.Fatalf("room %d: %d %s", i+2, code, b)
		}
	}
	if code, _ := ts.call("POST", "/api/events/"+ts.eventCode+"/rooms", []byte(`{"name":"One too many"}`), "application/json"); code != 409 {
		t.Errorf("room 101: %d, want 409", code)
	}
}

// BUGLOG RS8: a malformed body on a transport command is refused instead
// of starting the armed or first session; an empty one still means "no
// args".
func TestCmdRefusesMalformedJSON(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "A", DurationMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/cmd/start", []byte(`{"pos":`), "application/json"); code != 400 {
		t.Errorf("malformed body: %d, want 400", code)
	}
	if rt, _, _ := ts.db.LoadRuntime(ts.showID); rt.Running {
		t.Error("a malformed command started a session")
	}
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/cmd/start", nil, "application/json"); code != 200 {
		t.Errorf("empty body: %d %s, want 200", code, b)
	}
}

// BUGLOG RS16: an unknown theme name is refused, not saved as the default.
func TestUnknownThemeRefused(t *testing.T) {
	ts := newAPITest(t)
	if code, _ := ts.call("POST", "/api/theme", []byte(`{"theme":"no-such-theme"}`), "application/json"); code != 400 {
		t.Errorf("box theme: %d, want 400", code)
	}
	if code, _ := ts.call("POST", "/api/theme", []byte(`{"theme":"blue-future"}`), "application/json"); code != 200 {
		t.Errorf("installed theme: %d, want 200", code)
	}
	if code, _ := ts.call("PATCH", "/api/events/"+ts.eventCode, []byte(`{"theme":"no-such-theme"}`), "application/json"); code != 400 {
		t.Errorf("event theme: %d, want 400", code)
	}
}
