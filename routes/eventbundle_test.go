// The event copy (VENUE-CLOUD §4, §6): another box or the cloud imports it
// with the same codes, password hashes and screen keys; importing again
// keeps room ids.
package routes_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"timerpi/boards"

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

func TestEventCopyPreservesBlackoutAndRemovesMap(t *testing.T) {
	src := newAPITest(t)
	ev, _ := src.db.ResolveEvent(src.eventCode)
	if err := src.db.SetShowBlanked(src.showID, true); err != nil {
		t.Fatal(err)
	}
	a, err := src.db.CreateAsset(ev.ID, "map.png", "image/png", []byte(pngHeader+"map"))
	if err != nil {
		t.Fatal(err)
	}
	if err := src.db.SetEventMap(ev.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	raw, err := src.deps.ExportEvent(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	dst := newAPITest(t)
	copied, err := dst.deps.ImportEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := dst.db.ListRooms(copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 1 || !rooms[0].Blanked {
		t.Fatalf("copied blackout lost: %+v", rooms)
	}
	if copied.MapAsset == 0 {
		t.Fatal("initial map missing")
	}
	if err := src.db.SetEventMap(ev.ID, 0); err != nil {
		t.Fatal(err)
	}
	raw, err = src.deps.ExportEvent(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	copied, err = dst.deps.ImportEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if copied.MapAsset != 0 {
		t.Fatalf("removed map retained: %d", copied.MapAsset)
	}
}

func TestEventCopyFailurePreservesExistingEvent(t *testing.T) {
	for _, table := range []string{"cues", "messages", "polls", "screens", "display_presets", "display_boards", "assets"} {
		t.Run(table, func(t *testing.T) {
			ts := newAPITest(t)
			ev, _ := ts.db.ResolveEvent(ts.eventCode)
			if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Original", DurationMS: 10000}); err != nil {
				t.Fatal(err)
			}
			if _, err := ts.db.CreateMessage(ts.showID, "Original message", ""); err != nil {
				t.Fatal(err)
			}
			if _, err := ts.db.CreatePoll(timerpi.Poll{ShowID: ts.showID, Kind: timerpi.KindQA, Question: "Original question"}); err != nil {
				t.Fatal(err)
			}
			if err := ts.db.SetScreenConfig(ts.showID, "Original screen", "", 0, ""); err != nil {
				t.Fatal(err)
			}
			if _, err := ts.db.SavePreset(ts.showID, "Original preset", `{"theme":"lcars"}`); err != nil {
				t.Fatal(err)
			}
			if err := boards.Migrate(ts.db.DB); err != nil {
				t.Fatal(err)
			}
			if _, err := boards.CreateBoard(ts.db.DB, ts.showID, "Original layout", ""); err != nil {
				t.Fatal(err)
			}
			original, err := ts.deps.ExportEvent(ev.ID)
			if err != nil {
				t.Fatal(err)
			}
			incoming := original
			if table == "assets" {
				var b map[string]any
				if err := json.Unmarshal(original, &b); err != nil {
					t.Fatal(err)
				}
				b["map"] = map[string]any{"name": "map.png", "data": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(pngHeader+"map"))}
				incoming, err = json.Marshal(b)
				if err != nil {
					t.Fatal(err)
				}
			}
			// Simulate a storage failure after destructive replacement has begun.
			if _, err := ts.db.Exec(`CREATE TRIGGER fail_copy BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'copy storage failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := ts.deps.ImportEvent(incoming); err == nil {
				t.Fatal("copy succeeded despite storage failure")
			}
			after, err := ts.deps.ExportEvent(ev.ID)
			if err != nil {
				t.Fatal(err)
			}
			var beforeDoc, afterDoc map[string]any
			if err := json.Unmarshal(original, &beforeDoc); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(after, &afterDoc); err != nil {
				t.Fatal(err)
			}
			delete(beforeDoc, "exportedAt")
			delete(afterDoc, "exportedAt")
			if !reflect.DeepEqual(beforeDoc, afterDoc) {
				t.Fatalf("failed copy changed existing event\nbefore: %s\nafter: %s", original, after)
			}
		})
	}
}

func TestEventCopyRetiresOldRuntimeOnlyAfterSuccess(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Cue", DurationMS: 10000}); err != nil {
		t.Fatal(err)
	}
	old, err := ts.engines.Get(ts.showID)
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := ts.db.ResolveEvent(ts.eventCode)
	raw, err := ts.deps.ExportEvent(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.db.Exec(`CREATE TRIGGER fail_copy BEFORE INSERT ON cues BEGIN SELECT RAISE(ABORT,'copy failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.deps.ImportEvent(raw); err == nil {
		t.Fatal("expected import failure")
	}
	current, err := ts.engines.Get(ts.showID)
	if err != nil {
		t.Fatal(err)
	}
	if current != old {
		t.Fatal("failed import replaced original engine")
	}
	if err := old.Start(1); err != nil {
		t.Fatalf("failed import disabled live engine: %v", err)
	}
	if _, err := ts.db.Exec(`DROP TRIGGER fail_copy`); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.deps.ImportEvent(raw); err != nil {
		t.Fatal(err)
	}
	if err := old.Start(1); !errors.Is(err, timerpi.ErrEngineRetired) {
		t.Fatalf("stale engine can still overwrite copied runtime: %v", err)
	}
	rt, _, err := ts.db.LoadRuntime(ts.showID)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Running {
		t.Fatalf("old engine overwrote imported stopped state: %+v", rt)
	}
}

func TestEventCopyRejectsBrokenReferences(t *testing.T) {
	for _, kind := range []string{"duplicate room", "layout owner", "screen layout", "interaction parent", "spotlight", "vote"} {
		t.Run(kind, func(t *testing.T) {
			ts := newAPITest(t)
			ev, _ := ts.db.ResolveEvent(ts.eventCode)
			original, err := ts.deps.ExportEvent(ev.ID)
			if err != nil {
				t.Fatal(err)
			}
			var b map[string]any
			if err := json.Unmarshal(original, &b); err != nil {
				t.Fatal(err)
			}
			room := b["rooms"].([]any)[0].(map[string]any)
			switch kind {
			case "duplicate room":
				b["rooms"] = []any{room, room}
			case "layout owner":
				b["boards"] = []any{map[string]any{"id": 1, "room": "QQQQQQQQ", "name": "Layout", "layout": json.RawMessage(boards.DefaultLayoutJSON())}}
			case "screen layout":
				room["screens"] = []any{map[string]any{"name": "Screen", "boardId": 999}}
			case "interaction parent":
				room["polls"] = []any{map[string]any{"id": 1, "kind": "qa", "question": "Question", "parent": 999}}
			case "spotlight":
				room["polls"] = []any{map[string]any{"id": 1, "kind": "qa", "question": "Question", "spot": 999}}
			case "vote":
				room["votes"] = []any{map[string]any{"pollId": 999, "peer": "phone", "choice": "1"}}
			}
			raw, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ts.deps.ImportEvent(raw); err == nil {
				t.Fatal("broken copy reference accepted")
			}
			rooms, err := ts.db.ListRooms(ev.ID)
			if err != nil || len(rooms) != 1 || rooms[0].ID != ts.showID {
				t.Fatalf("existing rooms changed: %+v %v", rooms, err)
			}
		})
	}
}

func TestEventCopyRespectsUpdateReservation(t *testing.T) {
	ts := newAPITest(t)
	ev, _ := ts.db.ResolveEvent(ts.eventCode)
	raw, err := ts.deps.ExportEvent(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]any
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	room := b["rooms"].([]any)[0].(map[string]any)
	room["runtime"].(map[string]any)["running"] = true
	incoming, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := ts.engines.ReserveUpdate()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if _, err := ts.deps.ImportEvent(incoming); !errors.Is(err, timerpi.ErrUpdatePending) {
		t.Fatalf("copy bypassed update reservation: %v", err)
	}
	rt, _, err := ts.db.LoadRuntime(ts.showID)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Running {
		t.Fatal("copy restored a running timer during install")
	}
	cancel()
	if _, err := ts.deps.ImportEvent(raw); err != nil {
		t.Fatalf("copy after canceled install: %v", err)
	}
}
