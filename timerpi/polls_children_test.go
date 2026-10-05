package timerpi

import (
	"testing"
)

// Word-cloud children: only OPEN (operator-approved) words surface, with
// upvote counts, loudest first — this is what the board's wordcloud tile
// renders (PLAN §11.2).
func TestActivePollChildren(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Cloud")
	cloud, err := d.CreatePoll(Poll{ShowID: show.ID, Kind: KindWordCloud, Question: "One word for today"})
	if err != nil {
		t.Fatalf("create cloud: %v", err)
	}
	// Audience submits words (hidden children); one typo'd duplicate peer.
	w1, err := d.Submit(show.ID, KindWordCloud, "electric", "ph1", cloud.ID)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := d.Submit(show.ID, KindWordCloud, "cozy", "ph2", cloud.ID); err != nil {
		t.Fatalf("submit2: %v", err)
	}
	if w1.Parent != cloud.ID {
		t.Fatalf("submit did not parent to the cloud: %+v", w1)
	}
	// Nothing approved yet → no children on air.
	if err := d.SetPollState(show.ID, cloud.ID, StateOpen); err != nil {
		t.Fatalf("open cloud: %v", err)
	}
	pv, err := d.ActivePoll(show.ID)
	if err != nil || pv == nil {
		t.Fatalf("active cloud: %+v err %v", pv, err)
	}
	if len(pv.Children) != 0 {
		t.Fatalf("unapproved words leaked on air: %+v", pv.Children)
	}
	// Approve both; upvote the second one higher (an upvote needs its row
	// open — approval is exactly that).
	cozy, _ := d.ListPolls(show.ID)
	var cozyID int64
	for _, p := range cozy {
		if p.Question == "cozy" {
			cozyID = p.ID
		}
	}
	if err := d.SetPollState(show.ID, w1.ID, StateOpen); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := d.SetPollState(show.ID, cozyID, StateOpen); err != nil {
		t.Fatalf("approve cozy: %v", err)
	}
	if err := d.Vote(show.ID, cozyID, "ph3", "1"); err != nil {
		t.Fatalf("upvote: %v", err)
	}
	pv, _ = d.ActivePoll(show.ID)
	if len(pv.Children) != 2 {
		t.Fatalf("want 2 approved words, got %+v", pv.Children)
	}
	if pv.Children[0].Question != "cozy" || pv.Children[0].Upvotes != 1 {
		t.Fatalf("loudest-first ordering broken: %+v", pv.Children)
	}
	// Poll-kind items never carry children.
	poll, _ := d.CreatePoll(Poll{ShowID: show.ID, Kind: KindPoll, Question: "Q", Options: `["a"]`})
	_ = poll
	if err := d.SetPollState(show.ID, poll.ID, StateOpen); err != nil {
		t.Fatalf("open poll: %v", err)
	}
	pv, _ = d.ActivePoll(show.ID)
	if pv.Children != nil {
		t.Fatalf("poll item grew children: %+v", pv.Children)
	}
}
