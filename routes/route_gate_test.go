package routes_test

import (
	"strings"
	"testing"

	"timerpi/routes"
)

// openRoutes are the routes a browser with no session may reach: public
// pages, the phone and screen surfaces, sign-in and setup, and a few that
// answer 400/404 before any data (assets check their scope in the handler).
// Every other route must refuse an anonymous request.
var openRoutes = map[string]bool{
	"DELETE /api/assets/:id":                   true,
	"GET /":                                    true,
	"GET /a/:code":                             true,
	"GET /api/assets":                          true,
	"GET /api/audience/:code":                  true,
	"GET /api/audience/:code/qr":               true,
	"GET /api/board-templates":                 true,
	"GET /api/events/:code":                    true,
	"GET /api/import-example":                  true,
	"GET /api/shows/:ident/import-example":     true,
	"GET /api/shows/:ident/qr":                 true,
	"GET /api/shows/:ident/screens/self":       true,
	"GET /api/shows/:ident/walkin":             true,
	"GET /api/theme":                           true,
	"GET /api/pairing/self":                    true, // loopback only (pairing.go)
	"GET /d/box":                               true,
	"GET /api/link":                            true, // signed by the venue (link.go)
	"GET /api/link/bundle":                     true,
	"POST /api/link/register":                  true,
	"GET /api/pairing/status":                  true, // boxes poll it (VENUE-CLOUD §3)
	"GET /api/update/binary":                   true, // signed builds for other boxes (VENUE-CLOUD §14)
	"GET /api/update/manifest":                 true,
	"GET /api/waiting/mine":                    true,
	"GET /assets/:id":                          true,
	"GET /box":                                 true,
	"GET /d/":                                  true,
	"GET /d/:ident":                            true,
	"GET /e/:code":                             true,
	"GET /e/:code/admin":                       true,
	"GET /e/:code/leave":                       true,
	"POST /e/:code/leave":                      true, // same-origin form; drops only this browser's cookies
	"POST /logout":                             true,
	"POST /api/events/import":                  true, // makes a NEW event, like POST /api/events
	"GET /favicon.ico":                         true,
	"GET /health":                              true,
	"GET /logout":                              true,
	"GET /setup":                               true,
	"GET /super":                               true,
	"GET /zone/:name":                          true,
	"POST /api/assets":                         true,
	"POST /api/audience/:code/ask":             true,
	"POST /api/audience/:code/vote":            true,
	"POST /api/box/logout":                     true,
	"POST /api/box/setup":                      true,
	"POST /api/events":                         true,
	"POST /api/events/:code/rooms/:room/login": true,
	"POST /api/shows/:ident/client-log":        true,
	"POST /api/waiting/register":               true,
}

// BUGLOG RS6: the access gate keys on path prefixes, so a new route could
// be silently open. This test probes EVERY registered route without a
// session: it must be listed above as open, or answer 401 / 303 / 403. A
// new route fails here until someone decides which it is.
func TestEveryRouteIsGatedOrListedOpen(t *testing.T) {
	ts := newAPITest(t)
	seen := map[string]bool{}
	for _, ri := range routes.New(&routes.Deps{}).Routes() {
		key := ri.Method + " " + ri.Path
		seen[key] = true
		p := strings.NewReplacer(":ident", ts.showCode, ":code", ts.eventCode, ":room", ts.showCode, ":action", "go",
			":pid", "1", ":mid", "1", ":bid", "1", ":peer", "x", ":id", "1", ":name", "x").Replace(ri.Path)
		if strings.HasPrefix(ri.Path, "/api/audience/:code") || ri.Path == "/a/:code" {
			p = strings.Replace(ri.Path, ":code", ts.showCode, 1)
		}
		code, body := ts.anon(ri.Method, p, []byte(`{}`), "application/json")
		gated := code == 401 || code == 303 || code == 403
		if !gated && !openRoutes[key] {
			t.Errorf("%s answers %d to an anonymous request and is not listed as open: gate it or add it to openRoutes (%.120s)", key, code, body)
		}
	}
	for key := range openRoutes {
		if !seen[key] {
			t.Errorf("openRoutes lists %s, which no longer exists", key)
		}
	}
	// Assets check their scope in the handler: a room's assets need a session.
	if code, _ := ts.anon("GET", "/api/assets?room="+ts.showCode, nil, ""); code != 401 {
		t.Errorf("anonymous asset list: %d, want 401", code)
	}
}
