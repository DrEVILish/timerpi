package timerpi

import (
	"strings"
	"testing"
)

// One interaction item lifecycle: create (normalize + hidden default),
// vote (open only, dedupe by peer, range-checked), counts math, state
// machine (single-focus), and the audience visibility contract.
func TestPollsLifecycleWIP(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Polls")

	// Create: normalization + default state.
	p, err := d.CreatePoll(Poll{ShowID: show.ID, Kind: KindPoll, Question: "Lunch?",
		Options: `["Pizza","Skyr"]`})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.State != StateHidden {
		t.Fatalf("new poll must be hidden for moderation-by-silence, got %q", p.State)
	}
	if act, err := d.ActivePoll(show.ID); err != nil || act != nil {
		t.Fatalf("hidden poll surfaced as active: %+v err %v", act, err)
	}
	if _, err := d.CreatePoll(Poll{ShowID: show.ID, Kind: "bogus", Question: "x",
		Options: `[a,b]`}); err == nil {
		t.Error("unknown kind accepted")
	}
	if _, err := d.CreatePoll(Poll{ShowID: show.ID, Kind: KindPoll, Question: "",
		Options: `["a"]`}); err == nil {
		t.Error("poll with no question accepted")
	}
	if _, err := d.CreatePoll(Poll{ShowID: show.ID, Kind: KindQA, Question: "  ",
		Options: `[]`}); err == nil {
		t.Error("qa with no text accepted")
	}

	// Vote: closed items refuse; open items dedupe per peer + range-check.
	if err := d.Vote(show.ID, p.ID, "tv1", "0"); err == nil {
		t.Error("voted on a hidden poll")
	}
	if err := d.SetPollState(show.ID, p.ID, StateOpen); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := d.Vote(show.ID, p.ID, "tv1", "5"); err == nil {
		t.Error("out-of-range choice accepted")
	}
	if err := d.Vote(show.ID, p.ID, "", "0"); err == nil {
		t.Error("empty peer accepted")
	}
	if err := d.Vote(show.ID, p.ID, "tv1", "0"); err != nil {
		t.Fatalf("vote: %v", err)
	}
	// Change of mind replaces, never adds: still 1 distinct voter.
	if err := d.Vote(show.ID, p.ID, "tv1", "1"); err != nil {
		t.Fatalf("revote: %v", err)
	}
	if err := d.Vote(show.ID, p.ID, "tv2", "1"); err != nil {
		t.Fatalf("vote2: %v", err)
	}
	got, err := d.GetPoll(show.ID, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	v := d.PollCounts(got)
	if v.Total != 2 || len(v.Counts) != 2 || v.Counts[0] != 0 || v.Counts[1] != 2 {
		t.Fatalf("counts math: %+v", v)
	}

	// Single-focus state machine: opening B closes A.
	q, err := d.CreatePoll(Poll{ShowID: show.ID, Kind: KindPoll, Question: "Break?",
		Options: `["Yes","No"]`})
	if err != nil {
		t.Fatalf("create2: %v", err)
	}
	if err := d.SetPollState(show.ID, q.ID, StateOpen); err != nil {
		t.Fatalf("open2: %v", err)
	}
	a1, _ := d.GetPoll(show.ID, p.ID)
	if a1.State != StateHidden {
		t.Errorf("old poll stayed open after B was opened: %q", a1.State)
	}
	if err := d.SetPollState(show.ID, q.ID, "gone"); err == nil {
		t.Error("invalid state accepted")
	}

	// Results are on-air too.
	if err := d.SetPollState(show.ID, q.ID, StateResults); err != nil {
		t.Fatalf("results: %v", err)
	}
	act, err := d.ActivePoll(show.ID)
	if err != nil || act == nil {
		t.Fatalf("active after results: %+v err %v", act, err)
	}

	// Audience visibility: open qa shows with upvotes; submissions stay
	// hidden until carried into open state (moderation by silence).
	qr, err := d.CreatePoll(Poll{ShowID: show.ID, Kind: KindQA, Question: "Louder please",
		Author: "bh1"})
	if err != nil {
		t.Fatalf("submit qa: %v", err)
	}
	if vis, err := d.AudienceRead(show.ID); err != nil {
		t.Fatalf("read: %v", err)
	} else if vis.Poll != nil && vis.Poll.ID == qr.ID {
		t.Fatalf("concealed question is on air: %+v", vis.Poll)
	}
	// The submitted row is upvotable only when open; count is upvotes.
	if err := d.Vote(show.ID, qr.ID, "bh2", "1"); err == nil {
		t.Error("upvoted a concealed question")
	}
	sur, err := d.CreatePoll(Poll{ShowID: show.ID, Kind: KindIdeas, Question: "Roses are red"})
	if err != nil {
		t.Fatalf("create idea: %v", err)
	}
	if err := d.SetPollState(show.ID, sur.ID, StateOpen); err != nil {
		t.Fatalf("open idea: %v", err)
	}
	if err := d.Vote(show.ID, sur.ID, "bh2", "1"); err != nil {
		t.Fatalf("upvote open idea: %v", err)
	}
	vis, err := d.AudienceRead(show.ID)
	if err != nil || vis.Poll == nil || vis.Poll.Kind != KindIdeas {
		t.Fatalf("open idea not visible: %+v err %v", vis, err)
	}
	if vis.Poll.Upvotes != 1 {
		t.Errorf("idea upvote counts as options, want Upvotes=1: %+v", vis.Poll)
	}
	_ = qr

	// Submit: telemetry guard rails only — vertex trimming + peer required.
	if _, err := d.Submit(show.ID, KindQA, "Need agent", "", 0); err == nil {
		t.Error("submit without peer accepted")
	}
	essay := strings.Repeat("x", 500)
	sub, err := d.Submit(show.ID, KindQA, essay, "bh9", 0)
	if err != nil {
		t.Fatalf("submit essay: %v", err)
	}
	if len(sub.Question) > 280 {
		t.Errorf("essay not clipped: %d bytes", len(sub.Question))
	}
}
