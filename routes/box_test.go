package routes_test

import (
	"net/http"
	"strings"
	"testing"
)

// BUGLOG RC5: box settings need the box password. Creating an event (open
// to anyone on the network) or holding any event's supervisor password
// never unlocks them.
func TestBoxPasswordGuardsSettings(t *testing.T) {
	ts := newAPITest(t) // the shared client set the box password

	// A stranger creates their own event and signs in as its SuperOperator.
	stranger := newPersona(ts)
	if code, body := stranger.do("POST", "/api/events", `{"name":"Mine","password":"mine-pw","rooms":["X"]}`); code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create event: %d %s", code, body)
	}
	for _, path := range []string{"/api/osc", "/api/theme"} {
		method := "GET"
		body := ""
		if path == "/api/theme" {
			method, body = "POST", `{"theme":"tron"}`
		}
		if code, _ := stranger.do(method, path, body); code != http.StatusUnauthorized {
			t.Errorf("event SuperOperator %s %s: %d, want 401", method, path, code)
		}
	}
	// The settings page sends a browser to the box sign-in.
	code, _ := stranger.do("GET", "/settings", "", "text/html")
	if code != http.StatusSeeOther {
		t.Errorf("/settings without box session: %d, want 303 to /box", code)
	}
	// Setup is one-shot; a wrong password is refused.
	if code, _ := stranger.do("POST", "/api/box/setup", `{"password":"takeover-pw"}`); code != http.StatusConflict {
		t.Errorf("second setup: %d, want 409", code)
	}
	if code, _ := stranger.do("POST", "/api/box/login", `{"password":"wrong-password"}`); code != http.StatusUnauthorized {
		t.Errorf("wrong box password: %d, want 401", code)
	}

	// The real admin signs in on a second browser; changing the password
	// signs the first browser out.
	admin := newPersona(ts)
	if code, _ := admin.do("POST", "/api/box/login", `{"password":"`+testBoxPW+`"}`); code != 200 {
		t.Fatalf("box login: %d", code)
	}
	if code, _ := admin.do("GET", "/api/osc", ""); code != 200 {
		t.Errorf("box admin GET /api/osc: %d", code)
	}
	if code, _ := ts.call("GET", "/api/osc", nil, ""); code != 200 {
		t.Fatalf("shared client before change: %d", code)
	}
	if code, body := admin.do("POST", "/api/box/password", `{"current":"`+testBoxPW+`","password":"new-box-password"}`); code != 200 {
		t.Fatalf("change: %d %s", code, body)
	}
	if code, _ := ts.call("GET", "/api/osc", nil, ""); code != http.StatusUnauthorized {
		t.Errorf("old box session after change: %d, want 401", code)
	}
	if code, _ := admin.do("GET", "/api/osc", ""); code != 200 {
		t.Errorf("changer stays signed in: %d", code)
	}
	if code, body := admin.do("POST", "/api/box/password", `{"current":"new-box-password","password":"short"}`); code != http.StatusBadRequest || !strings.Contains(body, "8 characters") {
		t.Errorf("short password: %d %s", code, body)
	}
}

// A fresh box: nobody is box admin until someone sets the password, and
// the /box page offers that setup.
func TestBoxSetupPageOnFreshBox(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.Exec(`DELETE FROM settings WHERE key = 'box.pw_hash'`); err != nil {
		t.Fatal(err)
	}
	p := newPersona(ts)
	if code, _ := p.do("GET", "/api/osc", ""); code != http.StatusUnauthorized {
		t.Errorf("fresh box /api/osc: %d, want 401", code)
	}
	code, body := p.do("GET", "/box", "", "text/html")
	if code != 200 || !strings.Contains(body, `id="box-setup"`) {
		t.Errorf("fresh /box: %d, setup form missing", code)
	}
}
