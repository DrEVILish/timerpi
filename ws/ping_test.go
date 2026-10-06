package ws

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// BUGLOG RS29: every session pings on its own ticker; one client that
// never reads (a stalled phone) can't hold back another's pings.
func TestEverySessionPingsOnItsOwn(t *testing.T) {
	saved := pingInterval
	pingInterval = 40 * time.Millisecond
	defer func() { pingInterval = saved }()
	ts := newTestServer(t, false)
	_ = ts.joinClient(t, "display", "stalled") // never reads
	live := ts.joinClient(t, "display", "live")
	pings := make(chan struct{}, 8)
	live.conn.SetPingHandler(func(string) error {
		select {
		case pings <- struct{}{}:
		default:
		}
		return live.conn.WriteControl(websocket.PongMessage, nil, time.Now().Add(time.Second))
	})
	go func() {
		for {
			if _, _, err := live.conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	got := 0
	deadline := time.After(time.Second)
	for got < 3 {
		select {
		case <-pings:
			got++
		case <-deadline:
			t.Fatalf("live client got %d pings in 1 s", got)
		}
	}
}
