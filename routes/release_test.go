// STATUS N12 (VENUE-CLOUD §3): an event's screens are released 4 hours
// after its end, never while a timer runs; boxes learn it from
// /api/pairing/status.
package routes_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"timerpi/timerpi"
)

func TestEventEndReleasesScreens(t *testing.T) {
	ts := newAPITest(t)
	key, err := ts.db.ScreenKey(ts.showID, "Hall")
	if err != nil || key == "" {
		t.Fatal(err)
	}
	ends := time.Now().Add(-5 * time.Hour).UnixMilli()
	if code, b := ts.call("PATCH", "/api/events/"+ts.eventCode, []byte(`{"endsAt":`+jsonInt(ends)+`}`), "application/json"); code != 200 {
		t.Fatalf("set end: %d %s", code, b)
	}

	// A running timer holds the release back.
	for _, label := range []string{"Talk", "Questions"} {
		if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: label, DurationMS: 600_000}); err != nil {
			t.Fatal(err)
		}
	}
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/cmd/go", nil, ""); code != 200 {
		t.Fatalf("go: %d %s", code, b)
	}
	if got := ts.deps.ReleaseDue(); len(got) != 0 {
		t.Fatal("released while a timer was running")
	}
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/cmd/pause", nil, ""); code != 200 {
		t.Fatalf("pause: %d %s", code, b)
	}
	if got := ts.deps.ReleaseDue(); len(got) != 0 || !ts.db.ScreenKeyValid(ts.showID, "Hall", key) {
		t.Fatal("a paused cue can still resume, so its screen must stay assigned")
	}
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/cmd/next", nil, ""); code != 200 {
		t.Fatalf("next: %d %s", code, b)
	}
	if _, err := ts.db.Exec(`CREATE TRIGGER reject_screen_release BEFORE DELETE ON screens
		BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if got := ts.deps.ReleaseDue(); len(got) != 0 || !ts.db.ScreenKeyValid(ts.showID, "Hall", key) {
		t.Fatal("failed release discarded a screen")
	}
	if ev, ok := ts.db.ResolveEvent(ts.eventCode); !ok || ev.ReleasedAt != 0 {
		t.Fatal("event marked released despite failed screen cleanup")
	}
	if _, err := ts.db.Exec(`DROP TRIGGER reject_screen_release`); err != nil {
		t.Fatal(err)
	}
	if got := ts.deps.ReleaseDue(); len(got) != 1 {
		t.Fatalf("not released after end + 4 h: %v", got)
	}
	if ts.db.ScreenKeyValid(ts.showID, "Hall", key) {
		t.Fatal("the screen kept its key after release")
	}
	if got := ts.deps.ReleaseDue(); len(got) != 0 {
		t.Fatal("released twice")
	}
	_, b := ts.anon("GET", "/api/pairing/status?event="+ts.eventCode, nil, "")
	var st struct {
		Exists, Released bool
		EndsAt           int64
	}
	_ = json.Unmarshal(b, &st)
	if !st.Exists || !st.Released || st.EndsAt != ends {
		t.Fatalf("status = %s", b)
	}

	// Extending the event clears the release.
	later := time.Now().Add(time.Hour).UnixMilli()
	ts.call("PATCH", "/api/events/"+ts.eventCode, []byte(`{"endsAt":`+jsonInt(later)+`}`), "application/json")
	if _, b := ts.anon("GET", "/api/pairing/status?event="+ts.eventCode, nil, ""); !strings.Contains(string(b), `"released":false`) {
		t.Fatalf("extended event still released: %s", b)
	}
	if _, b := ts.anon("GET", "/api/pairing/status?event=NOPE1234", nil, ""); !strings.Contains(string(b), `"exists":false`) {
		t.Fatalf("unknown event: %s", b)
	}
}

func TestReplicaLeavesEventReleaseToPrimary(t *testing.T) {
	ts := newAPITest(t)
	ev, ok := ts.db.ResolveEvent(ts.eventCode)
	if !ok {
		t.Fatal("event not found")
	}
	if err := ts.db.SetEventEnd(ev.ID, time.Now().Add(-5*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	key, err := ts.db.ScreenKey(ts.showID, "Hall")
	if err != nil {
		t.Fatal(err)
	}
	for _, primary := range []string{"", "http://primary.local"} {
		ts.deps.BoxWriteTarget = func() (string, bool) { return primary, false }
		if released := ts.deps.ReleaseDue(); len(released) != 0 || !ts.db.ScreenKeyValid(ts.showID, "Hall", key) {
			t.Fatalf("replica released the event with primary %q", primary)
		}
	}
	ts.deps.BoxWriteTarget = func() (string, bool) { return "", true }
	if released := ts.deps.ReleaseDue(); len(released) != 1 || ts.db.ScreenKeyValid(ts.showID, "Hall", key) {
		t.Fatal("promoted primary did not release the expired event")
	}
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }
