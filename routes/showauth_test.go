// showauth_test.go — the per-show passphrase gate: page gating for operator
// + display, unlock round-trip, passphrase API self-gating, and the WS join
// refusal with token/cookie acceptance.
package routes_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func timeNowAdd() time.Time { return time.Now().Add(3 * time.Second) }

// browserGET is call() with a browser Accept header and manual cookie list.
func browserGET(t *testing.T, ts *apiTest, path, accept string, cookies ...*http.Cookie) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.srv.URL+path, nil)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	var raw []byte
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
	}
	return res, raw
}

func postJSONWithCookies(t *testing.T, ts *apiTest, path string, body string, cookies ...*http.Cookie) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	var raw []byte
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
	}
	return res, raw
}

func TestShowPassphraseGate(t *testing.T) {
	ts := newAPITest(t)

	// Set the show password (open appliance: first-set is allowed).
	res, raw := postJSONWithCookies(t, ts, "/api/shows/"+ts.showCode+"/passphrase", `{"pw":"cat"}`)
	var out struct {
		OK      bool   `json:"ok"`
		Enabled bool   `json:"enabled"`
		Token   string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || res.StatusCode != http.StatusOK || !out.Enabled || out.Token == "" {
		t.Fatalf("set show password: %d %s", res.StatusCode, raw)
	}
	var cook *http.Cookie
	for _, ck := range res.Cookies() {
		if strings.HasPrefix(ck.Name, "tp_show_") {
			cook = ck
		}
	}
	if cook == nil {
		t.Fatalf("no unlock cookie issued on set")
	}

	// Operator page WITHOUT unlock: the lock page (no cue content leaks).
	_, body := browserGET(t, ts, "/c/"+ts.showCode, "text/html")
	if !strings.Contains(string(body), "SHOW PASSWORD") {
		t.Fatalf("dashboard not gated: %.200s", body)
	}
	if strings.Contains(string(body), "Welcome") {
		t.Fatalf("GATED dashboard leaked cue content: %.200s", body)
	}

	// Display page gates too (extra password with no TV carve-out).
	_, body = browserGET(t, ts, "/d/"+ts.showCode, "text/html")
	if !strings.Contains(string(body), "SHOW PASSWORD") {
		t.Fatal("display page not gated")
	}

	// API caller: 401 JSON.
	code, raw2 := ts.call("GET", "/api/shows/"+ts.showCode, nil, "")
	if code != http.StatusUnauthorized || !strings.Contains(string(raw2), "show password") {
		t.Fatalf("api snapshot: %d %s", code, raw2)
	}

	// Wrong password refused.
	res2, raw2 := postJSONWithCookies(t, ts, "/api/shows/"+ts.showCode+"/unlock", `{"pw":"nope"}`)
	if res2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong unlock: %d %s", res2.StatusCode, raw2)
	}

	// Right password unlocks; the dashboard content shows with the cookie.
	res3, raw3 := postJSONWithCookies(t, ts, "/api/shows/"+ts.showCode+"/unlock", `{"pw":"cat"}`)
	var out3 struct {
		OK    bool   `json:"ok"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw3, &out3); err != nil || res3.StatusCode != http.StatusOK || !out3.OK {
		t.Fatalf("unlock: %d %s", res3.StatusCode, raw3)
	}
	var unlockCk *http.Cookie
	for _, ck := range res3.Cookies() {
		if strings.HasPrefix(ck.Name, "tp_show_") {
			unlockCk = ck
		}
	}
	_, body3 := browserGET(t, ts, "/c/"+ts.showCode, "text/html", unlockCk)
	if !strings.Contains(string(body3), "Running order") {
		t.Fatalf("unlocked dashboard still gated: %.200s", body3)
	}

	// WS join without a token → refused; with the token → joined.
	dial := func(headerCookies []string) *websocket.Conn {
		wsURL := "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
		hdr := http.Header{}
		for _, c := range headerCookies {
			hdr.Add("Cookie", c)
		}
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return conn
	}
	join := (func(token, cookieRaw string) map[string]any {
		c := dial(nilIfEmpty(cookieRaw))
		j := map[string]any{"v": 1, "t": "join", "role": "controls", "show": ts.showCode, "peerId": "j1"}
		if token != "" {
			j["showToken"] = token
		}
		rawJ, _ := json.Marshal(j)
		_ = c.WriteMessage(websocket.TextMessage, rawJ)
		c.SetReadDeadline(timeNowAdd())
		_, respRaw, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("join read: %v", err)
		}
		var m map[string]any
		_ = json.Unmarshal(respRaw, &m)
		return m
	})
	if m := join("", ""); m["t"] != "err" || !strings.Contains(anyStr(m["message"]), "show password") {
		t.Fatalf("locked show join allowed: %+v", m)
	}
	if m := join(out3.Token, ""); m["t"] == "err" {
		t.Fatalf("token join refused: %+v", m)
	}
	if m := join("", anyStr(unlockCk.Name)+"="+unlockCk.Value); m["t"] == "err" {
		t.Fatalf("cookie join refused: %+v", m)
	}
}

// operator password set → passphrase API gated behind operator login too.
func TestShowPassphraseUnderOperatorAuth(t *testing.T) {
	ts := newAPITest(t)
	setAuth(t, "opensesame")
	// No cookie → 401.
	code, raw := ts.call("POST", "/api/shows/"+ts.showCode+"/passphrase", []byte(`{"pw":"cat"}`), "application/json")
	if code != http.StatusUnauthorized {
		t.Fatalf("passphrase open under operator auth: %d %s", code, raw)
	}
}

func nilIfEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func anyStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
