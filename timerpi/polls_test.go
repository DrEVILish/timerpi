package timerpi

import (
	"strings"
	"testing"
)

func mustItem(t *testing.T, d *DB, showID int64, kind, q, opts string, correct int64) Poll {
	t.Helper()
	p, err := d.CreatePoll(Poll{ShowID: showID, Kind: kind, Question: q, Options: opts, Correct: correct})
	if err != nil {
		t.Fatalf("create %s: %v", kind, err)
	}
	return p
}

// Items are created off air; Show to Audience / Show to Presenter are
// independent targets; at most one item per target per room.
func TestPushTargets(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Targets")
	a := mustItem(t, d, show.ID, KindPoll, "Lunch?", `["Pizza","Skyr"]`, -1)
	b := mustItem(t, d, show.ID, KindPoll, "Coffee?", `["Yes","No"]`, -1)

	if on, _ := d.OnAirNow(show.ID); on.Audience != nil || on.Presenter != nil {
		t.Fatalf("new items must be off air: %+v", on)
	}
	if err := d.ShowTo(show.ID, a.ID, TargetPresenter, true); err != nil {
		t.Fatal(err)
	}
	on, _ := d.OnAirNow(show.ID)
	if on.Presenter == nil || on.Presenter.ID != a.ID || on.Audience != nil {
		t.Fatalf("presenter-only push: %+v", on)
	}
	// Phones see nothing while the item is on the presenter only.
	if vis, _ := d.AudienceRead(show.ID); vis.Poll != nil {
		t.Fatalf("presenter-only item leaked to phones: %+v", vis.Poll)
	}
	if err := d.ShowTo(show.ID, a.ID, TargetAudience, true); err != nil {
		t.Fatal(err)
	}
	// Pushing B to the audience takes A off the audience, but A stays on
	// the presenter.
	if err := d.ShowTo(show.ID, b.ID, TargetAudience, true); err != nil {
		t.Fatal(err)
	}
	on, _ = d.OnAirNow(show.ID)
	if on.Audience == nil || on.Audience.ID != b.ID || on.Presenter == nil || on.Presenter.ID != a.ID {
		t.Fatalf("targets after B→audience: %+v", on)
	}
	// Taking A off its last target hides it.
	if err := d.ShowTo(show.ID, a.ID, TargetPresenter, false); err != nil {
		t.Fatal(err)
	}
	got, _ := d.GetPoll(show.ID, a.ID)
	if got.State != StateHidden || got.ToAudience || got.ToPresenter {
		t.Fatalf("A should be hidden: %+v", got)
	}
	if err := d.ShowTo(show.ID, a.ID, "projector", true); err == nil {
		t.Error("unknown target accepted")
	}
	if err := d.HidePoll(show.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if on, _ := d.OnAirNow(show.ID); on.Audience != nil || on.Presenter != nil {
		t.Fatalf("hide left something on air: %+v", on)
	}
}

// Votes count while open on the audience target; results close voting and
// carry counts and totals; one vote per device, changeable until results.
func TestVotingAndResults(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Votes")
	p := mustItem(t, d, show.ID, KindPoll, "Lunch?", `["Pizza","Skyr","Soup"]`, -1)

	if err := d.Vote(show.ID, p.ID, "ph1", "0"); err == nil {
		t.Fatal("vote on a hidden item accepted")
	}
	if err := d.SetResults(show.ID, p.ID, true); err == nil {
		t.Fatal("results before showing accepted")
	}
	_ = d.ShowTo(show.ID, p.ID, TargetPresenter, true)
	if err := d.Vote(show.ID, p.ID, "ph1", "0"); err == nil {
		t.Fatal("vote accepted while not on the audience target")
	}
	_ = d.ShowTo(show.ID, p.ID, TargetAudience, true)
	for peer, choice := range map[string]string{"ph1": "0", "ph2": "1", "ph3": "1"} {
		if err := d.Vote(show.ID, p.ID, peer, choice); err != nil {
			t.Fatalf("vote %s: %v", peer, err)
		}
	}
	if err := d.Vote(show.ID, p.ID, "ph1", "1"); err != nil { // change of mind
		t.Fatal(err)
	}
	if err := d.Vote(show.ID, p.ID, "ph4", "9"); err == nil {
		t.Error("out-of-range choice accepted")
	}
	if err := d.Vote(show.ID, p.ID, "", "0"); err == nil {
		t.Error("anonymous vote accepted")
	}
	// BUGLOG RW17: while voting runs, the public view has the total but
	// not the per-option tally.
	if on, _ := d.OnAirNow(show.ID); on.Audience == nil || on.Audience.Counts != nil || on.Audience.Total != 3 {
		t.Fatalf("open poll public view leaks the tally: %+v", on.Audience)
	}
	if items, _ := d.ModeratorItems(show.ID); len(items[0].Counts) != 3 || items[0].Counts[1] != 3 {
		t.Fatalf("moderator must see the live tally: %+v", items[0])
	}
	if err := d.SetResults(show.ID, p.ID, true); err != nil {
		t.Fatal(err)
	}
	on, _ := d.OnAirNow(show.ID)
	if on.Audience == nil || on.Audience.State != StateResults || on.Audience.Total != 3 ||
		on.Audience.Counts[0] != 0 || on.Audience.Counts[1] != 3 || on.Audience.Counts[2] != 0 {
		t.Fatalf("results view: %+v", on.Audience)
	}
	// Results show wherever the item is: presenter too.
	if on.Presenter == nil || on.Presenter.State != StateResults {
		t.Fatalf("results did not follow to the presenter: %+v", on.Presenter)
	}
	if err := d.Vote(show.ID, p.ID, "ph5", "0"); err == nil {
		t.Error("vote accepted after results")
	}
}

// Quiz: the correct answer is required, hidden from phones until results,
// and revealed — including index 0 — once results are shown.
func TestQuizRevealIncludingFirstOption(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Quiz")
	if _, err := d.CreatePoll(Poll{ShowID: show.ID, Kind: KindQuiz, Question: "2+2?", Options: `["4","5"]`, Correct: -1}); err == nil {
		t.Fatal("quiz without a correct answer accepted")
	}
	q := mustItem(t, d, show.ID, KindQuiz, "2+2?", `["4","5"]`, 0)
	_ = d.ShowTo(show.ID, q.ID, TargetAudience, true)
	on, _ := d.OnAirNow(show.ID)
	if on.Audience.Correct != -1 {
		t.Fatalf("answer leaked before results: %d", on.Audience.Correct)
	}
	_ = d.SetResults(show.ID, q.ID, true)
	on, _ = d.OnAirNow(show.ID)
	if on.Audience.Correct != 0 {
		t.Fatalf("answer 0 not revealed in results: %d", on.Audience.Correct)
	}
	items, _ := d.ModeratorItems(show.ID)
	if items[0].Correct != 0 {
		t.Fatalf("moderator view must always know the answer: %+v", items[0])
	}
}

// Q&A: submissions only while shown to the audience; pending until
// approved; the wall shows approved + answered (answered last), never
// pending or dismissed; one spotlight; upvotes on approved questions.
func TestQAWallSpotlightModeration(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "QA")
	qa := mustItem(t, d, show.ID, KindQA, "Ask the panel", "", -1)

	if _, err := d.Submit(show.ID, qa.ID, "Too early?", "ph1"); err == nil {
		t.Fatal("submission accepted while the Q&A is off air")
	}
	_ = d.ShowTo(show.ID, qa.ID, TargetAudience, true)
	q1, err := d.Submit(show.ID, qa.ID, "When is lunch?", "ph1")
	if err != nil {
		t.Fatal(err)
	}
	q2, _ := d.Submit(show.ID, qa.ID, "Slides online?", "ph2")
	q3, _ := d.Submit(show.ID, qa.ID, "Spam spam", "ph3")
	if _, err := d.Submit(show.ID, qa.ID, "", "ph4"); err == nil {
		t.Error("empty submission accepted")
	}
	on, _ := d.OnAirNow(show.ID)
	if len(on.Audience.Children) != 0 {
		t.Fatalf("pending questions leaked to the wall: %+v", on.Audience.Children)
	}
	items, _ := d.ModeratorItems(show.ID)
	if items[0].Pending != 3 {
		t.Fatalf("moderator pending count: %+v", items[0])
	}
	for _, id := range []int64{q1.ID, q2.ID} {
		if err := d.Moderate(show.ID, id, StateOpen); err != nil {
			t.Fatal(err)
		}
	}
	_ = d.Moderate(show.ID, q3.ID, StateDismissed)
	if err := d.Vote(show.ID, q3.ID, "ph9", "1"); err == nil {
		t.Error("upvote on a dismissed question accepted")
	}
	_ = d.Vote(show.ID, q2.ID, "ph5", "1")
	_ = d.Vote(show.ID, q2.ID, "ph6", "1")
	_ = d.Vote(show.ID, q2.ID, "ph6", "1") // same device twice = one upvote
	on, _ = d.OnAirNow(show.ID)
	wall := on.Audience.Children
	if len(wall) != 2 || wall[0].ID != q2.ID || wall[0].Upvotes != 2 {
		t.Fatalf("wall order/upvotes: %+v", wall)
	}
	// Spotlight one, then mark it answered: it leaves the spotlight and
	// sinks below open questions.
	if err := d.Spotlight(show.ID, qa.ID, q2.ID); err != nil {
		t.Fatal(err)
	}
	on, _ = d.OnAirNow(show.ID)
	if on.Audience.Spotlight == nil || on.Audience.Spotlight.ID != q2.ID {
		t.Fatalf("spotlight: %+v", on.Audience.Spotlight)
	}
	_ = d.Moderate(show.ID, q2.ID, StateAnswered)
	on, _ = d.OnAirNow(show.ID)
	if on.Audience.Spotlight != nil {
		t.Fatalf("answered question kept the spotlight")
	}
	if wall := on.Audience.Children; len(wall) != 2 || wall[1].ID != q2.ID || wall[1].State != StateAnswered {
		t.Fatalf("answered question should sink: %+v", wall)
	}
	// Spotlighting a pending question approves it.
	q4, _ := d.Submit(show.ID, qa.ID, "Is it recorded?", "ph7")
	if err := d.Spotlight(show.ID, qa.ID, q4.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := d.GetPoll(show.ID, q4.ID); got.State != StateOpen {
		t.Fatalf("spotlit question not approved: %s", got.State)
	}
}

// Word cloud: identical words aggregate (weight = senders), one approval
// covers every copy, auto-approve skips moderation, words are short.
func TestWordCloudAggregation(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Cloud")
	cloud := mustItem(t, d, show.ID, KindWordCloud, "One word for today", "", -1)
	_ = d.ShowTo(show.ID, cloud.ID, TargetAudience, true)
	w1, _ := d.Submit(show.ID, cloud.ID, "Electric", "ph1")
	_, _ = d.Submit(show.ID, cloud.ID, "electric", "ph2")
	_, _ = d.Submit(show.ID, cloud.ID, "cozy", "ph3")
	if err := d.Moderate(show.ID, w1.ID, StateOpen); err != nil {
		t.Fatal(err)
	}
	on, _ := d.OnAirNow(show.ID)
	if c := on.Audience.Children; len(c) != 1 || !strings.EqualFold(c[0].Question, "electric") || c[0].Upvotes != 2 {
		t.Fatalf("cloud aggregation: %+v", c)
	}
	if err := d.UpdatePoll(show.ID, cloud.ID, "One word for today", nil, -1, true); err != nil {
		t.Fatal(err)
	}
	w, _ := d.Submit(show.ID, cloud.ID, strings.Repeat("x", 100), "ph4")
	if w.State != StateOpen || len(w.Question) > 32 {
		t.Fatalf("auto-approve / word clip: %+v", w)
	}
}

// Deleting an item removes its entries; entries can be deleted alone.
func TestDeleteItemAndEntries(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Del")
	qa := mustItem(t, d, show.ID, KindQA, "Ask", "", -1)
	_ = d.ShowTo(show.ID, qa.ID, TargetAudience, true)
	e, _ := d.Submit(show.ID, qa.ID, "Q", "ph1")
	if err := d.DeletePoll(show.ID, e.ID); err != nil {
		t.Fatal(err)
	}
	_, _ = d.Submit(show.ID, qa.ID, "Q2", "ph2")
	if err := d.DeletePoll(show.ID, qa.ID); err != nil {
		t.Fatal(err)
	}
	all, _ := d.ListPolls(show.ID)
	if len(all) != 0 {
		t.Fatalf("leftover rows: %+v", all)
	}
	if err := d.DeletePoll(show.ID, 99999); err == nil {
		t.Error("deleting a missing item succeeded")
	}
}

// BUGLOG RW54/RW18: the on-air view is cached per room, rebuilt after
// every poll write, and a failed rebuild serves the last good value.
func TestOnAirCache(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Cache")
	p := mustItem(t, d, show.ID, KindPoll, "Q?", `["a","b"]`, -1)
	_ = d.ShowTo(show.ID, p.ID, TargetAudience, true)
	on, err := d.OnAirNow(show.ID)
	if err != nil || on.Audience == nil || on.Audience.Total != 0 {
		t.Fatalf("first read: %+v %v", on.Audience, err)
	}
	_ = d.Vote(show.ID, p.ID, "ph1", "0")
	if on, _ = d.OnAirNow(show.ID); on.Audience.Total != 1 {
		t.Fatalf("vote not reflected: total %d", on.Audience.Total)
	}
	// Break the database read: the last good view is served, not "none".
	_ = d.Vote(show.ID, p.ID, "ph2", "1") // marks the cache stale
	if _, err := d.Exec(`ALTER TABLE polls RENAME TO polls_gone`); err != nil {
		t.Fatal(err)
	}
	on, err = d.OnAirNow(show.ID)
	if err != nil || on.Audience == nil || on.Audience.ID != p.ID {
		t.Fatalf("failed rebuild dropped the item: %+v %v", on.Audience, err)
	}
}
