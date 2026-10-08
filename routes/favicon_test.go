package routes_test

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

// The tab icon is the square TimerPi clock mark (owner 2026-10-08), on every
// page and for browsers that probe /favicon.ico.
func TestFavicon(t *testing.T) {
	ts := newAPITest(t)
	cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := cl.Get(ts.srv.URL + "/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if loc := res.Header.Get("Location"); loc != "/img/favicon.svg" {
		t.Errorf("/favicon.ico → %q, want /img/favicon.svg", loc)
	}
	// The binary embeds public/; the test server has no static tree.
	if svg, err := os.ReadFile("../public/img/favicon.svg"); err != nil || !strings.Contains(string(svg), `viewBox="0 0 96 96"`) {
		t.Errorf("public/img/favicon.svg should be the square mark: %v", err)
	}
	for _, path := range []string{"/", "/e/" + ts.eventCode, "/d/" + ts.showCode, "/a/" + ts.showCode} {
		_, page := ts.anon("GET", path, nil, "")
		if !strings.Contains(string(page), `<link rel="icon" href="/img/favicon.svg"`) {
			t.Errorf("%s has no favicon link", path)
		}
	}
}
