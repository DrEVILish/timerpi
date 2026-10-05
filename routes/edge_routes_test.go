package routes_test

import (
	"bytes"
	"image/png"
	"net/http"
	"strings"
	"testing"
)

// A7 notes cap: 4001 characters is one past the sanity ceiling → 400 with a
// factual error; 4000 exactly persists. (No test touched /notes before.)
func TestDayNotesCapBoundary(t *testing.T) {
	ts := newAPITest(t)
	path := "/api/shows/" + ts.showCode + "/notes"

	if code, _ := ts.call("POST", path, []byte(`{"text":"`+strings.Repeat("x", 4001)+`"}`), "application/json"); code != http.StatusBadRequest {
		t.Errorf("4001-char notes: %d, want 400", code)
	}
	if code, _ := ts.call("POST", path, []byte(`{"text":"`+strings.Repeat("x", 4000)+`"}`), "application/json"); code != http.StatusOK {
		t.Errorf("4000-char notes: %d, want 200", code)
	}
	// The 4001 attempt persisted nothing (the 4000 run is the only write).
	// Notes ride the snapshot's show object (Snapshot.Show.Notes), not the top
	// level — that is the A7 wire shape the daysheet reads.
	snap := ts.snapshot()
	show, _ := snap["show"].(map[string]any)
	if got, _ := show["notes"].(string); got != strings.Repeat("x", 4000) {
		t.Errorf("notes after the boundary dance: len %d, want 4000", len(got))
	}
}

// QR clamps (CONTRACT-UI frag-share callers pass arbitrary ?size=):
// missing/bad size → 320 default, <64 → 64, >1024 → 1024; data >512 → 400,
// empty → 400. Sizes are asserted by decoding the PNG dimensions (a 1-char
// QR compresses to <1 KiB at any size, so byte-length floors can't prove
// the clamp — IHDR width/height can).
func TestQRBoundaryClamps(t *testing.T) {
	ts := newAPITest(t)
	base := "/api/shows/" + ts.showCode + "/qr?data=x"
	cases := []struct {
		name, url string
		wantDim   int
	}{
		{"default", base, 320},
		{"tiny clamps to 64", base + "&size=8", 64},
		{"huge clamps to 1024", base + "&size=99999", 1024},
		{"bad size falls back to 320", base + "&size=abc", 320},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := ts.call("GET", c.url, nil, "")
			if code != 200 {
				t.Fatalf("%s: %d, want 200", c.name, code)
			}
			cfg, err := png.DecodeConfig(bytes.NewReader(body))
			if err != nil {
				t.Fatalf("%s: not a decodable PNG (%d bytes): %v", c.name, len(body), err)
			}
			if cfg.Width != c.wantDim || cfg.Height != c.wantDim {
				t.Errorf("%s: PNG is %dx%d, want %dx%d", c.name, cfg.Width, cfg.Height, c.wantDim, c.wantDim)
			}
		})
	}
	if code, _ := ts.call("GET", "/api/shows/"+ts.showCode+"/qr", nil, ""); code != http.StatusBadRequest {
		t.Errorf("missing data: %d, want 400", code)
	}
	if code, _ := ts.call("GET", base+strings.Repeat("y", 513), nil, ""); code != http.StatusBadRequest {
		t.Errorf("513-char data: %d, want 400", code)
	}
}

// Import example downloads: fmt outside xlsx|csv|json → 400; each valid
// format produces a nonempty file.
func TestImportExampleBadFormat(t *testing.T) {
	ts := newAPITest(t)
	if code, body := ts.call("GET", "/api/shows/"+ts.showCode+"/import-example?fmt=docx", nil, ""); code != http.StatusBadRequest || !strings.Contains(string(body), "fmt must be") {
		t.Errorf("bad fmt: %d %s", code, body)
	}
	for _, f := range []string{"xlsx", "csv", "json"} {
		if code, body := ts.call("GET", "/api/shows/"+ts.showCode+"/import-example?fmt="+f, nil, ""); code != 200 || len(body) == 0 {
			t.Errorf("valid fmt=%s: %d bytes=%d", f, code, len(body))
		}
	}
}

// Origin edges the middleware tests never sent: an opaque "null" Origin
// (sandboxed iframe) and a foreign Referer fallback — both browser-driven
// and cross-site → 403. Plus the WS-arm unit ShowUnlockedJoin: wrong cookie
// AND wrong join token refused; correct token/cookie accepted.
func TestOriginNullAndRefererFallback(t *testing.T) {
	ts := newAPITest(t)

	do := func(headers map[string]string) int {
		req, _ := http.NewRequest("POST", ts.srv.URL+"/api/shows", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}

	if got := do(map[string]string{"Origin": "null"}); got != http.StatusForbidden {
		t.Errorf("Origin null POST: %d, want 403", got)
	}
	if got := do(map[string]string{"Referer": "https://evil.example.com/frame"}); got != http.StatusForbidden {
		t.Errorf("evil Referer POST: %d, want 403", got)
	}
}
