package routes_test

import (
	"net/http"
	"strings"
	"testing"
)

// STATUS U25/U28/U29: a moderator works in Run and Audience only. Screens,
// layouts, presets and screen capture are the SuperOperator's; the room
// page has no Setup tab, no Screens link and no theme picker for them.
func TestModeratorRunAndAudienceOnly(t *testing.T) {
	ts := newAPITest(t)
	mod := newPersona(ts)
	if code, _ := mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode+"/login", `{"pw":""}`); code != 200 {
		t.Fatal("moderator login")
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/shows/" + ts.showCode + "/screens", ""},
		{"POST", "/api/shows/" + ts.showCode + "/screens/config", `{"name":"TV","theme":"tron"}`},
		{"POST", "/api/shows/" + ts.showCode + "/screens/template", `{"name":"TV","template":"room"}`},
		{"GET", "/api/shows/" + ts.showCode + "/boards", ""},
		{"POST", "/api/shows/" + ts.showCode + "/boards", `{"name":"Mine"}`},
		{"GET", "/api/shows/" + ts.showCode + "/presets", ""},
	} {
		if code, _ := mod.do(c.method, c.path, c.body); code != http.StatusUnauthorized {
			t.Errorf("moderator %s %s: %d, want 401", c.method, c.path, code)
		}
	}
	if code, _ := mod.do("GET", "/screens/"+ts.showCode, "", "text/html"); code != http.StatusUnauthorized {
		t.Errorf("moderator Screens page: %d, want 401", code)
	}
	code, page := mod.do("GET", "/c/"+ts.showCode, "", "text/html")
	if code != 200 {
		t.Fatalf("moderator room page: %d", code)
	}
	for _, gone := range []string{`data-tab="setup"`, `href="/screens/` + ts.showCode + `"`, `id="theme-select"`, "Change Theme"} {
		if strings.Contains(page, gone) {
			t.Errorf("moderator room page still has %q", gone)
		}
	}
	for _, kept := range []string{`data-tab="run"`, `data-tab="audience"`, `id="import-file"`} {
		if !strings.Contains(page, kept) {
			t.Errorf("moderator room page lost %q", kept)
		}
	}
	// The SuperOperator still has Screens and the theme picker.
	_, superPage := ts.call("GET", "/c/"+ts.showCode, nil, "")
	if !strings.Contains(string(superPage), `href="/screens/`+ts.showCode+`"`) || !strings.Contains(string(superPage), `id="theme-select"`) {
		t.Error("SuperOperator lost Screens or the theme picker")
	}
}
