package routes_test

import (
	"net/http"
	"strings"
	"testing"
)

// BUGLOG RW5: deleting or duplicating a room is SuperOperator-only. A
// moderator of a password-less room (anyone holding the event code) gets
// 401, and the room page hides the Duplicate panel from them.
func TestRoomDeleteCloneNeedSuper(t *testing.T) {
	ts := newAPITest(t)
	mod := newPersona(ts)
	if code, body := mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode+"/login", `{"pw":""}`); code != 200 {
		t.Fatalf("moderator login: %d %s", code, body)
	}
	if code, _ := mod.do("POST", "/api/shows/"+ts.showCode+"/clone", `{"title":"Mine"}`); code != http.StatusUnauthorized {
		t.Errorf("moderator clone: %d, want 401", code)
	}
	if code, _ := mod.do("DELETE", "/api/shows/"+ts.showCode, ""); code != http.StatusUnauthorized {
		t.Errorf("moderator delete: %d, want 401", code)
	}
	_, page := mod.do("GET", "/c/"+ts.showCode, "", "text/html")
	if strings.Contains(page, "show-clone-form") {
		t.Error("moderator sees the Duplicate panel")
	}
	// The SuperOperator still can.
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/clone", []byte(`{"title":"Copy"}`), "application/json"); code != http.StatusCreated {
		t.Errorf("super clone: %d", code)
	}
	_, page2 := ts.call("GET", "/c/"+ts.showCode, nil, "")
	if !strings.Contains(string(page2), "show-clone-form") {
		t.Error("SuperOperator lost the Duplicate panel")
	}
}
