package routes_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// BUGLOG RW10: setting a room password drops an operator socket that was
// opened under the old (password-less) moderator cookie.
func TestPasswordChangeDropsOpenControls(t *testing.T) {
	ts := newAPITest(t)
	mod := newPersona(ts)
	if code, _ := mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode+"/login", `{"pw":""}`); code != 200 {
		t.Fatal("moderator login")
	}
	d := websocket.Dialer{Jar: mod.client.Jar, HandshakeTimeout: 3 * time.Second}
	conn, _, err := d.Dial("ws"+strings.TrimPrefix(ts.srv.URL, "http")+"/ws", http.Header{"Origin": []string{ts.srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]any{"v": 1, "t": "join", "role": "controls", "show": ts.showCode, "peerId": "mod-1", "joinedAt": time.Now().UnixMilli()})
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var first map[string]any
	if err := conn.ReadJSON(&first); err != nil || first["t"] == "err" {
		t.Fatalf("controls join: %v %v", err, first)
	}

	// The SuperOperator sets a room password.
	if code, b := ts.call("PATCH", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode, []byte(`{"password":"newroompw"}`), "application/json"); code != 200 {
		t.Fatalf("set room password: %d %s", code, b)
	}
	// The old socket is told and closed.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	sawErr := false
	for {
		var m map[string]any
		if err := conn.ReadJSON(&m); err != nil {
			break // closed
		}
		if m["t"] == "err" && strings.Contains(fmtAny(m["message"]), "password changed") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("socket closed without the password-changed notice (or stayed open)")
	}
}

func fmtAny(v any) string { s, _ := v.(string); return s }
