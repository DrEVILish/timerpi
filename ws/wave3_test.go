package ws

import (
	"strings"
	"testing"
	"time"
)

// Unknown command actions answer `unknown command %q`; the HTTP-route
// actions answer the redirect hint instead (only unknown FRAMES were
// tested before).
func TestUnknownCommandAndRouteHint(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "bogus"})
	if errFrame := a.readUntil(t, "err"); !strings.Contains(errFrame["message"].(string), `unknown command "bogus"`) {
		t.Fatalf("unknown action err = %v", errFrame)
	}

	for _, action := range []string{"sync", "import", "display_cfg"} {
		a.send(t, map[string]any{"t": "cmd", "action": action})
		if errFrame := a.readUntil(t, "err"); !strings.Contains(errFrame["message"].(string), "not a WS command") {
			t.Fatalf("%s hint err = %v", action, errFrame)
		}
	}
}

// showMsg/hideMsg/clearMsgs had zero coverage (only addMsg was sent):
// hide is not delete (re-show works), clearMsgs deletes (re-show errs),
// and missing ids are refused.
func TestMessageShowHideClearViaWS(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "addMsg", "args": map[string]any{"text": "WRAP UP", "show": true}})
	snap := a.readUntil(t, "state")["snapshot"].(map[string]any)
	msgs := snap["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("shown addMsg: %v", msgs)
	}
	id := int64(msgs[0].(map[string]any)["id"].(float64))

	// Missing ids refused before touching the store.
	a.send(t, map[string]any{"t": "cmd", "action": "showMsg", "args": map[string]any{}})
	if errFrame := a.readUntil(t, "err"); !strings.Contains(errFrame["message"].(string), "showMsg needs id") {
		t.Fatalf("showMsg no id err = %v", errFrame)
	}
	a.send(t, map[string]any{"t": "cmd", "action": "hideMsg", "args": map[string]any{}})
	if errFrame := a.readUntil(t, "err"); !strings.Contains(errFrame["message"].(string), "hideMsg needs id") {
		t.Fatalf("hideMsg no id err = %v", errFrame)
	}

	// Hide removes it from the wire but keeps the row: re-show works.
	a.send(t, map[string]any{"t": "cmd", "action": "hideMsg", "args": map[string]any{"id": id}})
	if msgs := a.readUntil(t, "state")["snapshot"].(map[string]any)["messages"].([]any); len(msgs) != 0 {
		t.Fatalf("hideMsg left messages on the wire: %v", msgs)
	}
	a.send(t, map[string]any{"t": "cmd", "action": "showMsg", "args": map[string]any{"id": id}})
	if msgs := a.readUntil(t, "state")["snapshot"].(map[string]any)["messages"].([]any); len(msgs) != 1 {
		t.Fatalf("re-show after hide failed: %v", msgs)
	}

	// Clear deletes everything: the wire is empty and re-show errs.
	a.send(t, map[string]any{"t": "cmd", "action": "clearMsgs"})
	if msgs := a.readUntil(t, "state")["snapshot"].(map[string]any)["messages"].([]any); len(msgs) != 0 {
		t.Fatalf("clearMsgs left messages: %v", msgs)
	}
	// Clear deletes everything: the wire is empty and re-show of the
	// cleared id revives nothing (ShowMessage on a missing row is a
	// silent no-op success — UPDATE matches zero rows, no error — so a
	// state, not an err, comes back with the wire still empty).
	a.send(t, map[string]any{"t": "cmd", "action": "showMsg", "args": map[string]any{"id": id}})
	if msgs := a.readUntil(t, "state")["snapshot"].(map[string]any)["messages"].([]any); len(msgs) != 0 {
		t.Fatalf("showMsg on cleared id revived a row: %v", msgs)
	}
}

// The dashboard form submits FormData (all strings): show:"true" must
// show immediately, show:"false"/absent must queue. (The custom form's
// checkbox rides value="true"; booleans were already covered.)
func TestAddMsgFormDataShapes(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "addMsg", "args": map[string]any{"text": "A", "color": "#7C3AED", "show": "true"}})
	if msgs := a.readUntil(t, "state")["snapshot"].(map[string]any)["messages"].([]any); len(msgs) != 1 {
		t.Fatalf(`show:"true" did not show: %v`, msgs)
	}
	a.send(t, map[string]any{"t": "cmd", "action": "addMsg", "args": map[string]any{"text": "B", "show": "false"}})
	if msgs := a.readUntil(t, "state")["snapshot"].(map[string]any)["messages"].([]any); len(msgs) != 1 {
		t.Fatalf(`show:"false" leaked onto the wire: %v`, msgs)
	}
}

// Signal error branches (the relay test only covers the happy path):
// a missing target is refused; a gone peer is dropped quietly.
func TestSignalErrorBranches(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")
	b := ts.joinClient(t, "display", "peer-b")
	defer b.close()
	b.readUntil(t, "joined")
	a.readUntil(t, "peers")

	a.send(t, map[string]any{"t": "signal", "data": map[string]any{"candidate": "x"}})
	if errFrame := a.readUntil(t, "err"); !strings.Contains(errFrame["message"].(string), "signal needs to + data") {
		t.Fatalf("signal no-to err = %v", errFrame)
	}

	// A signal to a peer that already left is normal churn (late ICE
	// candidates): dropped without an err frame (E2E #9).
	a.send(t, map[string]any{"t": "signal", "to": "ghost", "data": map[string]any{"candidate": "x"}})
	a.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, raw, err := a.conn.ReadMessage(); err == nil {
		t.Fatalf("signal to a gone peer answered: %s", raw)
	}
}
