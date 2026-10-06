package routes_test

import (
	"net/http"
	"testing"
)

// BUGLOG RW40–RW42: screen rename, match, preset apply and settings
// PATCHes never destroy or half-save data.
func TestScreenRenameMatchPresetAndValidateFirst(t *testing.T) {
	ts := newAPITest(t)
	base := "/api/shows/" + ts.showCode
	post := func(path, body string) (int, []byte) {
		return ts.call("POST", path, []byte(body), "application/json")
	}
	mustPost := func(path, body string) {
		t.Helper()
		if code, b := post(path, body); code != 200 && code != 201 {
			t.Fatalf("%s %s: %d %s", path, body, code, b)
		}
	}
	mustPost(base+"/screens/config", `{"name":"Left","theme":"blue-future","room":"Hall A","kind":"audience","rotation":90}`)
	mustPost(base+"/screens/config", `{"name":"Right","theme":"","room":"Hall B","rotation":180}`)

	// RW40: renaming onto an existing name is a 409 and both screens survive.
	if code, _ := post(base+"/screens/rename", `{"from":"Left","to":"Right"}`); code != http.StatusConflict {
		t.Fatalf("rename onto an existing screen: %d, want 409", code)
	}
	for _, n := range []string{"Left", "Right"} {
		if _, err := ts.db.GetScreenByName(ts.showID, n); err != nil {
			t.Errorf("screen %q lost after a refused rename: %v", n, err)
		}
	}

	// RW41: Match copies theme and display type, keeps room and rotation.
	mustPost(base+"/screens/match", `{"from":"Left"}`)
	r, _ := ts.db.GetScreenByName(ts.showID, "Right")
	if r.Theme != "blue-future" || r.Kind != "audience" {
		t.Errorf("match copied theme %q kind %q, want blue-future audience", r.Theme, r.Kind)
	}
	if r.Room != "Hall B" || r.Rotation != 180 {
		t.Errorf("match changed room %q rotation %d, want Hall B 180", r.Room, r.Rotation)
	}

	// RW41: applying a preset keeps each screen's room.
	code, body := post(base+"/presets", `{"name":"Look"}`)
	if code != http.StatusCreated {
		t.Fatalf("save preset: %d %s", code, body)
	}
	presets, _ := ts.db.ListPresets(ts.showID)
	mustPost(base+"/presets/"+itoa(presets[0].ID)+"/apply", `{}`)
	for n, room := range map[string]string{"Left": "Hall A", "Right": "Hall B"} {
		if s, _ := ts.db.GetScreenByName(ts.showID, n); s.Room != room {
			t.Errorf("preset apply set %s's room to %q, want %q", n, s.Room, room)
		}
	}

	// RW42: a bad rotation saves nothing.
	if code, _ := post(base+"/screens/config", `{"name":"Left","theme":"","room":"Elsewhere","rotation":45}`); code != http.StatusBadRequest {
		t.Fatalf("bad rotation: %d, want 400", code)
	}
	if l, _ := ts.db.GetScreenByName(ts.showID, "Left"); l.Theme != "blue-future" || l.Room != "Hall A" {
		t.Errorf("refused config still saved theme %q room %q", l.Theme, l.Room)
	}

	// RW42: a short supervisor password leaves the event name alone.
	sh0, _ := ts.db.GetShow(ts.showID)
	before, _ := ts.db.GetEvent(sh0.EventID)
	if code, _ := ts.call("PATCH", "/api/events/"+ts.eventCode, []byte(`{"name":"Renamed","password":"123"}`), "application/json"); code != http.StatusBadRequest {
		t.Fatalf("short password: %d, want 400", code)
	}
	if after, _ := ts.db.GetEvent(sh0.EventID); after.Name != before.Name {
		t.Errorf("refused PATCH renamed the event to %q", after.Name)
	}
	// An empty room name is refused before the password changes.
	if code, _ := ts.call("PATCH", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode, []byte(`{"name":" ","password":"new-pw"}`), "application/json"); code != http.StatusBadRequest {
		t.Fatalf("empty room name: %d, want 400", code)
	}
	if sh, _ := ts.db.GetShow(ts.showID); sh.RoomPW != "" {
		t.Error("refused room PATCH still set the room password")
	}
}
