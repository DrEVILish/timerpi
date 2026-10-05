// auth_test.go — the A1 operator-password gate: HTTP surfaces (redirect for
// browsers, 401 for API, Basic + cookie credentials, password set/clear) and
// the WS join gate (controls denied without token, allowed with; display
// joins always open; commands refused from non-controls sessions).
package routes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"timerpi/config"
)

// setAuth flips the operator password on the global config and restores it.
func setAuth(t *testing.T, pw string) {
	t.Helper()
	prev := config.AuthPassword()
	if err := config.SetAuthPassword(pw); err != nil {
		t.Fatalf("SetAuthPassword: %v", err)
	}
	t.Cleanup(func() { _ = config.SetAuthPassword(prev) })
}

func TestAuthDisabledByDefault(t *testing.T) {
	ts := newAPITest(t)
	code, body := ts.call("GET", "/api/shows", nil, "")
	if code != http.StatusOK {
		t.Fatalf("api/shows with auth disabled: %d %s", code, body)
	}
	code, _ = ts.callType("GET", "/c/"+ts.showCode, nil, "text/html")
	if code != http.StatusOK {
		t.Fatalf("dashboard with auth disabled: %d", code)
	}
}

func TestAuthGateRedirectsAnd401s(t *testing.T) {
	ts := newAPITest(t)
	setAuth(t, "opensesame")

	// Browser GET on an operator page → /login redirect (no-follow client).
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	req, _ := http.NewRequest("GET", ts.srv.URL+"/c/"+ts.showCode, nil)
	req.Header.Set("Accept", "text/html")
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /c: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("dashboard GET with auth: want 302, got %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("redirect target: %q", loc)
	}

	// API GET without Accept-html → 401 JSON.
	code, body := ts.call("GET", "/api/shows", nil, "")
	if code != http.StatusUnauthorized || !strings.Contains(string(body), "operator password") {
		t.Fatalf("api GET with auth: %d %s", code, body)
	}

	// Mutating POST → 401.
	code, _ = ts.call("POST", "/api/shows", []byte(`{"title":"x"}`), "application/json")
	if code != http.StatusUnauthorized {
		t.Fatalf("api POST with auth: %d", code)
	}

	// Exemptions stay open.
	code, _ = ts.call("GET", "/health", nil, "")
	if code != http.StatusOK {
		t.Fatalf("/health gated: %d", code)
	}
	code, _ = ts.callType("GET", "/d/"+ts.showCode, nil, "text/html")
	if code != http.StatusOK {
		t.Fatalf("/d gated: %d", code)
	}
	for _, p := range []string{"/css/timerpi.css", "/src/timerpi.js", "/img/marker.svg", "/ftl/dist/xbmc.css"} {
		req, _ := http.NewRequest("GET", ts.srv.URL+p, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("static %s: %v", p, err)
		}
		res.Body.Close()
		if res.StatusCode == http.StatusFound || res.StatusCode == http.StatusUnauthorized {
			t.Fatalf("static asset gated: %s → %d", p, res.StatusCode)
		}
	}
}

func TestAuthBasicAndCookie(t *testing.T) {
	ts := newAPITest(t)
	setAuth(t, "opensesame")

	// Basic auth credentials pass for scripts.
	req, _ := http.NewRequest("GET", ts.srv.URL+"/api/shows", nil)
	req.SetBasicAuth("operator", "opensesame")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("basic GET: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("basic GET with good creds: %d", res.StatusCode)
	}

	// Login round-trip: wrong pw 401, right pw sets the cookie + token.
	code, _ := ts.call("POST", "/api/login", []byte(`{"pw":"nope"}`), "application/json")
	if code != http.StatusUnauthorized {
		t.Fatalf("bad login: %d", code)
	}
	req, _ = http.NewRequest("POST", ts.srv.URL+"/api/login", strings.NewReader(`{"pw":"opensesame"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	var out struct {
		OK    bool   `json:"ok"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("login body: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !out.OK || out.Token == "" {
		t.Fatalf("login: %d ok=%v", res.StatusCode, out.OK)
	}
	for _, ck := range res.Cookies() {
		if ck.Name == "tp_auth" {
			req, _ = http.NewRequest("GET", ts.srv.URL+"/api/shows", nil)
			req.AddCookie(ck)
			res2, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("cookie GET: %v", err)
			}
			res2.Body.Close()
			if res2.StatusCode != http.StatusOK {
				t.Fatalf("cookie GET gated: %d", res2.StatusCode)
			}
			break
		}
	}
	if out.Token == "" {
		t.Fatalf("no token issued")
	}
}

func TestAuthPasswordSetClearLifecycle(t *testing.T) {
	ts := newAPITest(t)

	// Unset: the endpoint is open (first-set path).
	code, body := ts.call("POST", "/api/auth/password", []byte(`{"pw":"temp123"}`), "application/json")
	var out struct {
		OK      bool   `json:"ok"`
		Enabled bool   `json:"enabled"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || code != http.StatusOK || !out.Enabled {
		t.Fatalf("set password: %d %s", code, body)
	}
	if !config.HasAuth() {
		t.Fatal("auth did not turn on")
	}
	// Now gated...
	code, _ = ts.call("GET", "/api/shows", nil, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("after set: %d", code)
	}
	// ...but the fresh token still works via the cookie issued at set time —
	// simulate by logging in with the new password.
	req, _ := http.NewRequest("POST", ts.srv.URL+"/api/login", strings.NewReader(`{"pw":"temp123"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login after set: %v", err)
	}
	authCk := res.Cookies()[0]
	res.Body.Close()
	req, _ = http.NewRequest("GET", ts.srv.URL+"/api/shows", nil)
	req.AddCookie(authCk)
	res2, _ := http.DefaultClient.Do(req)
	res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("cookie after set: %d", res2.StatusCode)
	}
	// Clear (via cookie) → open again.
	req, _ = http.NewRequest("POST", ts.srv.URL+"/api/auth/password", strings.NewReader(`{"pw":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCk)
	var out2 struct {
		OK      bool `json:"ok"`
		Enabled bool `json:"enabled"`
	}
	res3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := json.NewDecoder(res3.Body).Decode(&out2); err != nil {
		t.Fatalf("clear body: %v", err)
	}
	res3.Body.Close()
	if !out2.OK || out2.Enabled {
		t.Fatalf("clear: ok=%v enabled=%v", out2.OK, out2.Enabled)
	}
	if config.HasAuth() {
		t.Fatal("auth did not clear")
	}
	code, _ = ts.call("GET", "/api/shows", nil, "")
	if code != http.StatusOK {
		t.Fatalf("after clear: %d", code)
	}
	setAuth(t, "") // restore for local safety (although restored by helper already)
}

// ---------------------------------------------------------------------------
// WS join gate.

type wsAuthClient struct {
	conn *websocket.Conn
}

func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func (c *wsAuthClient) join(t *testing.T, role, show, token string) map[string]any {
	t.Helper()
	join := map[string]any{"v": 1, "t": "join", "role": role, "show": show, "peerId": "t1", "joinedAt": 1000}
	if token != "" {
		join["authToken"] = token
	}
	raw, _ := json.Marshal(join)
	if err := c.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("join write: %v", err)
	}
	c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, rawResp, err := c.conn.ReadMessage()
	if err != nil {
		t.Fatalf("join read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(rawResp, &m); err != nil {
		t.Fatalf("resp: %v", err)
	}
	return m
}

func TestWSJoinAuthGate(t *testing.T) {
	ts := newAPITest(t)
	// Token via the same derivation the login endpoint serves.
	setAuth(t, "opensesame")

	// Controls join without a token → refused with the honest error.
	c1 := wsAuthClient{conn: dialWS(t, ts.srv)}
	m := c1.join(t, "controls", ts.showCode, "")
	if m["t"] != "err" || !strings.Contains(fmtStr(m["message"]), "operator password") {
		t.Fatalf("controls join without token: %+v", m)
	}

	// Controls join with the right token → joined.
	tok, _ := json.Marshal(config.AuthToken())
	var tokStr string
	_ = json.Unmarshal(tok, &tokStr)
	c2 := wsAuthClient{conn: dialWS(t, ts.srv)}
	m = c2.join(t, "controls", ts.showCode, tokStr)
	if m["t"] == "err" {
		t.Fatalf("controls join with token refused: %+v", m)
	}

	// Display join without a token → open (fresh stage TV contract).
	c3 := wsAuthClient{conn: dialWS(t, ts.srv)}
	m = c3.join(t, "display", ts.showCode, "")
	if m["t"] == "err" {
		t.Fatalf("display join gated: %+v", m)
	}

	// Spoofed display role cannot command while auth is on. Peers/state
	// frames may be queued ahead — read until the err shows up.
	raw, _ := json.Marshal(map[string]any{"t": "cmd", "action": "go", "args": map[string]any{}})
	if err := c3.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("cmd write: %v", err)
	}
	var cm map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for {
		c3.conn.SetReadDeadline(deadline)
		_, rawResp, err := c3.conn.ReadMessage()
		if err != nil {
			t.Fatalf("cmd resp: %v", err)
		}
		_ = json.Unmarshal(rawResp, &cm)
		if cm["t"] != "err" && time.Now().Before(deadline) {
			continue
		}
		break
	}
	if cm["t"] != "err" || !strings.Contains(fmtStr(cm["message"]), "operator password") {
		t.Fatalf("display cmd allowed: %+v", cm)
	}
}

// fmtStr stringifies any JSON value safely for assertions.
func fmtStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// gin is referenced to keep the import minimal-assertion style consistent
// with api_test.go (TestMode set inside newAPITest).
var _ = gin.TestMode

func TestDefaultThemeB7(t *testing.T) {
	ts := newAPITest(t)
	// Default stays empty (bundled fallback) until set.
	code, raw := ts.call("GET", "/api/theme", nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/theme: %d %s", code, raw)
	}
	var j struct {
		Current string   `json:"current"`
		Themes  []string `json:"themes"`
	}
	if err := json.Unmarshal(raw, &j); err != nil || len(j.Themes) < 1 {
		t.Fatalf("themes list: %d %s", code, raw)
	}
	// Pick a real bundle name when the dist tree is present (integration
	// env), else any charset-valid name (config validation is charset-only;
	// an unknown bundle just falls back at render time).
	target := ""
	for _, n := range j.Themes {
		if n != "xbmc" {
			target = n
			break
		}
	}
	if target == "" {
		target = "alienware"
	}
	code, raw = ts.call("POST", "/api/theme", []byte(`{"theme":"`+target+`"}`), "application/json")
	if code != http.StatusOK {
		t.Fatalf("POST /api/theme: %d %s", code, raw)
	}
	// The homepage now ships it server-side (html[data-theme] + meta).
	_, body := ts.callType("GET", "/", nil, "text/html")
	if !strings.Contains(string(body), `data-theme="`+target+`"`) ||
		!strings.Contains(string(body), `content="`+target+`"`) {
		t.Errorf("homepage does not carry default theme %q", target)
	}
	// Reset to bundled default (empty string clears).
	code, _ = ts.call("POST", "/api/theme", []byte(`{"theme":""}`), "application/json")
	if code != http.StatusOK {
		t.Fatalf("clear theme: %d", code)
	}
	_, body = ts.callType("GET", "/", nil, "text/html")
	for _, sub := range []string{`data-theme="blue-future"`, `content="blue-future"`} {
		if !strings.Contains(string(body), sub) {
			t.Errorf("homepage lost the product default theme: missing %q", sub)
		}
	}
}
