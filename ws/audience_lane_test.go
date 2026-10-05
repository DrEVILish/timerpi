// Audience-lane unit tests (PLAN §11.5, phase 4): audience joins land in
// their own bucket (the boards' 512 cap untouched), the join reply carries
// the visible-interaction state only, the lane is read-only, and
// BroadcastPoll reaches both the lane and the boards without a snapshot.
package ws

import (
	"strings"
	"testing"

	"timerpi/timerpi"
)

func TestAudienceLaneJoinAndFeed(t *testing.T) {
	ts := newTestServer(t, false)
	ts.hub.SetPollsFunc(func(int64) (*timerpi.PollView, error) {
		return &timerpi.PollView{ID: 7, Kind: timerpi.KindPoll, Question: "Lunch?",
			Options: []string{"Pizza", "Skyr"}, State: timerpi.StateOpen,
			Counts: []int64{1, 0}, Total: 1}, nil
	})

	// An audience join answers with the poll frame — never a snapshot.
	c := ts.joinClient(t, "audience", "phone-1")
	defer c.close()
	f := c.read(t)
	if f["t"] != "poll" {
		t.Fatalf("audience join reply: %v", f)
	}
	poll, _ := f["poll"].(map[string]any)
	if poll == nil || poll["question"] != "Lunch?" {
		t.Fatalf("poll payload: %v", f)
	}
	if _, has := f["snapshot"]; has {
		t.Fatal("audience received cue state (must be poll-only)")
	}

	// BroadcastPoll reaches the lane AND board sessions as a delta.
	board := ts.joinClient(t, "screen", "board-1")
	defer board.close()
	f2 := board.read(t)
	t.Logf("DBG board frame: %v", f2)
	ts.hub.BroadcastPoll(ts.showID)
	df := c.read(t)
	if df["t"] != "poll" || df["poll"] == nil {
		t.Fatalf("lane delta missing: %v", df)
	}
	// The board gets the SAME small delta (no full snapshot behind it).
	bf := board.read(t)
	if bf["t"] != "poll" || bf["poll"] == nil {
		t.Fatalf("board delta missing: %v", bf)
	}

	// The lane is read-only: any command is refused.
	c.send(t, map[string]any{"t": "cmd", "action": "go"})
	err := c.read(t)
	if err["t"] != "err" || !strings.Contains(err["message"].(string), "read-only") {
		t.Fatalf("audience command not refused: %v", err)
	}
}

func TestAudienceLaneCappedSeparately(t *testing.T) {
	ts := newTestServer(t, false)
	ts.hub.SetPollsFunc(func(int64) (*timerpi.PollView, error) { return nil, nil })
	// An audience-only show: the lane counts phones, the boards' 512
	// budget stays untouched (a 4000-socket flood is the load harness's
	// job — TestLoadSteadyAudience under TP_LOAD=1).
	c := ts.joinClient(t, "audience", "phone-cap")
	defer c.close()
	c.read(t)
	if got := ts.hub.AudSessions(); got != 1 {
		t.Fatalf("aud lane count: want 1, got %d", got)
	}
	if got := ts.hub.Sessions(); got != 0 {
		t.Fatalf("audience leaked into the board session count: %d", got)
	}
	// (Direct sh.aud access from the test races unregister's delete — the
	// AudSessions counter above is the synchronized view.)
}
