package routes_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"timerpi/routes"
)

// Phone sign-in by QR (owner 2026-10-07): only the Event Technician can
// show a code; a code signs one phone in once, within its lifetime; a
// password change voids it.
func TestPhoneSignIn(t *testing.T) {
	ts := newAPITest(t)
	mint := func() (int, string) {
		t.Helper()
		code, body := ts.call("POST", "/api/events/"+ts.eventCode+"/phone-link", []byte(`{"base":"`+ts.srv.URL+`"}`), "application/json")
		var j struct {
			URL string `json:"url"`
			QR  string `json:"qr"`
		}
		_ = json.Unmarshal(body, &j)
		if code == 200 && !strings.HasPrefix(j.QR, "data:image/png;base64,") {
			t.Errorf("no QR image in %s", body)
		}
		return code, j.URL
	}
	// A phone: no cookies, no redirects followed.
	phone := func(link string) (int, string, bool) {
		t.Helper()
		cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := cl.Get(link)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		session := false
		for _, ck := range res.Cookies() {
			if ck.Name == "tp_ev_"+ts.eventCode && ck.Value != "" {
				session = true
			}
		}
		return res.StatusCode, res.Header.Get("Location"), session
	}

	if code, _ := ts.anon("POST", "/api/events/"+ts.eventCode+"/phone-link", []byte(`{}`), "application/json"); code != 401 {
		t.Errorf("anonymous mint: %d, want 401", code)
	}
	code, link := mint()
	if code != 200 || !strings.HasPrefix(link, ts.srv.URL+"/e/"+ts.eventCode+"/phone?t=") {
		t.Fatalf("mint: %d %q", code, link)
	}
	if st, loc, ok := phone(link); st != http.StatusSeeOther || loc != "/e/"+ts.eventCode+"/admin" || !ok {
		t.Fatalf("redeem: %d %q session=%v", st, loc, ok)
	}
	if st, _, ok := phone(link); st == http.StatusSeeOther || ok {
		t.Errorf("a code works once: second use %d session=%v", st, ok)
	}
	u, _ := url.Parse(link)
	tok := u.Query().Get("t")
	if st, _, ok := phone(ts.srv.URL + "/e/" + ts.eventCode + "/phone?t=" + url.QueryEscape(tok[:len(tok)-1]+"0")); st == http.StatusSeeOther || ok {
		t.Errorf("a tampered code signed in: %d", st)
	}

	// Expired.
	old := routes.PhoneLinkTTL
	routes.PhoneLinkTTL = -time.Second
	_, stale := mint()
	routes.PhoneLinkTTL = old
	if st, _, ok := phone(stale); st == http.StatusSeeOther || ok {
		t.Errorf("an expired code signed in: %d", st)
	}

	// A password change voids codes already shown.
	_, pending := mint()
	if code, b := ts.call("PATCH", "/api/events/"+ts.eventCode, []byte(`{"password":"another-pw-1"}`), "application/json"); code != 200 {
		t.Fatalf("password change: %d %s", code, b)
	}
	ts.call("POST", "/api/events/"+ts.eventCode+"/login", []byte(`{"pw":"another-pw-1"}`), "application/json")
	if st, _, ok := phone(pending); st == http.StatusSeeOther || ok {
		t.Errorf("a code from before the password change signed in: %d", st)
	}
}
