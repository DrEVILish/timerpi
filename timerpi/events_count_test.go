package timerpi

import "testing"

// BUGLOG RW6: a failing count is an error, not "no protected events".
func TestCountProtectedEventsReportsErrors(t *testing.T) {
	d := openTestDB(t)
	if n, err := d.CountProtectedEvents(); err != nil || n != 0 {
		t.Fatalf("fresh box: n=%d err=%v", n, err)
	}
	if _, err := d.Exec(`DROP TABLE events`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CountProtectedEvents(); err == nil {
		t.Fatal("query failure read as zero events")
	}
}
