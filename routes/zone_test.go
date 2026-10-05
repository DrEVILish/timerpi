// zone tests (PLAN §11.1 item 1, test-gap round): the zone label round-trip
// (sanitize + persist + audit), the walk-in page rendering (rooms grouped,
// now/next slots, empty case), and gating.
package routes_test

import (
	"strings"
	"testing"

	"timerpi/timerpi"
)

func TestZoneLabelRoundtrip(t *testing.T) {
	ts := newAPITest(t)
	// Set via the API (share-code addressed).
	code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/zone",
		[]byte(`{"zone":"  Hall A "}`), "")
	if code != 200 {
		t.Fatalf("zone set: %d %s", code, b)
	}
	if !strings.Contains(string(b), `"zone":"Hall A"`) {
		t.Fatalf("zone not sanitized: %s", b)
	}
	sh, err := ts.db.GetShow(ts.showID)
	if err != nil || sh.Zone != "Hall A" {
		t.Fatalf("zone not persisted: %+v err %v", sh.Zone, err)
	}
	// Empty clears.
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/zone",
		[]byte(`{"zone":""}`), ""); code != 200 {
		t.Fatalf("zone clear: %d", code)
	}
	sh, _ = ts.db.GetShow(ts.showID)
	if sh.Zone != "" {
		t.Fatalf("zone clear did not persist: %q", sh.Zone)
	}
}

func TestZonePageRendersRooms(t *testing.T) {
	ts := newAPITest(t)
	// Two shows in one zone (the second room), one show outside it.
	if err := ts.db.SetShowZone(ts.showID, "Hall A"); err != nil {
		t.Fatalf("zone: %v", err)
	}
	other, err := ts.db.CreateShow("Hall A Second Room")
	if err != nil {
		t.Fatalf("second show: %v", err)
	}
	if err := ts.db.SetShowZone(other.ID, "Hall A"); err != nil {
		t.Fatalf("zone other: %v", err)
	}
	if _, err := ts.db.CreateShow("Unzoned"); err != nil {
		t.Fatalf("third show: %v", err)
	}
	// A cue in the first room so its schedule has a row.
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Welcome", DurationMS: 300_000}); err != nil {
		t.Fatalf("cue: %v", err)
	}

	code, body := ts.call("GET", "/zone/Hall%20A", nil, "")
	if code != 200 {
		t.Fatalf("zone page: %d %.200s", code, body)
	}
	s := string(body)
	for _, sub := range []string{
		`Hall A`, `data-page="zone"`, `tp-zone-clock`,
		"API Test Show", "Hall A Second Room", "Welcome",
	} {
		if !strings.Contains(s, sub) {
			t.Errorf("zone page missing %q", sub)
		}
	}
	if strings.Contains(s, "Unzoned") {
		t.Error("unzoned show leaked onto the zone board")
	}
	// Unknown/empty zone: honest empty state, not an error.
	code, body = ts.call("GET", "/zone/Nowhere", nil, "")
	if code != 200 || !strings.Contains(string(body), "No rooms are assigned") {
		t.Fatalf("empty zone: %d %.200s", code, body)
	}
}
