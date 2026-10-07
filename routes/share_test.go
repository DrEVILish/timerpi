// package routes tests — share panel + code-only addressing (Agent L):
// frag-share renders the 4-4 CODE, build URLs from the code only, and the
// numeric id is refused as an address everywhere public.
package routes_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"timerpi/timerpi"
)

// TestSharePanelAndCodeOnlyAddress: the dashboard/share fragment carries
// the code (4-4) and code-built display/control URLs; numeric /c /d and
// /api/shows/<id> all 404 (scope change: digits are the legacy world).
func TestSharePanelAndCodeOnlyAddress(t *testing.T) {
	ts := newAPITest(t)

	code := ts.ShowCode(ts.showID)
	if !timerpi.ValidCode(code) {
		t.Fatalf("stored code %q not canonical", code)
	}
	fmt4x4 := code[:4] + "-" + code[4:]

	// Dashboard page: frag-share embedded — code readout + code URLs.
	status, body := ts.call("GET", "/c/"+code, nil, "")
	if status != 200 {
		t.Fatalf("dashboard /c/%s: %d", code, status)
	}
	for _, want := range []string{
		`/a/` + code,                 // audience link + mirror
		`/api/shows/` + code + `/qr`, // QR route by code
		`data=%2Fa%2F` + code,        // QR payload is the audience link (edge-encoded)
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	// A numeric link ends right after the id; a random code may merely
	// START with the same digit (that made this test flaky).
	for _, end := range []string{`"`, `?`, `'`, `<`} {
		if bytes.Contains(body, []byte(`/d/`+ts.showIDNumber()+end)) {
			t.Errorf("dashboard still links a numeric display URL")
		}
	}

	// Display page resolves by code (and by dashed/lowercase spelling).
	for _, ident := range []string{code, fmt4x4, strings.ToLower(code)} {
		if st, _ := ts.call("GET", "/d/"+ident, nil, ""); st != 200 {
			t.Errorf("display /d/%s: %d", ident, st)
		}
	}

	// Numeric refusals — every public surface.
	for _, path := range []string{
		"/c/" + ts.showIDNumber(),
		"/d/" + ts.showIDNumber(),
		"/api/shows/" + ts.showIDNumber(),
	} {
		if st, _ := ts.call("GET", path, nil, ""); st != http.StatusNotFound {
			t.Errorf("%s: %d (want 404 — code-only addressing)", path, st)
		}
	}

	// QR is reachable via the code address.
	if st, _ := ts.call("GET", "/api/shows/"+code+"/qr?data=/d/"+code+"&size=128", nil, ""); st != 200 {
		t.Errorf("qr by code: %d", st)
	}
}

// ShowCode reads a show's stored code (test-side convenience).
func (ts *apiTest) ShowCode(id int64) string {
	s, err := ts.db.GetShow(id)
	if err != nil {
		ts.t.Fatalf("get show %d: %v", id, err)
	}
	return s.Code
}

// showIDNumber is the internal id as the legacy URL spelled it.
func (ts *apiTest) showIDNumber() string { return jsonNumber(ts.showID) }
