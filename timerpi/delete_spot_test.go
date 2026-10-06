package timerpi

import "testing"

// BUGLOG RS13: deleting a spotlighted entry clears the spotlight.
func TestDeleteSpotlightedEntryClearsSpot(t *testing.T) {
	d := openTestDB(t)
	sh := mustCreateShow(t, d, "Q")
	item, err := d.CreatePoll(Poll{ShowID: sh.ID, Kind: KindQA, Question: "Ask"})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := d.CreatePollRaw(Poll{ShowID: sh.ID, Kind: "submission", Question: "Why?", Parent: item.ID, State: StateOpen, Author: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Spotlight(sh.ID, item.ID, entry.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.DeletePoll(sh.ID, entry.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := d.GetPoll(sh.ID, item.ID); p.Spot != 0 {
		t.Errorf("spot still points at deleted entry %d", p.Spot)
	}
}
