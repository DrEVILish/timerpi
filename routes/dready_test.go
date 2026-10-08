// dready tests (PLAN §11.3 phase 1): the /d/ no-code surface renders the
// READY overlay, /d redirects to /d/, the home page offers the button, and
// the page identity survives the waiting-room capture loop (register →
// mine poll carries ?screen=, which mesh.js screenName() then adopts).
package routes_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestDisplayReadyPage(t *testing.T) {
	ts := newAPITest(t)
	code, body := ts.call("GET", "/d/", nil, "")
	if code != 200 {
		t.Fatalf("/d/: %d %.200s", code, body)
	}
	s := string(body)
	for _, sub := range []string{
		`READY FOR SHOW OPERATOR`, `id="tp-waiting"`,
		`data-waiting="1"`, `runWaiting`, `waiting.js`,
	} {
		if !strings.Contains(s, sub) {
			t.Errorf("/d/ missing %q", sub)
		}
	}
	if strings.Contains(s, `data-show="`) {
		t.Error("/d/ must not carry a show code (it has none)")
	}
	// GET /d (no slash) lands on the same surface (gin trailing-slash 301,
	// client follows).
	code, body = ts.call("GET", "/d", nil, "")
	if code != 200 || !strings.Contains(string(body), "READY FOR SHOW OPERATOR") {
		t.Fatalf("/d redirect: %d %.200s", code, body)
	}
}

func TestHomePageOpenDisplay(t *testing.T) {
	ts := newAPITest(t)
	code, body := ts.call("GET", "/", nil, "")
	if code != 200 {
		t.Fatalf("home: %d", code)
	}
	s := string(body)
	for _, sub := range []string{`href="/d/"`, `Open a screen`, `data-kiosk`, `id="join-form"`, `id="create-form"`, `Event Technician Password`} {
		if !strings.Contains(s, sub) {
			t.Errorf("home missing %q", sub)
		}
	}
}

// The room page's app bar has no "Open Display" link (2026-10-07: it
// opened the legacy /d/<room> page, not a new screen; screens are opened
// on /d/ from the home or Screens page and set up there).
func TestNavDisplayEntity(t *testing.T) {
	ts := newAPITest(t)
	code, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	if code != 200 {
		t.Fatalf("dashboard: %d", code)
	}
	s := string(body)
	for _, sub := range []string{`Change Theme`, `action="/e/` + ts.eventCode + `/leave"`} {
		if !strings.Contains(s, sub) {
			t.Errorf("dashboard appbar missing %q", sub)
		}
	}
	for _, gone := range []string{`Open Display`, `href="/d/` + ts.showCode + `"`} {
		if strings.Contains(s, gone) {
			t.Errorf("dashboard still has %q", gone)
		}
	}
	if strings.Contains(s, "Back to Shows") {
		t.Error("dashboard still says Back to Shows (replaced by Logout)")
	}
	// STATUS U2: no tap-to-fullscreen on screens, the ready card included.
	code, body = ts.call("GET", "/d/", nil, "")
	if code != 200 || strings.Contains(string(body), "requestFullscreen") {
		t.Fatalf("/d/ still forces fullscreen on tap: %d", code)
	}
}

// The /d/ page relies on the SAME waiting-room plumbing the orphaned
// displays use — assert the loop it runs end to end: register (upsert),
// capture, then one /mine poll hands back the code with its identity.
func TestDisplayReadyCaptureLoop(t *testing.T) {
	ts := newAPITest(t)
	// The /d/ display registers itself…
	if code, b := ts.call("POST", "/api/waiting/register",
		[]byte(`{"name":"Screen-DREADY","host":"timerpi.local"}`), ""); code != 200 {
		t.Fatalf("register: %d %s", code, b)
	}
	// …an operator lists it and captures it into a show…
	if code, b := ts.call("GET", "/api/waiting", nil, ""); code != 200 || !strings.Contains(string(b), "Screen-DREADY") {
		t.Fatalf("waiting list: %d %s", code, b)
	}
	// id of the row: first row is 1 in a fresh db
	if code, b := ts.call("POST", "/api/waiting/1/capture",
		[]byte(fmt.Sprintf(`{"code":%q}`, ts.showCode)), ""); code != 200 {
		t.Fatalf("capture: %d %s", code, b)
	}
	// …and the next poll hops with the identity intact (?screen= adoption
	// is mesh.js's job on arrival; here the code itself must come back).
	code, b := ts.call("GET", "/api/waiting/mine?name=Screen-DREADY&host=timerpi.local", nil, "")
	if code != 200 {
		t.Fatalf("mine: %d %s", code, b)
	}
	if !strings.Contains(string(b), `"assigned":"`+ts.showCode+`"`) {
		t.Fatalf("mine body: %s", b)
	}
	// consume-once: the second poll is empty
	code, b = ts.call("GET", "/api/waiting/mine?name=Screen-DREADY&host=timerpi.local", nil, "")
	if code != 200 || strings.Contains(string(b), `"assigned":"`+ts.showCode+`"`) {
		t.Fatalf("second poll re-served: %d %s", code, b)
	}
}

// The footer carries the Event ID and a ping readout, not the session code
// (2026-10-07).
func TestRoomFooterEventID(t *testing.T) {
	ts := newAPITest(t)
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	s := string(body)
	foot := s[strings.Index(s, `<footer class="app-status">`):]
	foot = foot[:strings.Index(foot, `</footer>`)]
	for _, want := range []string{`Event ID`, `id="conn-ping"`} {
		if !strings.Contains(foot, want) {
			t.Errorf("footer missing %q: %s", want, foot)
		}
	}
	if strings.Contains(foot, `Session`) {
		t.Errorf("footer still shows the session: %s", foot)
	}
}
