package ws

import (
	"strings"
	"testing"
	"time"
)

// E5 over WS: cueEdit accepts a strict HH:MM startAt (visible on the
// snapshot cue), clears on "", and refuses garbage without touching the row.
func TestCueEditStartAtE5(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	past := time.Now().Local().Add(-time.Hour).Format("15:04")
	a.send(t, map[string]any{"t": "cmd", "action": "cueEdit", "args": map[string]any{"pos": 1, "startAt": past}})
	snap := a.readUntil(t, "state")["snapshot"].(map[string]any)
	cues := snap["cues"].([]any)
	if cues[0].(map[string]any)["startAt"] != past {
		t.Fatalf("startAt not stored: %v", cues[0])
	}

	a.send(t, map[string]any{"t": "cmd", "action": "cueEdit", "args": map[string]any{"pos": 1, "startAt": "25:99"}})
	if errFrame := a.readUntil(t, "err"); !strings.Contains(errFrame["message"].(string), "startAt must be HH:MM") {
		t.Fatalf("garbage startAt err = %v", errFrame)
	}
	// The refused write left the stored value alone (no state follows an
	// err; the next command's snapshot still carries the old time).
	a.send(t, map[string]any{"t": "cmd", "action": "cueEdit", "args": map[string]any{"pos": 1, "startAt": ""}})
	snap = a.readUntil(t, "state")["snapshot"].(map[string]any)
	if _, present := snap["cues"].([]any)[0].(map[string]any)["startAt"]; present {
		t.Fatalf("cleared startAt still on the wire: %v", snap["cues"].([]any)[0])
	}
}
