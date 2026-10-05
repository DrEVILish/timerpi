package routes_test

import (
	"net/http"
	"strings"
	"testing"
)

// E3 blackout round-trip: REST raises/clears, the snapshot flag follows,
// every display surface ships the overlay + body flag, dashboard ships the
// toggle, and the flag survives a show snapshot untouched otherwise.
func TestBlankRoundTripE3(t *testing.T) {
	ts := newAPITest(t)
	path := "/api/shows/" + ts.showCode + "/blank"

	if code, _ := ts.call("POST", path, []byte(`{}`), "application/json"); code != http.StatusBadRequest {
		t.Errorf("blank missing on: %d, want 400", code)
	}

	if code, body := ts.call("POST", path, []byte(`{"on":true}`), "application/json"); code != http.StatusOK || !strings.Contains(string(body), `"blanked":true`) {
		t.Fatalf("blank on: %d %s", code, body)
	}
	if show := ts.snapshot()["show"].(map[string]any); show["blanked"] != true {
		t.Fatalf("snapshot show.blanked = %v, want true", show["blanked"])
	}

	// All display surfaces carry the overlay + a true flag while blanked.
	for _, p := range []string{"/d/" + ts.showCode, "/d/" + ts.showCode + "?view=board"} {
		code, body := ts.call("GET", p, nil, "")
		if code != http.StatusOK {
			t.Fatalf("%s: %d", p, code)
		}
		for _, sub := range []string{`class="tp-blanked"`, `data-blanked="true"`, `STANDBY`} {
			if !strings.Contains(string(body), sub) {
				t.Errorf("%s missing %q while blanked", p, sub)
			}
		}
	}
	for _, v := range []string{"next", "daysheet", "clock"} {
		code, body := ts.call("GET", "/d/"+ts.showCode+"?view="+v, nil, "")
		if code != http.StatusOK || !strings.Contains(string(body), `data-blanked="true"`) {
			t.Errorf("view=%s blank flag: %d", v, code)
		}
	}

	// Release: flag clears everywhere, overlay stays shipped but hidden.
	if code, _ := ts.call("POST", path, []byte(`{"on":false}`), "application/json"); code != http.StatusOK {
		t.Fatalf("blank off: %d", code)
	}
	if show := ts.snapshot()["show"].(map[string]any); show["blanked"] != false {
		t.Fatalf("snapshot show.blanked = %v, want false", show["blanked"])
	}
	code, body := ts.call("GET", "/d/"+ts.showCode, nil, "")
	if code != http.StatusOK || !strings.Contains(string(body), `data-blanked="false"`) || !strings.Contains(string(body), `class="tp-blanked"`) {
		t.Errorf("unblanked display: %d (overlay must ship, flag false)", code)
	}

	// Dashboard ships the toggle (pressed state is JS-painted per snapshot).
	_, body = ts.call("GET", "/c/"+ts.showCode, nil, "")
	if !strings.Contains(string(body), `id="tp-blank"`) {
		t.Error("dashboard missing #tp-blank toggle")
	}
}
