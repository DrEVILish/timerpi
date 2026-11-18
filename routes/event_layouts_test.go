package routes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"timerpi/boards"
)

// PRODUCT 2026-10-06 (STATUS U10/U11): layouts belong to the event. A
// layout made in one room is listed and usable in every room, and deleting
// the room that made it keeps it for the others.
func TestLayoutsAreEventWide(t *testing.T) {
	ts := newAPITest(t)
	other := ts.newRoom("Banner")
	_, body := ts.call("POST", "/api/shows/"+ts.showCode+"/boards", []byte(`{"name":"Event poster"}`), "application/json")
	var b struct{ ID int64 }
	_ = json.Unmarshal(body, &b)
	_, list := ts.call("GET", "/api/shows/"+other.Code+"/boards", nil, "")
	if !strings.Contains(string(list), "Event poster") {
		t.Fatalf("other room doesn't see the event layout: %s", list)
	}
	if code, b2 := ts.call("POST", "/api/shows/"+other.Code+"/screens/config", []byte(fmt.Sprintf(`{"name":"Banner door","boardId":%d}`, b.ID)), "application/json"); code != 200 {
		t.Fatalf("assign event layout in the other room: %d %s", code, b2)
	}
	if code, _ := ts.call("DELETE", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode, nil, ""); code != 200 {
		t.Fatalf("delete the room that made it: %d", code)
	}
	_, list = ts.call("GET", "/api/shows/"+other.Code+"/boards", nil, "")
	if !strings.Contains(string(list), "Event poster") {
		t.Errorf("layout vanished with the room that made it: %s", list)
	}
	if code, _ := ts.call("GET", fmt.Sprintf("/d/%s?view=board&board=%d", other.Code, b.ID), nil, ""); code != 200 {
		t.Errorf("Banner screen can't render the layout after the delete: %d", code)
	}
}

func TestDeleteEventLayoutClearsAssignmentsAtomically(t *testing.T) {
	ts := newAPITest(t)
	other := ts.newRoom("Banner")
	layout, err := boards.CreateBoard(ts.db, ts.showID, "Event layout", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, room := range []string{ts.showCode, other.Code} {
		status, body := ts.call("POST", "/api/shows/"+room+"/screens/config",
			[]byte(fmt.Sprintf(`{"name":"Door","boardId":%d}`, layout.ID)), "application/json")
		if status != http.StatusOK {
			t.Fatalf("assign layout in %s: %d %s", room, status, body)
		}
	}
	if _, err := ts.db.Exec(`CREATE TRIGGER reject_layout_clear BEFORE UPDATE OF board_id ON screens
		WHEN OLD.board_id > 0 AND NEW.board_id = 0 BEGIN SELECT RAISE(ABORT, 'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	checkAssignments := func(want int64) {
		t.Helper()
		for _, id := range []int64{ts.showID, other.ID} {
			screen, err := ts.db.GetScreenByName(id, "Door")
			if err != nil || screen.BoardID != want {
				t.Fatalf("room %d: screen=%+v error=%v, want board %d", id, screen, err, want)
			}
		}
	}
	path := fmt.Sprintf("/api/shows/%s/boards/%d", other.Code, layout.ID)
	if status, body := ts.call("DELETE", path, nil, ""); status != http.StatusInternalServerError {
		t.Fatalf("failed assignment cleanup: %d %s", status, body)
	}
	if _, err := boards.GetBoard(ts.db, ts.showID, layout.ID); err != nil {
		t.Fatalf("layout removed despite failed cleanup: %v", err)
	}
	checkAssignments(layout.ID)
	if _, err := ts.db.Exec(`DROP TRIGGER reject_layout_clear`); err != nil {
		t.Fatal(err)
	}
	if status, body := ts.call("DELETE", path, nil, ""); status != http.StatusOK {
		t.Fatalf("delete from sibling room: %d %s", status, body)
	}
	checkAssignments(0)
	if status, _ := ts.call("DELETE", path, nil, ""); status != http.StatusNotFound {
		t.Fatalf("deleted layout still exists: %d", status)
	}
}

// STATUS U11: editing a built-in makes a named event layout; the targets
// list offers same-type screens across the event (grouped by room) for the
// SuperOperator, only rooms they moderate for a moderator; the listed
// screens switch to the new layout.
func TestLayoutCopyFromBuiltIn(t *testing.T) {
	ts := newAPITest(t)
	banner := ts.newRoom("Banner")
	cfg := func(room, body string) {
		t.Helper()
		if code, b := ts.call("POST", "/api/shows/"+room+"/screens/config", []byte(body), "application/json"); code != 200 {
			t.Fatalf("config: %d %s", code, b)
		}
	}
	cfg(ts.showCode, `{"name":"Stark door","kind":"walkin"}`)
	cfg(ts.showCode, `{"name":"Stark DSM","kind":"presenter"}`)
	cfg(banner.Code, `{"name":"Banner door","kind":"walkin"}`)
	ts.call("POST", "/api/shows/"+ts.showCode+"/screens/template", []byte(`{"name":"Stark door","template":"room"}`), "application/json")

	_, body := ts.call("GET", "/api/shows/"+ts.showCode+"/layout-targets?kind=walkin", nil, "")
	s := string(body)
	if !strings.Contains(s, "Stark door") || !strings.Contains(s, "Banner door") || strings.Contains(s, "Stark DSM") {
		t.Fatalf("SuperOperator targets (walk-in, whole event): %s", s)
	}
	code, body := ts.call("POST", "/api/shows/"+ts.showCode+"/layouts", []byte(fmt.Sprintf(
		`{"name":"Walk-in v2","template":"room","screens":[{"room":%q,"name":"Stark door"},{"room":%q,"name":"Banner door"}]}`,
		ts.showCode, banner.Code)), "application/json")
	if code != 200 {
		t.Fatalf("copy: %d %s", code, body)
	}
	var out struct{ BoardID int64 }
	_ = json.Unmarshal(body, &out)
	for _, chk := range []struct {
		id   int64
		name string
	}{{ts.showID, "Stark door"}, {banner.ID, "Banner door"}} {
		scr, _ := ts.db.GetScreenByName(chk.id, chk.name)
		if scr.BoardID != out.BoardID || scr.Template != "" {
			t.Errorf("%s not switched to the copy: %+v", chk.name, scr)
		}
	}

	// Moderators don't manage screens or layouts at all (STATUS U25).
	mod := newPersona(ts)
	mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode+"/login", `{"pw":""}`)
	if code, _ := mod.do("GET", "/api/shows/"+ts.showCode+"/layout-targets?kind=walkin", ""); code != http.StatusUnauthorized {
		t.Errorf("moderator layout targets: %d, want 401", code)
	}
	if code, _ := mod.do("POST", "/api/shows/"+ts.showCode+"/layouts", `{"name":"Sneaky","template":"room","screens":[]}`); code != http.StatusUnauthorized {
		t.Errorf("moderator made a layout: %d, want 401", code)
	}
}
