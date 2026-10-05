package ws

import (
	"testing"
	"time"
)

// The broadcast-race regression (review): engine.notify runs subscribers
// inline on EVERY mutating goroutine — the 250 ms ticker and per-session
// commands — so broadcast used to race sh.sessions/sh.sigs against
// register/unregister. Under -race this test fails loudly on any unsynced
// re-introduction.
func TestConcurrentBroadcastNoRace(t *testing.T) {
	ts := newTestServer(t, true)
	const senders = 3
	clients := make([]*wsClient, senders)
	for i := range clients {
		clients[i] = ts.joinClient(t, "controls", string(rune('a'+i))+"-peer")
		defer clients[i].close()
		clients[i].readUntil(t, "joined")
	}

	done := make(chan struct{})
	for i, c := range clients {
		go func(i int, c *wsClient) {
			defer func() { _ = recover() }() // dial deadlines during teardown
			for n := 0; n < 30; n++ {
				c.send(t, map[string]any{"t": "cmd", "action": []string{"next", "prev", "pause", "pause"}[n%4]})
				c.send(t, map[string]any{"t": "cmd", "action": "cueAdd", "args": map[string]any{"label": "race", "durationMS": 60_000}})
				c.send(t, map[string]any{"t": "cmd", "action": "clearMsgs"})
			}
			done <- struct{}{}
		}(i, c)
	}
	// Let the ticker interleave while commands stream.
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < senders; i++ {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("senders wedged")
		}
	}

	// Server alive + engines consistent is the assertion (race detector
	// does the heavy lifting; a map-race fatal kills the test binary).
	for _, c := range clients {
		c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	}
}
