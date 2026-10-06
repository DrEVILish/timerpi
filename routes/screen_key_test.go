package routes_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// BUGLOG RW9: only a screen holding its key (or a browser a moderator
// opened) receives operator content. A stranger with the room code joins
// as a display and gets the public snapshot: no stage messages, no notes,
// no Presenter item.
func TestScreenKeyGatesOperatorContent(t *testing.T) {
	ts := newAPITest(t)
	// Operator content: a cue note and a shown stage message.
	if _, err := ts.db.Exec(`INSERT INTO cues (show_id, pos, label, duration_ms, notes, tags, updated_at) VALUES (?, 1, 'Talk', 60000, 'SECRET-NOTE', 'SECRET-TAG', 1)`, ts.showID); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.db.Exec(`INSERT INTO messages (show_id, text, shown_at, updated_at) VALUES (?, 'SECRET-MSG', 1, 1)`, ts.showID); err != nil {
		t.Fatal(err)
	}
	join := func(p *persona, q url.Values) string {
		t.Helper()
		var dialer websocket.Dialer
		if p != nil {
			dialer.Jar = p.client.Jar
		}
		conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(ts.srv.URL, "http")+"/ws", http.Header{"Origin": []string{ts.srv.URL}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		frame := map[string]any{"v": 1, "t": "join", "role": "display", "show": ts.showCode, "joinedAt": time.Now().UnixMilli()}
		if q.Get("screen") != "" {
			frame["screen"], frame["key"] = q.Get("screen"), q.Get("key")
		}
		_ = conn.WriteJSON(frame)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	stranger := newPersona(ts)
	if got := join(stranger, url.Values{}); strings.Contains(got, "SECRET") {
		t.Errorf("unkeyed display received operator content: %.300s", got)
	}
	if code, page := stranger.do("GET", "/d/"+ts.showCode+"?view=board", ""); code != 200 || strings.Contains(page, "SECRET") {
		t.Errorf("unkeyed display page shows operator content (%d)", code)
	}
	// A wrong key for a real screen name is still public.
	if got := join(stranger, url.Values{"screen": {"Stage"}, "key": {"nope"}}); strings.Contains(got, "SECRET") {
		t.Error("wrong key received operator content")
	}

	// (The board view renders stage messages server-side.)
	// The moderator fetches the screen link; the key unlocks it.
	_, body := ts.call("POST", "/api/shows/"+ts.showCode+"/screens/link", []byte(`{"name":"Stage"}`), "application/json")
	var out struct{ Link string }
	_ = json.Unmarshal(body, &out)
	u, err := url.Parse(out.Link)
	if err != nil || u.Query().Get("key") == "" {
		t.Fatalf("screen link: %s", body)
	}
	if got := join(stranger, u.Query()); !strings.Contains(got, "SECRET-MSG") {
		t.Errorf("keyed screen missing stage messages: %.300s", got)
	}
	if code, page := stranger.do("GET", u.RequestURI()+"&view=board", ""); code != 200 || !strings.Contains(page, "SECRET-MSG") {
		t.Errorf("keyed display page lacks stage messages (%d)", code)
	}
	// Forgetting the screen releases it: the old key stops working.
	ts.call("POST", "/api/shows/"+ts.showCode+"/screens/forget", []byte(`{"name":"Stage"}`), "application/json")
	if got := join(stranger, u.Query()); strings.Contains(got, "SECRET") {
		t.Error("forgotten screen's key still unlocks operator content")
	}
}
