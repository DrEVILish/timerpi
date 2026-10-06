package ws

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// BUGLOG RW11: a display can't take an operator's peer id, and screens
// can't claim an early joinedAt (it orders the browser-mesh election).
func TestJoinPeerIdentityRules(t *testing.T) {
	ts := newTestServer(t, false)
	op := ts.joinClient(t, "controls", "op-1")
	defer op.conn.Close()
	if m := op.read(t); m["t"] != "joined" {
		t.Fatalf("operator join: %v", m)
	}

	dial := func(role, peer string, joinedAt int64) map[string]any {
		t.Helper()
		wsURL := "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		t.Cleanup(func() { conn.Close() })
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(
			`{"v":1,"t":"join","role":%q,"show":%q,"peerId":%q,"joinedAt":%d}`, role, ts.showCode, peer, joinedAt)))
		c := &wsClient{conn: conn}
		return c.read(t)
	}
	if m := dial("display", "op-1", 1); m["t"] != "err" {
		t.Errorf("display joined with the operator's peer id: %v", m)
	}
	m := dial("display", "tv-1", 1)
	you, _ := m["you"].(map[string]any)
	if ja, _ := you["joinedAt"].(float64); ja < float64(time.Now().Add(-time.Minute).UnixMilli()) {
		t.Errorf("display kept a claimed joinedAt of %v", you["joinedAt"])
	}
	if m := dial("display", strings.Repeat("x", 500), 0); m["t"] == "joined" {
		if you, _ := m["you"].(map[string]any); len(fmt.Sprint(you["peerId"])) > 64 {
			t.Errorf("oversized peer id kept: %d chars", len(fmt.Sprint(you["peerId"])))
		}
	}
}

func TestClampJoinedAt(t *testing.T) {
	now := int64(10_000_000_000)
	day := int64(24 * 3600 * 1000)
	cases := []struct {
		role    string
		claimed int64
		want    int64
	}{
		{"display", 1, now},
		{"controls", now + 5, now},
		{"controls", now - 1000, now - 1000},
		{"controls", 1, now - day},
		{"controls", 0, now},
	}
	for _, c := range cases {
		if got := clampJoinedAt(c.role, c.claimed, now); got != c.want {
			t.Errorf("clampJoinedAt(%s, %d) = %d, want %d", c.role, c.claimed, got, c.want)
		}
	}
}
