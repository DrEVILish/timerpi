// Capture-modal tests (PLAN §11.2, owner round): capturing a waiting
// display names it and sets Theme / Room / Layout in one step; the config
// is on the registry BEFORE the display hops; the claimed display leaves
// the waiting list; the poll carries the adopted screen name.
package routes_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"timerpi/boards"
)

func TestCaptureWithConfig(t *testing.T) {
	ts := newAPITest(t)
	// A waiting display registers itself.
	if code, b := ts.call("POST", "/api/waiting/register",
		[]byte(`{"name":"Screen-XXXX","host":"tv.local"}`), ""); code != 200 {
		t.Fatalf("register: %d %s", code, b)
	}
	// The show's default board exists (seeded) — grab its id for the modal.
	code, b := ts.call("GET", "/api/shows/"+ts.showCode+"/boards", nil, "")
	if code != 200 {
		t.Fatalf("boards: %d %s", code, b)
	}
	var boardList []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(b, &boardList); err != nil || len(boardList) == 0 {
		t.Fatalf("boards body: %s", b)
	}
	bid := boardList[0].ID

	// The operator captures with the full modal payload.
	if code, b = ts.call("POST", "/api/waiting/1/capture", []byte(fmt.Sprintf(
		`{"code":%q,"name":"Screen 5","theme":"lcars","room":"Hall A","boardId":%d}`, ts.showCode, bid)), ""); code != 200 {
		t.Fatalf("capture: %d %s", code, b)
	}
	if !strings.Contains(string(b), `"name":"Screen 5"`) {
		t.Fatalf("capture reply: %s", b)
	}

	// The config is on the registry BEFORE the display joins.
	scr, err := ts.db.GetScreenByName(ts.showID, "Screen 5")
	if err != nil {
		t.Fatalf("screen row: %v", err)
	}
	if scr.Theme != "lcars" || scr.Room != "Hall A" || scr.BoardID != bid {
		t.Fatalf("screen config wrong: %+v", scr)
	}

	// The waiting list no longer shows the captured display.
	code, b = ts.call("GET", "/api/waiting", nil, "")
	if code != 200 || strings.Contains(string(b), "Screen-XXXX") {
		t.Fatalf("captured display still listed: %d %s", code, b)
	}

	// The display's poll hands back the code AND the adopted name, once.
	code, b = ts.call("GET", "/api/waiting/mine?name=Screen-XXXX&host=tv.local", nil, "")
	if code != 200 || !strings.Contains(string(b), `"assigned":"`+ts.showCode+`"`) ||
		!strings.Contains(string(b), `"screen":"Screen 5"`) {
		t.Fatalf("mine: %d %s", code, b)
	}
	if code, b = ts.call("GET", "/api/waiting/mine?name=Screen-XXXX&host=tv.local", nil, ""); strings.Contains(string(b), fmt.Sprintf(`"assigned":"%s"`, ts.showCode)) {
		t.Fatalf("re-poll re-served: %d %s", code, b)
	}

	// Blank capture (no modal fields) keeps the display's own name and the
	// operator-default theme. The row id comes from the LIST (the operator
	// UI never hardcodes ids).
	if code, _ := ts.call("POST", "/api/waiting/register",
		[]byte(`{"name":"TV-2","host":"tv2.local"}`), ""); code != 200 {
		t.Fatalf("register 2: %d", code)
	}
	code, b = ts.call("GET", "/api/waiting", nil, "")
	if code != 200 {
		t.Fatalf("list: %d %s", code, b)
	}
	var list struct {
		Waiting []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"waiting"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("list body: %s", b)
	}
	tv2 := int64(0)
	for _, w := range list.Waiting {
		if w.Name == "TV-2" {
			tv2 = w.ID
		}
	}
	if tv2 == 0 {
		t.Fatalf("TV-2 not in the waiting list: %s", b)
	}
	if code, b = ts.call("POST", fmt.Sprintf("/api/waiting/%d/capture", tv2), []byte(fmt.Sprintf(`{"code":%q}`, ts.showCode)), ""); code != 200 {
		t.Fatalf("plain capture: %d %s", code, b)
	}
	if !strings.Contains(string(b), `"name":"TV-2"`) {
		t.Fatalf("plain capture reply: %s", b)
	}
	scr2, err := ts.db.GetScreenByName(ts.showID, "TV-2")
	if err != nil || scr2.Theme != "" || scr2.Room != "" {
		t.Fatalf("plain capture config: %+v err %v", scr2, err)
	}

	// Bad theme refused; unknown board refused; unknown waiting id 404s.
	if code, _ := ts.call("POST", fmt.Sprintf("/api/waiting/%d/capture", tv2),
		[]byte(fmt.Sprintf(`{"code":%q,"theme":"HAS DOTS"}`, ts.showCode)), ""); code != 400 {
		t.Errorf("bad theme accepted: %d", code)
	}
	if code, _ := ts.call("POST", fmt.Sprintf("/api/waiting/%d/capture", tv2),
		[]byte(fmt.Sprintf(`{"code":%q,"boardId":999}`, ts.showCode)), ""); code != 400 {
		t.Errorf("unknown board accepted: %d", code)
	}
	if code, _ := ts.call("POST", "/api/waiting/999/capture",
		[]byte(fmt.Sprintf(`{"code":%q}`, ts.showCode)), ""); code != 404 {
		t.Errorf("unknown waiting id: %d", code)
	}

	// The gallery payload carries the room so the operator sees locations.
	code, b = ts.call("GET", "/api/shows/"+ts.showCode+"/screens", nil, "")
	if code != 200 || !strings.Contains(string(b), `"room":"Hall A"`) {
		t.Fatalf("screens payload room: %d %.200s", code, b)
	}
}

// Layout assignment steers the DISPLAY (owner report): a named screen with
// an assigned board loads its board view even from the bare stage URL; an
// unassigned screen stays on the stage.
func TestLayoutAssignmentSteersStage(t *testing.T) {
	ts := newAPITest(t)
	boards.Migrate(ts.db.DB)
	if _, err := boards.CreateBoard(ts.db.DB, ts.showID, "Lobby Display",
		`{"v":1,"widgets":[{"id":"clock","type":"wallclock","x":0,"y":0,"w":4,"h":1}]}`); err != nil {
		t.Fatalf("board: %v", err)
	}
	// Two waiting displays captured — one assigned the board, one not.
	for i, name := range []string{"TV-1", "TV-2"} {
		if code, _ := ts.call("POST", "/api/waiting/register",
			[]byte(fmt.Sprintf(`{"name":%q,"host":"tv%d.local"}`, name, i)), ""); code != 200 {
			t.Fatalf("register %s: %d", name, code)
		}
	}
	var ids []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	code, b := ts.call("GET", "/api/waiting", nil, "")
	json.Unmarshal([]byte(b), &struct {
		Waiting []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"waiting"`
	}{Waiting: ids})
	_ = ids
	// (list order is id ASC; capture both by name lookup)
	wlist := struct {
		Waiting []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"waiting"`
	}{}
	json.Unmarshal([]byte(b), &wlist)
	byName := map[string]int64{}
	for _, w := range wlist.Waiting {
		byName[w.Name] = w.ID
	}
	for _, name := range []string{"TV-1", "TV-2"} {
		if code, bb := ts.call("POST", fmt.Sprintf("/api/waiting/%d/capture", byName[name]),
			[]byte(fmt.Sprintf(`{"code":%q}`, ts.showCode)), ""); code != 200 {
			t.Fatalf("capture %s: %d %s", name, code, bb)
		}
	}
	// Assign the board to TV-1 only.
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/screens/config",
		[]byte(`{"name":"TV-1","boardId":1}`), ""); code != 200 {
		t.Fatalf("config: %d %s", code, b)
	}
	// TV-1's stage URL lands on the board (redirect follows to the widget grid).
	code, b = ts.call("GET", "/d/"+ts.showCode+"?screen=TV-1", nil, "")
	if code != 200 || !strings.Contains(string(b), `data-widget="wallclock"`) {
		t.Fatalf("assigned screen did not render its board: %d %.200s", code, b)
	}
	// TV-2 (unassigned) still gets the stage.
	code, b = ts.call("GET", "/d/"+ts.showCode+"?screen=TV-2", nil, "")
	if code != 200 || strings.Contains(string(b), `data-widget=`) {
		t.Fatalf("unassigned screen should stay on the stage: %d %.200s", code, b)
	}
}

// The capture modal's Template pick (layout round): capturing with a
// template builds that screen's OWN board from the named layout
// (re-capture replaces it, never duplicates) and assigns it.
func TestCaptureWithTemplate(t *testing.T) {
	ts := newAPITest(t)
	if err := boards.Migrate(ts.db.DB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, pair := range []struct{ id, name string }{{"1", "TV-1"}} {
		_ = pair
	}
	if code, _ := ts.call("POST", "/api/waiting/register",
		[]byte(`{"name":"TV-1","host":"tv.local"}`), ""); code != 200 {
		t.Fatal("register")
	}
	if code, _ := ts.call("POST", "/api/waiting/1/capture",
		[]byte(fmt.Sprintf(`{"code":%q,"name":"TV-1","template":"room"}`, ts.showCode)), ""); code != 200 {
		t.Fatal("capture with template")
	}
	// The screen's own board exists, holds the room widgets, and is assigned.
	list, _ := boards.ListBoards(ts.db.DB, ts.showID)
	mine := 0
	var mineID int64
	for _, b := range list {
		if b.Name == "TV-1 layout" {
			mine++
			mineID = b.ID
			if !strings.Contains(b.LayoutJSON(), `"type":"joinqr"`) {
				t.Errorf("template widgets missing: %.200s", b.LayoutJSON())
			}
		}
	}
	if mine != 1 {
		t.Fatalf("want exactly 1 built board, got %d", mine)
	}
	if scr, err := ts.db.GetScreenByName(ts.showID, "TV-1"); err != nil || scr.BoardID != mineID {
		t.Fatalf("board not assigned to the screen: %+v err %v", scr, err)
	}
	// Recapture with a DIFFERENT template → replaced, not duplicated.
	if code, _ := ts.call("POST", "/api/waiting/register",
		[]byte(`{"name":"TV-1","host":"tv.local"}`), ""); code != 200 {
		t.Fatal("re-register")
	}
	if code, _ := ts.call("POST", "/api/waiting/1/capture",
		[]byte(fmt.Sprintf(`{"code":%q,"name":"TV-1","template":"break"}`, ts.showCode)), ""); code != 200 {
		t.Fatal("recapture")
	}
	list2, _ := boards.ListBoards(ts.db.DB, ts.showID)
	n := 0
	for _, b := range list2 {
		if b.Name == "TV-1 layout" {
			n++
			if !strings.Contains(b.LayoutJSON(), `"type":"wallclock"`) {
				t.Errorf("replaced board kept the old template: %.200s", b.LayoutJSON())
			}
		}
	}
	if n != 1 {
		t.Fatalf("re-capture duplicated the board: %d", n)
	}
	// Unknown template refused.
	if code, _ := ts.call("POST", "/api/waiting/1/capture",
		[]byte(fmt.Sprintf(`{"code":%q,"template":"spaceship"}`, ts.showCode)), ""); code != 400 {
		t.Errorf("unknown template accepted: %d", code)
	}
}

// Assets picker: two uploads list with names (map config rides this).
func TestAssetList(t *testing.T) {
	ts := newAPITest(t)
	img := append([]byte(pngHeader), bytes.Repeat([]byte{9}, 16)...)
	if code, _ := uploadAsset(t, ts, "map-north.png", img); code != 200 {
		t.Fatalf("upload: %d", code)
	}
	if code, b := ts.call("GET", "/api/assets", nil, ""); code != 200 || !strings.Contains(string(b), "map-north.png") {
		t.Fatalf("list: %d %s", code, b)
	}

}
