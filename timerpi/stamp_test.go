package timerpi

import "testing"

// BUGLOG RW27: deletes and renumbers leave no row of their own, so they must
// still move UpdatedStamp strictly forward — otherwise a stale mesh push
// carrying the old stamp passes the sync check and restores the old list.
func TestDeleteAndReorderAdvanceStamp(t *testing.T) {
	d := openTestDB(t)
	sh := mustCreateShow(t, d, "Stamp")
	for _, l := range []string{"A", "B", "C"} {
		if _, err := d.CreateCue(sh.ID, Cue{Label: l, DurationMS: 60000}); err != nil {
			t.Fatalf("create %s: %v", l, err)
		}
	}
	msg, err := d.CreateMessage(sh.ID, "hi", "")
	if err != nil {
		t.Fatalf("message: %v", err)
	}
	cues, _ := d.ListCues(sh.ID)

	steps := []struct {
		name string
		do   func() error
	}{
		{"move", func() error { return d.MoveCue(sh.ID, 1, 3) }},
		{"reorder", func() error { return d.ReorderCues(sh.ID, []int64{cues[2].ID, cues[0].ID, cues[1].ID}) }},
		{"delete cue", func() error { return d.DeleteCue(sh.ID, 2) }},
		{"delete message", func() error { return d.DeleteMessage(sh.ID, msg.ID) }},
		{"replace with nothing", func() error { return d.ReplaceCues(sh.ID, nil) }},
	}
	for _, s := range steps {
		before := d.UpdatedStamp(sh.ID)
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if after := d.UpdatedStamp(sh.ID); after <= before {
			t.Errorf("%s: stamp %d → %d, want strictly later", s.name, before, after)
		}
	}
}
