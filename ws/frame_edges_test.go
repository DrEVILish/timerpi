package ws

import (
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Instrumented client for RAW frame sends (the shared helpers only send
// valid-shaped JSON).
func (c *wsClient) sendRaw(t *testing.T, frame string) {
	t.Helper()
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := c.conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		t.Fatalf("sendRaw: %v", err)
	}
}

// Malformed JSON in (post-join) answers a plain "unparseable frame" err to
// that client only — the session must survive (a later good frame works).
func TestUnparseableFrameChapter(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.sendRaw(t, `{"t":`) // broken JSON
	errFrame := a.readUntil(t, "err")
	if !strings.Contains(errFrame["message"].(string), "unparseable frame") {
		t.Fatalf("malformed json err = %v", errFrame)
	}

	// The same session keeps working after the err.
	a.send(t, map[string]any{"t": "cmd", "action": "pause"})
	if snap := a.readUntil(t, "state")["snapshot"]; snap == nil {
		t.Fatal("session broke after a malformed frame")
	}
}

// Unknown frame types answer `unknown frame type %q` (unknown FRAME, not
// unknown COMMAND — the command branch already has its own wording).
func TestUnknownFrameType(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "shout"})
	errFrame := a.readUntil(t, "err")
	if !strings.Contains(errFrame["message"].(string), `unknown frame type "shout"`) {
		t.Fatalf("unknown frame err = %v", errFrame)
	}
}

// settings branches outside {ts}: "title" renames (fanout carries it), a
// whitespace-only title errs, neither field errs.
func TestSettingsBranches(t *testing.T) {
	ts := newTestServer(t, true)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	// Rename via settings{title} → renamed in the fanned snapshot.
	a.send(t, map[string]any{"t": "cmd", "action": "settings", "args": map[string]any{"title": "New Name"}})
	snap := a.readUntil(t, "state")["snapshot"].(map[string]any)
	if show := snap["show"].(map[string]any); show["title"] != "New Name" {
		t.Fatalf("settings{title}: show title = %v", show["title"])
	}

	// Whitespace-only title → err, original title stands.
	a.send(t, map[string]any{"t": "cmd", "action": "settings", "args": map[string]any{"title": "   "}})
	errFrame := a.readUntil(t, "err")
	if !strings.Contains(errFrame["message"].(string), "settings needs ts or title") {
		t.Fatalf("settings{whitespace title} err = %v", errFrame)
	}

	// Neither field → err.
	a.send(t, map[string]any{"t": "cmd", "action": "settings", "args": map[string]any{"bogus": 1}})
	errFrame = a.readUntil(t, "err")
	if !strings.Contains(errFrame["message"].(string), "settings needs ts or title") {
		t.Fatalf("settings{bogus} err = %v", errFrame)
	}
}

// addMsg with a non-hex color errs BEFORE any DB write (no orphan rows).
func TestAddMsgInvalidColor(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "addMsg", "args": map[string]any{"text": "hi", "color": "red"}})
	errFrame := a.readUntil(t, "err")
	if !strings.Contains(errFrame["message"].(string), "invalid color") {
		t.Fatalf("addMsg bad color err = %v", errFrame)
	}
}
