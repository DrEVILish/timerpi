// ws tests: KickSession — the operator "delete session" path. The kicked
// client must receive the "session deleted" error frame (mesh.js stops
// reconnecting on it), then the socket closes; a second kick finds nothing.
package ws

import (
	"strings"
	"testing"
	"time"
)

func TestKickSessionDropsAndExplains(t *testing.T) {
	ts := newTestServer(t, false)
	c := ts.joinClient(t, "screen", "peer-1")
	defer c.close()
	c.read(t) // joined frame

	if got := ts.hub.KickSession(ts.showID, "peer-1"); got != 1 {
		t.Fatalf("kick count: want 1, got %d", got)
	}
	// The why frame lands before the close.
	err := c.read(t)
	if err["t"] != "err" || !strings.Contains(err["message"].(string), "session deleted") {
		t.Fatalf("kick frame: %v", err)
	}
	clock := 150 * time.Millisecond // wait ≥ the drain sleep, then grief
	time.Sleep(clock)
	if _, _, err := c.conn.ReadMessage(); err == nil {
		t.Error("socket still open after kick")
	}
	// Nothing left to kick: peer gone, unknown peer, unknown show.
	if got := ts.hub.KickSession(ts.showID, "peer-1"); got != 0 {
		t.Errorf("re-kick: want 0, got %d", got)
	}
	if got := ts.hub.KickSession(ts.showID, "never-joined"); got != 0 {
		t.Errorf("unknown peer: want 0, got %d", got)
	}
	if got := ts.hub.KickSession(ts.showID+999, "peer-1"); got != 0 {
		t.Errorf("unknown show: want 0, got %d", got)
	}
}
