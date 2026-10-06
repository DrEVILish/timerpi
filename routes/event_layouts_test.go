package routes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
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

	// A moderator of Stark only: targets show Stark only, and naming a
	// Banner screen is refused.
	mod := newPersona(ts)
	mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode+"/login", `{"pw":""}`)
	_, tg := mod.do("GET", "/api/shows/"+ts.showCode+"/layout-targets?kind=walkin", "")
	if strings.Contains(tg, "Banner") {
		t.Errorf("moderator sees another room's screens: %s", tg)
	}
	if code, _ := mod.do("POST", "/api/shows/"+ts.showCode+"/layouts", fmt.Sprintf(
		`{"name":"Sneaky","template":"room","screens":[{"room":%q,"name":"Banner door"}]}`, banner.Code)); code != http.StatusForbidden {
		t.Errorf("moderator switched another room's screen: %d", code)
	}
}
