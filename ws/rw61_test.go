package ws

import "testing"

// BUGLOG RW61: a cell edit of Speaker, At zero, an alert or the notes
// saved but the running order kept the old text until a reload — the
// table's change check only compared title, duration, type and timer.
func TestEveryRowFieldRedrawsTheTable(t *testing.T) {
	ts := newTestServer(t, true)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	for _, edit := range []map[string]any{
		{"speaker": "Grace"},
		{"endAction": "OVERTIME"},
		{"alert1MS": 60_000},
		{"alert2MS": 30_000},
		{"alertColor1": "#16a34a"},
		{"alertColor2": "#a855f7"},
		{"notes": "Lights down"},
		{"tags": "VT"},
		{"color": "#ff4444"},
	} {
		before := ts.oobs.n("frag-cuelist")
		args := map[string]any{"pos": 2}
		for k, v := range edit {
			args[k] = v
		}
		a.send(t, map[string]any{"t": "cmd", "action": "cueEdit", "args": args})
		a.readUntil(t, "state")
		if ts.oobs.n("frag-cuelist") == before {
			t.Errorf("cueEdit %v did not redraw the running order", edit)
		}
	}
}

// BUGLOG RW61: the state frame only carries messages on stage. A queued
// message must still redraw the panel, and an unrelated update must not
// redraw it again (the check compared the on-stage list with the full
// one, so any queued message meant a redraw on every update).
func TestQueuedMessageRedrawsPanelOnce(t *testing.T) {
	ts := newTestServer(t, true)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	before := ts.oobs.n("frag-messages")
	a.send(t, map[string]any{"t": "cmd", "action": "addMsg", "args": map[string]any{"text": "Queued", "show": false}})
	a.readUntil(t, "state")
	after := ts.oobs.n("frag-messages")
	if after == before {
		t.Fatal("a queued message did not redraw the messages panel")
	}
	a.send(t, map[string]any{"t": "cmd", "action": "cueEdit", "args": map[string]any{"pos": 1, "label": "Hello"}})
	a.readUntil(t, "state")
	if n := ts.oobs.n("frag-messages"); n != after {
		t.Errorf("an unrelated edit redrew the messages panel (%d → %d)", after, n)
	}
}
