package timerpi

import (
	"fmt"
	"testing"
)

// BUGLOG RS27: adopting pre-event shows is all-or-nothing, so a failure
// half-way leaves no empty events behind and the retry adopts each show
// exactly once.
func TestAdoptOrphansIsAtomic(t *testing.T) {
	d := openTestDB(t)
	a := mustCreateShow(t, d, "Old A")
	b := mustCreateShow(t, d, "Old B")
	count := func() int {
		n, err := d.CountEvents()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	if _, err := d.Exec(fmt.Sprintf(`CREATE TRIGGER boom BEFORE UPDATE OF event_id ON shows WHEN NEW.id = %d BEGIN SELECT RAISE(ABORT, 'boom'); END`, b.ID)); err != nil {
		t.Fatal(err)
	}
	if err := d.adoptOrphanShows(); err == nil {
		t.Fatal("adoption should fail on the second show")
	}
	if n := count(); n != before {
		t.Fatalf("a failed adoption left %d events behind", n-before)
	}
	if _, err := d.Exec(`DROP TRIGGER boom`); err != nil {
		t.Fatal(err)
	}
	if err := d.adoptOrphanShows(); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != before+2 {
		t.Errorf("retry made %d events, want 2", n-before)
	}
	for _, id := range []int64{a.ID, b.ID} {
		if sh, _ := d.GetShow(id); sh.EventID == 0 {
			t.Errorf("show %d not adopted", id)
		}
	}
}
