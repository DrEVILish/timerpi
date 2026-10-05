package routes_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// F1 screens: assign → list → self → match → rename → forget, plus the
// dashboard panel ships. Theme/name validation refuses junk.
func TestScreensFlowF1(t *testing.T) {
	ts := newAPITest(t)
	base := "/api/shows/" + ts.showCode
	screensGet := func() []map[string]any {
		code, body := ts.call("GET", base+"/screens", nil, "")
		if code != 200 {
			t.Fatalf("screens list: %d %s", code, body)
		}
		var out struct {
			Screens []map[string]any `json:"screens"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("screens json: %v", err)
		}
		return out.Screens
	}
	find := func(rows []map[string]any, name string) map[string]any {
		for _, r := range rows {
			if r["name"] == name {
				return r
			}
		}
		return nil
	}

	boards := ts.boardsList(t)
	boardID := int64(boards[0]["id"].(float64))

	post := func(path, body string) (int, []byte) {
		return ts.call("POST", path, []byte(body), "application/json")
	}

	// Validation: junk theme + empty name are 400s.
	if code, _ := post(base+"/screens/config", `{"name":"Stage Left","theme":"Bad Theme!"}`); code != http.StatusBadRequest {
		t.Fatalf("bad theme: %d, want 400", code)
	}
	if code, _ := post(base+"/screens/config", `{"name":"  ","theme":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty name: %d, want 400", code)
	}

	// Assign two screens.
	if code, body := post(base+"/screens/config", `{"name":"Stage Left","theme":"blue-future","boardId":`+itoa(boardID)+`}`); code != 200 {
		t.Fatalf("config Stage Left: %d %s", code, body)
	}
	if code, _ := post(base+"/screens/config", `{"name":"Lobby","theme":""}`); code != 200 {
		t.Fatalf("config Lobby: %d", code)
	}
	row := find(screensGet(), "Stage Left")
	if row == nil || row["theme"] != "blue-future" || row["connected"] != false {
		t.Fatalf("Stage Left row = %v", row)
	}
	if row["boardId"].(float64) != float64(boardID) {
		t.Errorf("board assignment lost: %v", row)
	}

	// Self lookup (what a joining display would also receive pushed).
	code, body := ts.call("GET", base+"/screens/self?name=Stage+Left", nil, "")
	if code != 200 || !strings.Contains(string(body), `"known":true`) || !strings.Contains(string(body), "blue-future") {
		t.Fatalf("self: %d %s", code, body)
	}
	if code, body := ts.call("GET", base+"/screens/self?name=Nope", nil, ""); code != 200 || !strings.Contains(string(body), `"known":false`) {
		t.Errorf("unknown self: %d %s", code, body)
	}

	// Match all: Lobby copies Stage Left's config.
	if code, _ := post(base+"/screens/match", `{"from":"Stage Left"}`); code != 200 {
		t.Fatalf("match: %d", code)
	}
	if r := find(screensGet(), "Lobby"); r == nil || r["theme"] != "blue-future" {
		t.Fatalf("match did not copy: %v", r)
	}

	// Rename carries the config (registry row identity is the name).
	if code, _ := post(base+"/screens/rename", `{"from":"Stage Left","to":"Stage Right"}`); code != 200 {
		t.Fatalf("rename: %d", code)
	}
	rows := screensGet()
	if find(rows, "Stage Left") != nil {
		t.Error("old name survived rename")
	}
	if r := find(rows, "Stage Right"); r == nil || r["theme"] != "blue-future" {
		t.Errorf("renamed row wrong: %v", r)
	}

	// Forget removes a row.
	if code, _ := post(base+"/screens/forget", `{"name":"Lobby"}`); code != 200 {
		t.Fatalf("forget: %d", code)
	}
	if find(screensGet(), "Lobby") != nil {
		t.Error("forgotten screen survived")
	}

	// The dashboard ships the panel.
	_, dash := ts.call("GET", "/c/"+ts.showCode, nil, "")
	for _, sub := range []string{`id="screens-panel"`, `id="tp-screens"`, `id="tp-preset-save"`, `id="tp-preset-import"`} {
		if !strings.Contains(string(dash), sub) {
			t.Errorf("dashboard missing %q", sub)
		}
	}
}

// F2 presets: save snapshots the registry; apply restores it; export and
// import round-trip a plain JSON file (and reject non-preset JSON).
func TestPresetsRoundTripF2(t *testing.T) {
	ts := newAPITest(t)
	base := "/api/shows/" + ts.showCode

	if code, _ := ts.call("POST", base+"/screens/config", []byte(`{"name":"Stage Left","theme":"blue-future","boardId":0}`), "application/json"); code != 200 {
		t.Fatalf("config: %d", code)
	}
	code, body := ts.call("POST", base+"/presets", []byte(`{"name":"Evening"}`), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("preset save: %d %s", code, body)
	}
	var saved struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &saved); err != nil || saved.ID == 0 || saved.Name != "Evening" {
		t.Fatalf("preset body: %s", body)
	}

	// List carries the snapshot data.
	code, body = ts.call("GET", base+"/presets", nil, "")
	if code != 200 || !strings.Contains(string(body), "Stage Left") {
		t.Fatalf("presets list: %d %s", code, body)
	}

	// Wipe the config, apply the preset, expect restoration.
	if code, _ := ts.call("POST", base+"/screens/config", []byte(`{"name":"Stage Left","theme":""}`), "application/json"); code != 200 {
		t.Fatalf("wipe: %d", code)
	}
	if code, _ := ts.call("POST", base+"/presets/"+itoa(saved.ID)+"/apply", nil, ""); code != 200 {
		t.Fatalf("apply: %d", code)
	}
	if _, self := ts.call("GET", base+"/screens/self?name=Stage+Left", nil, ""); !strings.Contains(string(self), "blue-future") {
		t.Fatalf("apply did not restore theme: %s", self)
	}

	// Export → bytes; import them back under a fresh row.
	code, file := ts.call("GET", base+"/presets/"+itoa(saved.ID)+"/export", nil, "")
	if code != 200 || !strings.Contains(string(file), presetExportKind) {
		t.Fatalf("export: %d %.120s", code, file)
	}
	if code, _ := ts.call("POST", base+"/presets/import", file, "application/json"); code != http.StatusCreated {
		t.Fatalf("import: %d", code)
	}
	// Non-preset JSON is refused with the honest phrase.
	if code, _ := ts.call("POST", base+"/presets/import", []byte(`{"title":"nope"}`), "application/json"); code != http.StatusBadRequest {
		t.Errorf("non-preset import: %d, want 400", code)
	}

	// Delete.
	if code, _ := ts.call("DELETE", base+"/presets/"+itoa(saved.ID), nil, ""); code != 200 {
		t.Fatalf("delete preset: %d", code)
	}
}

const presetExportKind = "timerpi-display-preset"
