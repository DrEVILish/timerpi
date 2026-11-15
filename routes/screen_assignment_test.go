package routes_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"timerpi/boards"
)

func TestScreenAssignmentStorageFailureIsAtomic(t *testing.T) {
	for _, path := range []string{"config", "match", "preset"} {
		t.Run(path, func(t *testing.T) {
			ts := newAPITest(t)
			base := "/api/shows/" + ts.showCode
			post := func(path, body string) (int, []byte) {
				return ts.call("POST", base+path, []byte(body), "application/json")
			}
			if code, b := post("/screens/config", `{"name":"TV","theme":"blue-future","kind":"audience","rotation":90}`); code != 200 {
				t.Fatalf("setup: %d %s", code, b)
			}
			before, _ := ts.db.GetScreenByName(ts.showID, "TV")
			var endpoint, body string
			switch path {
			case "config":
				endpoint, body = "/screens/config", `{"name":"TV","theme":"paper","kind":"presenter"}`
			case "match":
				if code, b := post("/screens/config", `{"name":"Source","theme":"paper","kind":"presenter"}`); code != 200 {
					t.Fatalf("source setup: %d %s", code, b)
				}
				endpoint, body = "/screens/match", `{"from":"Source"}`
			case "preset":
				preset, err := ts.db.SavePreset(ts.showID, "Fault", `{"screens":[{"name":"TV","theme":"paper","boardId":0,"kind":"presenter","rotation":180},{"name":"Good","theme":"paper","boardId":0,"kind":"presenter","rotation":180}]}`)
				if err != nil {
					t.Fatal(err)
				}
				endpoint, body = fmt.Sprintf("/presets/%d/apply", preset.ID), `{}`
			}
			if path != "config" {
				if code, b := post("/screens/config", `{"name":"Good","kind":"audience","rotation":180}`); code != 200 {
					t.Fatalf("good target setup: %d %s", code, b)
				}
			}
			_, err := ts.db.Exec(`CREATE TRIGGER fail_look BEFORE UPDATE OF kind ON screens
				WHEN NEW.name = 'TV' AND NEW.kind = 'presenter' BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
			if err != nil {
				t.Fatal(err)
			}
			code, response := post(endpoint, body)
			if path == "config" && code != 500 {
				t.Errorf("failed assignment: %d %s", code, response)
			}
			if path != "config" {
				var result map[string]any
				if err := json.Unmarshal(response, &result); err != nil {
					t.Fatal(err)
				}
				if path == "match" && (result["matched"] != float64(1) || result["failed"] != float64(1)) {
					t.Errorf("match counts: %s", response)
				}
				if path == "preset" && result["screens"] != float64(1) {
					t.Errorf("preset count: %s", response)
				}
				good, err := ts.db.GetScreenByName(ts.showID, "Good")
				if err != nil {
					t.Fatal(err)
				}
				if good.Kind != "presenter" || good.Theme != "paper" {
					t.Errorf("batch stopped after one failed target: %+v", good)
				}
			}
			after, err := ts.db.GetScreenByName(ts.showID, "TV")
			if err != nil {
				t.Fatal(err)
			}
			if after.Theme != before.Theme || after.Kind != before.Kind || after.Rotation != before.Rotation || after.Template != before.Template {
				t.Errorf("failed %s partially changed the screen: before %+v, after %+v", path, before, after)
			}
		})
	}
}

func TestScreenAssignmentKeepsKeysMountingAndLegacyPresetFields(t *testing.T) {
	ts := newAPITest(t)
	base := "/api/shows/" + ts.showCode
	post := func(path, body string) {
		t.Helper()
		if code, response := ts.call("POST", base+path, []byte(body), "application/json"); code != 200 {
			t.Fatalf("%s: %d %s", path, code, response)
		}
	}
	layout, err := boards.CreateBoard(ts.db.DB, ts.showID, "Named", "")
	if err != nil {
		t.Fatal(err)
	}
	post("/screens/config", fmt.Sprintf(`{"name":"Source","theme":"paper","boardId":%d,"kind":"walkin"}`, layout.ID))
	post("/screens/config", `{"name":"TV","room":"FOH","kind":"presenter","rotation":270}`)
	key, err := ts.db.ScreenKey(ts.showID, "TV")
	if err != nil {
		t.Fatal(err)
	}
	post("/screens/match", `{"from":"Source"}`)
	screen, err := ts.db.GetScreenByName(ts.showID, "TV")
	if err != nil {
		t.Fatal(err)
	}
	if screen.BoardID != layout.ID || screen.Kind != "walkin" || screen.Rotation != 270 || screen.Room != "FOH" {
		t.Fatalf("match did not preserve the complete assignment: %+v", screen)
	}
	post("/screens/template", `{"name":"TV","template":""}`)
	preset, err := ts.db.SavePreset(ts.showID, "Legacy", `{"screens":[{"name":"TV","theme":"blue-future","boardId":0}]}`)
	if err != nil {
		t.Fatal(err)
	}
	post(fmt.Sprintf("/presets/%d/apply", preset.ID), `{}`)
	screen, err = ts.db.GetScreenByName(ts.showID, "TV")
	if err != nil {
		t.Fatal(err)
	}
	if screen.Template != "" || screen.BoardID != 0 || screen.Kind != "walkin" || screen.Rotation != 270 || screen.Room != "FOH" {
		t.Errorf("legacy preset changed omitted fields or explicit plain timer: %+v", screen)
	}
	if !ts.db.ScreenKeyValid(ts.showID, "TV", key) {
		t.Error("assignment replaced the screen trust key")
	}
}

func TestScreenTemplateFailureDoesNotLeaveASelectedTemplate(t *testing.T) {
	ts := newAPITest(t)
	if err := ts.db.SetScreenConfig(ts.showID, "TV", "", 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.db.Exec(`CREATE TRIGGER fail_look BEFORE UPDATE OF kind ON screens
		WHEN NEW.name = 'TV' AND NEW.kind = 'walkin' BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	code, body := ts.call("POST", "/api/shows/"+ts.showCode+"/screens/template", []byte(`{"name":"TV","template":"room"}`), "application/json")
	if code != 500 {
		t.Fatalf("template selection: %d %s", code, body)
	}
	screen, err := ts.db.GetScreenByName(ts.showID, "TV")
	if err != nil {
		t.Fatal(err)
	}
	if screen.Template != "" || screen.Kind != "" {
		t.Errorf("failed template selection left a partial assignment: %+v", screen)
	}
}

func TestFailedCaptureKeepsScreenWaiting(t *testing.T) {
	ts := newAPITest(t)
	if err := ts.db.RegisterWaitingToken("TV", "bench", "secret"); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := ts.db.Get(&id, `SELECT id FROM waiting_screens WHERE name = 'TV'`); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.db.Exec(`CREATE TRIGGER fail_screen BEFORE INSERT ON screens
		WHEN NEW.name = 'TV' BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	code, body := ts.call("POST", fmt.Sprintf("/api/waiting/%d/capture", id), []byte(fmt.Sprintf(`{"code":%q,"name":"TV","kind":"presenter"}`, ts.showCode)), "application/json")
	if code != 500 {
		t.Fatalf("capture: %d %s", code, body)
	}
	w, err := ts.db.GetWaiting(id)
	if err != nil {
		t.Fatal(err)
	}
	if w.Assigned != "" {
		t.Errorf("failed capture released the waiting screen to %q", w.Assigned)
	}
}
