// Audience-layer frame tests (test-gap round): the hub merges the on-air
// poll (pollsFn) into every fanout frame, so boards and audience clients
// stay in lockstep without a second subscription.
package ws

import (
	"testing"

	"timerpi/timerpi"
)

// readState drains oob fragments until the full state frame arrives
// (broadcast ships fragments first, state LAST).
func readState(t *testing.T, c *wsClient) map[string]any {
	t.Helper()
	for i := 0; i < 10; i++ {
		f := c.read(t)
		if f["t"] == "state" {
			return f
		}
	}
	t.Fatal("no state frame in 10 reads")
	return nil
}

func TestBroadcastCarriesPoll(t *testing.T) {
	ts := newTestServer(t, false)
	ts.hub.SetPollsFunc(func(showID int64) (*timerpi.PollView, error) {
		return &timerpi.PollView{ID: 9, Kind: timerpi.KindPoll,
			Question: "Lunch?", State: timerpi.StateOpen,
			Options: []string{"Pizza", "Skyr"}, Counts: []int64{1, 2}, Total: 3}, nil
	})
	c := ts.joinClient(t, "screen", "peer-poll")
	defer c.close()
	c.read(t) // joined frame

	// One broadcast — every frame the client sees carries the poll.
	eng, err := ts.engines.Get(ts.showID)
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	snap, err := eng.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	ts.hub.broadcast(ts.showID, snap)
	frame := readState(t, c)
	snapFrame, _ := frame["snapshot"].(map[string]any)
	poll, ok := snapFrame["poll"].(map[string]any)
	if !ok {
		t.Fatalf("state frame carries no poll: %v", frame)
	}
	if poll["id"].(float64) != 9 || poll["question"] != "Lunch?" || poll["state"] != "open" {
		t.Fatalf("poll payload: %v", poll)
	}
	// nil pollsFn (or a read error) must leave the frame clean, not break
	// it — asserted on a FRESH client + fresh snapshot so no stale queue
	// frames from the first broadcast leak into the read.
	ts.hub.SetPollsFunc(func(int64) (*timerpi.PollView, error) { return nil, nil })
	c2 := ts.joinClient(t, "screen", "peer-poll-2")
	defer c2.close()
	c2.read(t) // joined frame
	snap2, err := eng.Snapshot()
	if err != nil {
		t.Fatalf("snapshot2: %v", err)
	}
	ts.hub.broadcast(ts.showID, snap2)
	if frame := readState(t, c2); frame["snapshot"].(map[string]any)["poll"] != nil {
		t.Fatalf("nil active poll still shipped: %v", frame["snapshot"])
	}
}
