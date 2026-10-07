package routes_test

import (
	"strings"
	"testing"

	"timerpi/boards"
)

// You pick a layout; the screen's orientation picks its version (owner
// 2026-10-07). Changing the display type moves a built-in to that type's
// layouts; the mounting never changes the layout, only its version.
func TestScreenLayoutVersions(t *testing.T) {
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
	orient := func(query string) string {
		t.Helper()
		_, page := ts.anon("GET", "/d/"+ts.showCode+"?view=board&screen=Door"+query, nil, "")
		for _, o := range []string{"portrait", "landscape"} {
			if strings.Contains(string(page), `data-orientation="`+o+`"`) {
				return o
			}
		}
		return "?"
	}
	post("template", `{"name":"Door","template":"room"}`)
	check("walk-in room", "walkin", "room", 0)
	if o := orient("&tpl=room"); o != "landscape" {
		t.Errorf("unrotated screen got the %s version", o)
	}
	post("config", `{"name":"Door","rotation":90}`)
	check("mounted portrait keeps the layout", "walkin", "room", 90)
	if o := orient("&tpl=room"); o != "portrait" {
		t.Errorf("portrait-mounted screen got the %s version", o)
	}
	if o := orient("&tpl=room&orient=landscape"); o != "landscape" {
		t.Errorf("a device held landscape got the %s version", o)
	}
	post("config", `{"name":"Door","kind":"presenter"}`)
	check("type presenter → a presenter layout", "presenter", "dsm", 90)
	post("template", `{"name":"Door","template":"timer"}`)
	check("picking a layout keeps the mounting", "presenter", "timer", 90)
	post("template", `{"name":"Door","template":"stage-portrait"}`)
	check("a retired -portrait key lands on its layout", "presenter", "stage", 90)
}

func TestFitTemplate(t *testing.T) {
	for _, c := range []struct{ key, kind, want string }{
		{"room", "walkin", "room"},
		{"room", "presenter", "dsm"},
		{"qawall", "audience", "qawall"},
		{"dsm", "", "dsm"},
		{"dsm", "walkin", "event"},
	} {
		if got := boards.FitTemplate(c.key, c.kind); got != c.want {
			t.Errorf("FitTemplate(%q,%q) = %q, want %q", c.key, c.kind, got, c.want)
		}
	}
}
