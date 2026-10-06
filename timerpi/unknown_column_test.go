package timerpi

import "testing"

// BUGLOG RS36: a column the structs don't know doesn't break reads.
func TestReadsSurviveAnUnknownColumn(t *testing.T) {
	d := openTestDB(t)
	sh := mustCreateShow(t, d, "Future")
	if _, err := d.CreateCue(sh.ID, Cue{Label: "A", DurationMS: 1000}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE cues ADD COLUMN from_the_future TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE shows ADD COLUMN from_the_future TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if cues, err := d.ListCues(sh.ID); err != nil || len(cues) != 1 {
		t.Errorf("ListCues with an unknown column: %v (%d)", err, len(cues))
	}
	if _, err := d.GetShow(sh.ID); err != nil {
		t.Errorf("GetShow with an unknown column: %v", err)
	}
}
