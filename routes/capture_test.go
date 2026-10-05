// Capture-modal tests (PLAN §11.2, owner round): capturing a waiting
// display names it and sets Theme / Room / Layout in one step; the config
// is on the registry BEFORE the display hops; the claimed display leaves
// the waiting list; the poll carries the adopted screen name.
package routes_test

import (
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
