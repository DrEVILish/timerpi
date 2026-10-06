package timerpi

import "testing"

// BUGLOG RW31: a room is created already attached to its event; a clone
// that fails leaves no empty room; deleting an event removes its rooms and
// its uploaded assets in one go.
func TestRoomWritesAreAtomic(t *testing.T) {
	d := openTestDB(t)
	ev, _, err := d.CreateEvent("Conf", "secret-pw", nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.CreateRoom(ev.ID, "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := d.CreateRoom(ev.ID, "B")
	if err != nil {
		t.Fatal(err)
	}
	if a.EventID != ev.ID || b.EventID != ev.ID || b.RoomPos != a.RoomPos+1 {
		t.Fatalf("rooms not attached in order: %+v %+v", a, b)
	}

	// A clone whose cues fail validation leaves nothing behind.
	if _, err := d.Exec(`INSERT INTO cues (show_id, pos, label, duration_ms, kind, timer_kind, end_action) VALUES (?, 1, 'Bad', 1000, 'nonsense', 'COUNTDOWN', 'HOLD')`, a.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := d.ListRooms(ev.ID)
	if _, err := d.CloneShow(a.ID, "Copy"); err == nil {
		t.Fatal("clone of an invalid cue should fail")
	}
	if after, _ := d.ListRooms(ev.ID); len(after) != len(before) {
		t.Errorf("failed clone left a room: %d → %d rooms", len(before), len(after))
	}

	asset, err := d.CreateAsset(ev.ID, "map.png", "image/png", []byte("\x89PNG\r\n\x1a\n...."))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteEvent(ev.ID); err != nil {
		t.Fatal(err)
	}
	if rooms, _ := d.ListRooms(ev.ID); len(rooms) != 0 {
		t.Errorf("rooms survived the event: %d", len(rooms))
	}
	if _, err := d.GetAsset(asset.ID); err == nil {
		t.Error("the event's asset survived the event")
	}
}
