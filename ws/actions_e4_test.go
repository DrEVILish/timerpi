package ws

import (
	"strings"
	"testing"
)

// E4: mutating WS verbs land in the per-show action log with the
// role:peer actor; failures are NOT logged.
func TestActionLogE4(t *testing.T) {
	ts := newTestServer(t, true)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "go"})
	a.readUntil(t, "state")
	a.send(t, map[string]any{"t": "cmd", "action": "cueAdd", "args": map[string]any{"label": "Sponsor", "durationMS": 60_000}})
	a.readUntil(t, "state")
	// A refused op (missing pos) logs nothing — note: cueDel on an
	// unknown-but-wellformed pos is a silent no-op success at the DB
	// layer (zero-row DELETE, no error), same family as re-showing a
	// cleared message id; only the guard refusal errs here.
	a.send(t, map[string]any{"t": "cmd", "action": "cueDel"})
	a.readUntil(t, "err")

	acts, err := ts.db.ListActions(ts.showID, 10)
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(acts) != 2 {
		t.Fatalf("logged actions = %+v, want exactly [cueAdd go] (failure excluded)", acts)
	}
	if acts[0].Action != "cueAdd" || acts[0].Detail != "Sponsor" {
		t.Errorf("newest = %+v, want cueAdd/Sponsor", acts[0])
	}
	if !strings.HasPrefix(acts[1].Actor, "controls:") || acts[1].Action != "go" {
		t.Errorf("older = %+v, want controls:* go", acts[1])
	}
}
