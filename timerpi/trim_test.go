package timerpi

import "testing"

// BUGLOG RW53: public views carry at most MaxPublicEntries entries — the
// most upvoted, in their original order — plus the spotlight, and count
// the rest in More.
func TestPublicViewTrimmed(t *testing.T) {
	v := &PollView{ID: 1, Kind: KindQA}
	for i := int64(1); i <= 80; i++ {
		v.Children = append(v.Children, PollView{ID: i, Upvotes: i % 7})
	}
	v.Spotlight = &PollView{ID: 3}
	out := v.Trimmed(MaxPublicEntries)
	if n := len(out.Children); n < MaxPublicEntries || n > MaxPublicEntries+1 || out.More != 80-n {
		t.Fatalf("kept %d, more %d", len(out.Children), out.More)
	}
	spot := false
	for i, c := range out.Children {
		if c.ID == 3 {
			spot = true
		}
		if i > 0 && c.ID < out.Children[i-1].ID {
			t.Fatal("trimmed entries lost their order")
		}
	}
	if !spot {
		t.Error("the spotlight was trimmed away")
	}
	if len(v.Children) != 80 {
		t.Error("Trimmed changed the original view")
	}
	small := &PollView{Children: []PollView{{ID: 1}}}
	if small.Trimmed(MaxPublicEntries) != small {
		t.Error("a short view should pass through")
	}
	var none *PollView
	if none.Trimmed(5) != nil {
		t.Error("nil view")
	}
}
