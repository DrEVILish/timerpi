package routes_test

import (
	"testing"

	"timerpi/boards"
)

// Type → Mounted → Layout: changing a screen's display type or mounting
// refits its built-in layout, and picking a layout keeps the mounting
// when it fits (the owner's "Screen Type / Layout / Rotation" bug).
func TestScreenTypeAndMountingRefitLayout(t *testing.T) {
	ts := newAPITest(t)
	if err := boards.Migrate(ts.db.DB); err != nil {
		t.Fatal(err)
	}
	post := func(path, body string) {
		t.Helper()
		if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/screens/"+path, []byte(body), "application/json"); code != 200 {
			t.Fatalf("%s %s: %d %s", path, body, code, b)
		}
	}
	check := func(step, kind, tpl string, rot int) {
		t.Helper()
		s, err := ts.db.GetScreenByName(ts.showID, "Door")
		if err != nil || s.Kind != kind || s.Template != tpl || s.Rotation != rot {
			t.Fatalf("%s: got kind=%q template=%q rotation=%d, want %q %q %d", step, s.Kind, s.Template, s.Rotation, kind, tpl, rot)
		}
	}
	post("template", `{"name":"Door","template":"room"}`)
	check("walk-in room", "walkin", "room", 0)
	post("config", `{"name":"Door","rotation":90}`)
	check("mounted portrait → twin", "walkin", "room-portrait", 90)
	post("config", `{"name":"Door","kind":"presenter"}`)
	check("type presenter → portrait presenter layout", "presenter", "stage-portrait", 90)
	post("template", `{"name":"Door","template":"timer-portrait"}`)
	check("portrait pick keeps the mounting", "presenter", "timer-portrait", 90)
	post("config", `{"name":"Door","rotation":0}`)
	check("back to landscape → twin", "presenter", "timer", 0)
}

func TestFitTemplate(t *testing.T) {
	for _, c := range []struct {
		key, kind string
		portrait  bool
		want      string
	}{
		{"room", "walkin", false, "room"},
		{"room", "walkin", true, "room-portrait"},
		{"room-portrait", "walkin", false, "room"},
		{"room", "presenter", false, "dsm"},
		{"qawall", "audience", true, "main-portrait"},
		{"dsm", "", true, "stage-portrait"},
	} {
		if got := boards.FitTemplate(c.key, c.kind, c.portrait); got != c.want {
			t.Errorf("FitTemplate(%q,%q,%v) = %q, want %q", c.key, c.kind, c.portrait, got, c.want)
		}
	}
	// Every display type has at least one built-in of each shape.
	for _, kind := range []string{"audience", "walkin", "presenter"} {
		for _, p := range []bool{false, true} {
			got := boards.FitTemplate("", kind, p)
			found := false
			for _, tp := range boards.Templates() {
				if tp.Key == got && tp.Kind == kind && (tp.Layout.Orientation == "portrait") == p {
					found = true
				}
			}
			if !found {
				t.Errorf("no %s built-in for portrait=%v", kind, p)
			}
		}
	}
}
