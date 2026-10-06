package timerpi

import "testing"

// BUGLOG RS12: the public submission total counts visible entries only.
func TestPublicTotalHidesHeldBackEntries(t *testing.T) {
	d := openTestDB(t)
	sh := mustCreateShow(t, d, "Q")
	item, err := d.CreatePoll(Poll{ShowID: sh.ID, Kind: KindQA, Question: "Ask"})
	if err != nil {
		t.Fatal(err)
	}
	for i, st := range []string{StateOpen, StateHidden, StateHidden, StateDismissed, StateAnswered} {
		if _, err := d.CreatePollRaw(Poll{ShowID: sh.ID, Kind: "submission", Question: "q", Parent: item.ID, State: st, Author: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := d.GetPoll(sh.ID, item.ID)
	if v := d.itemView(p, false); v.Total != 2 {
		t.Errorf("public total = %d, want 2 (open + answered)", v.Total)
	}
	if v := d.itemView(p, true); v.Total != 5 {
		t.Errorf("moderator total = %d, want 5", v.Total)
	}
}
