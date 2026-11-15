package timerpi

import "testing"

func TestReorderCuesDuplicateIDsKeepDenseOrder(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Order")
	var ids []int64
	for _, label := range []string{"a", "b", "c"} {
		cue, err := d.CreateCue(show.ID, sampleCue(0, label))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, cue.ID)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := d.ReorderCues(show.ID, []int64{ids[2], ids[2], 99999, ids[0], ids[0]}); err != nil {
			t.Fatal(err)
		}
		cues, err := d.ListCues(show.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := []int64{ids[2], ids[0], ids[1]}
		if len(cues) != len(want) {
			t.Fatalf("cue count = %d", len(cues))
		}
		for i, cue := range cues {
			if cue.ID != want[i] || cue.Pos != int64(i+1) {
				t.Fatalf("reorder attempt %d: cue %d = %+v; want id %d at position %d", attempt, i, cue, want[i], i+1)
			}
		}
	}
}
