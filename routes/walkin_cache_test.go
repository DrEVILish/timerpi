package routes_test

import (
	"strings"
	"testing"
	"time"
)

// BUGLOG RW57: the walk-in feed is built once per event per 2 s and shared
// by every screen polling it, then refreshed.
func TestWalkinFeedCachedBriefly(t *testing.T) {
	ts := newAPITest(t)
	other := ts.newRoom("Annex")
	get := func(code string) string {
		t.Helper()
		c, raw := ts.anon("GET", "/api/shows/"+code+"/walkin", nil, "")
		if c != 200 {
			t.Fatalf("walkin: %d %s", c, raw)
		}
		return string(raw)
	}
	get(ts.showCode)
	if err := ts.db.RenameShow(other.ID, "Renamed Annex"); err != nil {
		t.Fatal(err)
	}
	if body := get(other.Code); strings.Contains(body, "Renamed Annex") || !strings.Contains(body, `"here":true`) {
		t.Errorf("within 2 s the feed should be the cached one (with this screen's room marked): %s", body)
	}
	time.Sleep(2100 * time.Millisecond)
	if body := get(ts.showCode); !strings.Contains(body, "Renamed Annex") {
		t.Errorf("after 2 s the feed should be rebuilt: %s", body)
	}
}
