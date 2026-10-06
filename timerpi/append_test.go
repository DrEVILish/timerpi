package timerpi

import "testing"

// BUGLOG RS30: appending is one transaction — order kept, positions
// contiguous, and a bad row leaves nothing behind.
func TestAppendCuesAtomic(t *testing.T) {
	d := openTestDB(t)
	sh := mustCreateShow(t, d, "Append")
	if _, err := d.CreateCue(sh.ID, Cue{Label: "Existing", DurationMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendCues(sh.ID, []Cue{{Label: "A", DurationMS: 1000}, {Label: "B", DurationMS: 1000}}); err != nil {
		t.Fatal(err)
	}
	cues, _ := d.ListCues(sh.ID)
	if len(cues) != 3 || cues[1].Label != "A" || cues[2].Label != "B" || cues[2].Pos != 3 {
		t.Fatalf("after append: %+v", cues)
	}
	bad := []Cue{{Label: "C", DurationMS: 1000}, {Label: "D", DurationMS: MaxDurationMS + 1}}
	if err := d.AppendCues(sh.ID, bad); err == nil {
		t.Fatal("a bad row should refuse the whole append")
	}
	if cues, _ := d.ListCues(sh.ID); len(cues) != 3 {
		t.Errorf("a refused append left %d cues, want 3", len(cues))
	}
}
