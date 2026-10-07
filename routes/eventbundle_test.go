// The event copy (VENUE-CLOUD §4, §6): another box or the cloud imports it
// with the same codes, password hashes and screen keys; importing again
// keeps room ids.
package routes_test

import (
	"strings"
	"testing"

	"timerpi/timerpi"
)

func TestEventCopyRoundTrip(t *testing.T) {
	src := newAPITest(t)
	room2 := src.newRoom("Room B")
	if _, err := src.db.CreateCue(src.showID, timerpi.Cue{Label: "Opening", DurationMS: 600_000, AlertFlash1: true}); err != nil {
		t.Fatal(err)
	}
	if err := src.db.SetRoomPassword(room2.ID, "roompw"); err != nil {
		t.Fatal(err)
	}
	key, _ := src.db.ScreenKey(src.showID, "Stage")
	_ = src.db.SetScreenLook(src.showID, "Stage", "presenter", 90)
	_ = src.db.SetScreenTemplate(src.showID, "Stage", "dsm")
	mustPollCreate(t, src, `{"kind":"poll","question":"Lunch?","options":["Pizza","Soup"]}`)
	ev, _ := src.db.ResolveEvent(src.eventCode)
	raw, err := src.deps.ExportEvent(ev.ID)
	if err != nil {
		t.Fatal(err)
	}

	dst := newAPITest(t)
	got, err := dst.deps.ImportEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != ev.Code || got.SuperHash != ev.SuperHash {
		t.Fatalf("event copy = %+v", got)
	}
	rooms, _ := dst.db.ListRooms(got.ID)
	if len(rooms) != 2 || rooms[0].Code != src.showCode || rooms[1].Code != room2.Code {
		t.Fatalf("rooms = %+v", rooms)
	}
	cues, _ := dst.db.ListCues(rooms[0].ID)
	if len(cues) != 1 || cues[0].Label != "Opening" || !cues[0].AlertFlash1 {
		t.Fatalf("cues = %+v", cues)
	}
	if !dst.db.ScreenKeyValid(rooms[0].ID, "Stage", key) {
		t.Fatal("the screen key didn't travel: the box would lose its screen")
	}
	screens, _ := dst.db.ListScreens(rooms[0].ID)
	if len(screens) != 1 || screens[0].Kind != "presenter" || screens[0].Rotation != 90 || screens[0].Template != "dsm" {
		t.Fatalf("screens = %+v", screens)
	}
	if !timerpi.CheckPassword(rooms[1].RoomPW, "roompw") {
		t.Fatal("the room password didn't travel")
	}
	// The Event Technician signs in on the copy with the same password.
	if code, b := dst.call("POST", "/api/events/"+ev.Code+"/login", []byte(`{"pw":"testpw"}`), "application/json"); code != 200 {
		t.Fatalf("login on the copy: %d %s", code, b)
	}
	if _, b := dst.call("GET", "/api/shows/"+src.showCode+"/polls", nil, ""); !strings.Contains(string(b), "Lunch?") {
		t.Fatalf("polls didn't travel: %s", b)
	}

	// Importing again (the next copy) keeps the room ids; a room removed
	// at the source goes.
	_ = src.db.DeleteShow(room2.ID)
	raw2, _ := src.deps.ExportEvent(ev.ID)
	if _, err := dst.deps.ImportEvent(raw2); err != nil {
		t.Fatal(err)
	}
	again, _ := dst.db.ListRooms(got.ID)
	if len(again) != 1 || again[0].ID != rooms[0].ID {
		t.Fatalf("after re-import rooms = %+v (want id %d kept)", again, rooms[0].ID)
	}
}
