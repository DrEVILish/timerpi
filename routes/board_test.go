// package routes tests — display board (Agent N): board CRUD + ?view=board
// against REAL templates + engine + SQLite over httptest (reuses the
// api_test.go harness: newAPITest boots routes.New, which mounts
// RegisterBoards with one line).
package routes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"timerpi/config"
	"timerpi/views"
)

// boardIDs lists the test show's boards as generic maps.
func (ts *apiTest) boardsList(t *testing.T) []map[string]any {
	t.Helper()
	code, body := ts.call("GET", "/api/shows/"+ts.showCode+"/boards", nil, "")
	if code != http.StatusOK {
		t.Fatalf("list boards: %d %s", code, body)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("list boards json: %v (%s)", err, body)
	}
	return list
}

// CRUD lifecycle: list seeds the default, create/rename/layout/delete round-trip.
func TestBoardCRUD(t *testing.T) {
	ts := newAPITest(t)

	// First list seeds the show default ("Main", factory layout).
	list := ts.boardsList(t)
	if len(list) != 1 || list[0]["name"] != "Main" {
		t.Fatalf("seeded list = %v", list)
	}
	defID := int64(list[0]["id"].(float64))
	lay, _ := json.Marshal(list[0]["layout"])
	if !bytes.Contains(lay, []byte(`"type":"countdown"`)) {
		t.Errorf("factory layout missing countdown: %s", lay)
	}

	// Create → 201 with factory layout.
	code, body := ts.call("POST", "/api/shows/"+ts.showCode+"/boards", []byte(`{"name":"Lobby"}`), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created map[string]any
	_ = json.Unmarshal(body, &created)
	newID := int64(created["id"].(float64))
	if newID == defID {
		t.Errorf("create reused the default id")
	}

	// Empty name refused.
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/boards", []byte(`{"name":"  "}`), "application/json"); code != http.StatusBadRequest {
		t.Errorf("empty name: %d (want 400)", code)
	}

	// Rename + shrink to a single tile → 200.
	upd := `{"name":"Foyer","layout":{"v":1,"widgets":[{"id":"r","type":"rate","x":0,"y":0,"w":2,"h":1}]}}`
	code, body = ts.call("PUT", "/api/shows/"+ts.showCode+"/boards/"+itoa(newID), []byte(upd), "application/json")
	if code != http.StatusOK {
		t.Fatalf("update: %d %s", code, body)
	}
	if !bytes.Contains(body, []byte(`"Foyer"`)) || !bytes.Contains(body, []byte(`"rate"`)) {
		t.Errorf("update body = %s", body)
	}

	// Unknown widget type refused.
	bad := `{"layout":{"v":1,"widgets":[{"id":"x","type":"hologram","x":0,"y":0,"w":2,"h":1}]}}`
	if code, _ := ts.call("PUT", "/api/shows/"+ts.showCode+"/boards/"+itoa(newID), []byte(bad), "application/json"); code != http.StatusBadRequest {
		t.Errorf("unknown type: %d (want 400)", code)
	}
	// Overlapping tiles refused.
	overlap := `{"layout":{"v":1,"widgets":[` +
		`{"id":"a","type":"rate","x":0,"y":0,"w":4,"h":2},` +
		`{"id":"b","type":"rate","x":2,"y":1,"w":4,"h":2}]}}`
	if code, _ := ts.call("PUT", "/api/shows/"+ts.showCode+"/boards/"+itoa(newID), []byte(overlap), "application/json"); code != http.StatusBadRequest {
		t.Errorf("overlap: %d (want 400)", code)
	}
	// Missing board → 404.
	if code, _ := ts.call("PUT", "/api/shows/"+ts.showCode+"/boards/424242", []byte(upd), "application/json"); code != http.StatusNotFound {
		t.Errorf("missing board: %d (want 404)", code)
	}

	// Delete → 200, then gone.
	if code, _ := ts.call("DELETE", "/api/shows/"+ts.showCode+"/boards/"+itoa(newID), nil, ""); code != http.StatusOK {
		t.Errorf("delete: %d (want 200)", code)
	}
	if code, _ := ts.call("DELETE", "/api/shows/"+ts.showCode+"/boards/"+itoa(newID), nil, ""); code != http.StatusNotFound {
		t.Errorf("delete again: %d (want 404)", code)
	}
	if got := ts.boardsList(t); len(got) != 1 {
		t.Errorf("after delete: %d boards (want 1)", len(got))
	}
}

// Code-only addressing (Agent L rule): numeric show idents 404 everywhere.
func TestBoardNumeric404(t *testing.T) {
	ts := newAPITest(t)
	numeric := "/api/shows/" + jsonNumber(ts.showID) + "/boards"
	if code, _ := ts.call("GET", numeric, nil, ""); code != http.StatusNotFound {
		t.Errorf("numeric list: %d (want 404)", code)
	}
	if code, _ := ts.call("POST", numeric, []byte(`{"name":"X"}`), "application/json"); code != http.StatusNotFound {
		t.Errorf("numeric create: %d (want 404)", code)
	}
	if code, _ := ts.call("PUT", numeric+"/1", []byte(`{}`), "application/json"); code != http.StatusNotFound {
		t.Errorf("numeric update: %d (want 404)", code)
	}
	if code, _ := ts.call("DELETE", numeric+"/1", nil, ""); code != http.StatusNotFound {
		t.Errorf("numeric delete: %d (want 404)", code)
	}
	// Unknown code 404s too.
	if code, _ := ts.call("GET", "/api/shows/ZZZZZZZZ/boards", nil, ""); code != http.StatusNotFound {
		t.Errorf("unknown code: %d (want 404)", code)
	}
}

// The board view renders every factory tile id + zero-JS initials.
func TestBoardView(t *testing.T) {
	ts := newAPITest(t)

	code, body := ts.call("GET", "/d/"+ts.showCode+"?view=board", nil, "")
	if code != http.StatusOK {
		t.Fatalf("board view: %d %.300s", code, body)
	}
	s := string(body)
	for _, sub := range []string{
		`id="b-grid"`, `data-view="board"`, `id="b-offline"`, `id="b-join-qr"`,
		`id="b-layout"`, `board.`, // rev'd asset name: board.vNN.js (any rev)
		`id="b-w-countdown"`, `id="b-w-cuelabel"`, `id="b-w-speaker"`,
		`id="b-w-nextup"`, `id="b-w-wallclock"`, `id="b-w-progress"`,
		`id="b-w-dayprogress"`, `id="b-w-messages"`, `id="b-w-showtitle"`,
		`id="b-w-rate"`, `id="b-w-schedule"`,
		`data-widget="countdown"`, `grid-column: 1 / span 8`,
	} {
		if !strings.Contains(s, sub) {
			t.Errorf("board view missing %q", sub)
		}
	}
	// Locked by default: no compose chrome without ?edit=1.
	if strings.Contains(s, `id="b-toolbar"`) {
		t.Error("locked board must not render the toolbar")
	}

	// ?edit=1 renders the compose chrome (palette + toggle + reset).
	code, body = ts.call("GET", "/d/"+ts.showCode+"?view=board&edit=1", nil, "")
	if code != http.StatusOK {
		t.Fatalf("board edit view: %d", code)
	}
	s = string(body)
	for _, sub := range []string{
		`id="b-toolbar"`, `id="b-edit-toggle"`, `id="b-palette"`,
		`id="b-settings"`, `id="b-reset"`, `id="b-boards"`, `data-editable="1"`,
		`data-add="countdown"`, `data-add="schedule"`,
	} {
		if !strings.Contains(s, sub) {
			t.Errorf("edit view missing %q", sub)
		}
	}

	// ?board=<id> selects; bad ids 404; numeric show idents 404.
	list := ts.boardsList(t)
	bid := itoa(int64(list[0]["id"].(float64)))
	if code, _ := ts.call("GET", "/d/"+ts.showCode+"?view=board&board="+bid, nil, ""); code != http.StatusOK {
		t.Errorf("board select: %d (want 200)", code)
	}
	// A stale/deleted board id must NOT blank a room with a raw error
	// (owner review round): it falls back to the show's default board.
	if code, b := ts.call("GET", "/d/"+ts.showCode+"?view=board&board=424242", nil, ""); code != http.StatusOK ||
		!strings.Contains(string(b), "data-widget=") {
		t.Errorf("stale board id: %d (want default-board fallback)", code)
	}
	// Unknown SHOW stays a friendly 404; junk board ids soft-land on the
	// default board (owner round: TVs must not break mid-event).
	for _, path := range []string{
		"/d/424242?view=board",
		"/d/notanumber?view=board",
	} {
		if code, _ := ts.call("GET", path, nil, ""); code != http.StatusNotFound {
			t.Errorf("%s: %d (want 404)", path, code)
		}
	}
}

// ParseFS: the whole templates dir incl. display_board.html + b-*.html
// parses (views.New globs *.html + fragments/*.html — new files ride it).
func TestBoardTemplatesParse(t *testing.T) {
	if _, err := views.New(templatesRoot()); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

// Edit auth (NOTES-board §5.4, closed 2026-10-04): ?edit=1 composes FREE
// while no operator password exists, but once one is set the toolbar is
// operator furniture — an unauthed /d/ page renders the read-only board
// (no edit chrome), and a logged-in one keeps it.
func TestBoardEditAuthGate(t *testing.T) {
	ts := newAPITest(t)
	path := "/d/" + ts.showCode + "?view=board&edit=1"

	// Without a password: edit=1 ships the editor chrome (pre-A1 behavior).
	_, body := ts.call("GET", path, nil, "")
	if !bytes.Contains(body, []byte(`b-toolbar`)) || !bytes.Contains(body, []byte(`data-add`)) {
		t.Errorf("password-free page: editor chrome missing (b-toolbar/data-add)")
	}

	// With a password set: same URL, no session → READ-ONLY board.
	prev := config.AuthPassword() // may be empty
	if err := config.SetAuthPassword("c2bench"); err != nil {
		t.Fatalf("SetAuthPassword: %v", err)
	}
	t.Cleanup(func() { _ = config.SetAuthPassword(prev) })
	_, body = ts.call("GET", path, nil, "")
	if bytes.Contains(body, []byte(`b-toolbar`)) || bytes.Contains(body, []byte(`data-add`)) {
		t.Errorf("authed-password page still shipped editor chrome to a stranger")
	}
	if !bytes.Contains(body, []byte(`Main`)) || !bytes.Contains(body, []byte(`b-w-`)) {
		t.Errorf("read-only render lost the board itself (tiles/title)")
	}
}

// itoa is int64→decimal for URL params.
func itoa(n int64) string {
	return strings.TrimSpace(strings.Replace(strings.Replace(strings.Replace(
		jsonNumberOf(n), `"`, "", -1), " ", "", -1), "\n", "", -1))
}

func jsonNumberOf(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
