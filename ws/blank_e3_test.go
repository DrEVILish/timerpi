package ws

import (
	"testing"
)

// E3 over WS: blank raises show.blanked on the fanned snapshot, unblank
// clears it (role gating rides the central A1 check).
func TestBlankVerbsE3(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "blank"})
	snap := a.readUntil(t, "state")["snapshot"].(map[string]any)
	if show := snap["show"].(map[string]any); show["blanked"] != true {
		t.Fatalf("blank: show.blanked = %v", show["blanked"])
	}

	a.send(t, map[string]any{"t": "cmd", "action": "unblank"})
	snap = a.readUntil(t, "state")["snapshot"].(map[string]any)
	if show := snap["show"].(map[string]any); show["blanked"] != false {
		t.Fatalf("unblank: show.blanked = %v", show["blanked"])
	}
}
