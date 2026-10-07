package ws

import (
	"testing"
	"time"
)

// Flash (2026-10-07): an operator's flash reaches every screen of the room
// as a transient frame; screens can't send it.
func TestFlashFansOut(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")
	d := ts.joinClient(t, "display", "peer-d")
	defer d.close()
	d.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "flash"})
	if f := d.readUntil(t, "flash"); f["ms"] != float64(FlashMS) {
		t.Fatalf("flash frame = %v", f)
	}
	d.send(t, map[string]any{"t": "cmd", "action": "flash"})
	if e := d.readUntil(t, "err"); e["message"] == nil {
		t.Fatal("a screen was allowed to flash")
	}
}

// An alert's Flash setting is saved with the cue.
func TestAlertFlashSaved(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")
	a.send(t, map[string]any{"t": "cmd", "action": "cueEdit", "args": map[string]any{"pos": 2, "alertFlash2": true}})
	for i := 0; i < 50; i++ {
		if c, err := ts.db.GetCue(ts.showID, 2); err == nil && c.AlertFlash2 {
			if c.AlertFlash1 {
				t.Fatal("alertFlash1 turned on too")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("alertFlash2 was not saved")
}
