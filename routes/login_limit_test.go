package routes_test

import (
	"net/http"
	"testing"
)

// BUGLOG RW12: after a run of wrong supervisor passwords, even the right
// one is refused (429) until the window passes, and no hashing is spent.
func TestLoginFailuresAreLimited(t *testing.T) {
	ts := newAPITest(t)
	p := newPersona(ts)
	for i := 0; i < 8; i++ {
		if code, _ := p.do("POST", "/api/events/"+ts.eventCode+"/login", `{"pw":"guess"}`); code != http.StatusUnauthorized {
			t.Fatalf("wrong guess %d: %d, want 401", i+1, code)
		}
	}
	if code, _ := p.do("POST", "/api/events/"+ts.eventCode+"/login", `{"pw":"`+testSuperPW+`"}`); code != http.StatusTooManyRequests {
		t.Errorf("after 8 failures: %d, want 429", code)
	}
	// Another target is not affected.
	if code, _ := p.do("POST", "/api/box/login", `{"password":"`+testBoxPW+`"}`); code != 200 {
		t.Errorf("box login after event lockout: %d", code)
	}
}

// BUGLOG RW14: a password-less (migrated) event can't be claimed with the
// event code alone; the box password holder can.
func TestLegacyEventClaimNeedsBox(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.Exec(`UPDATE events SET super_hash = '' WHERE code = ?`, ts.eventCode); err != nil {
		t.Fatal(err)
	}
	stranger := newPersona(ts)
	if code, _ := stranger.do("POST", "/api/events/"+ts.eventCode+"/login", `{"pw":""}`); code != http.StatusUnauthorized {
		t.Errorf("code-only claim: %d, want 401", code)
	}
	// The shared client holds the box session.
	if code, b := ts.call("POST", "/api/events/"+ts.eventCode+"/login", []byte(`{"pw":""}`), "application/json"); code != 200 {
		t.Errorf("box admin claim: %d %s", code, b)
	}
}
