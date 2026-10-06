package ws

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// BUGLOG RW1: an oversized frame (here before any join) closes the
// connection instead of being buffered.
func TestOversizedFrameClosesConnection(t *testing.T) {
	ts := newTestServer(t, false)
	wsURL := "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	big := `{"v":1,"t":"join","role":"display","show":"` + strings.Repeat("A", maxFrameBytes+1) + `"}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(big)); err != nil {
		return // already refused
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("server answered an oversized frame instead of closing")
	} else if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
		t.Fatal("connection still open after an oversized frame")
	}
}
