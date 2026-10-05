// SuperOperator tests (PLAN §11.1, phase 3): the device-password gate,
// the zone-filterable room list with live state, one-room verbs, and bulk
// verbs scoped to a zone. Login/cookie flow mirrors showauth_test.
package routes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"timerpi/config"
	"timerpi/timerpi"
)

func loginSuper(t *testing.T, ts *apiTest, pw string) *http.Cookie {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.srv.URL+"/api/login", strings.NewReader(fmt.Sprintf(`{"pw":%q}`, pw)))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	for _, ck := range res.Cookies() {
		if ck.Name == "tp_auth" {
			return ck
		}
	}
	t.Fatal("no tp_auth cookie from login")
	return nil
}

func superGet(t *testing.T, ts *apiTest, path string, ck *http.Cookie) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.srv.URL+path, nil)
	if ck != nil {
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
	return res.StatusCode, raw
}

func superPost(t *testing.T, ts *apiTest, path, body string, ck *http.Cookie) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if ck != nil {
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
	return res.StatusCode, raw
}

func TestSuperGateAndPage(t *testing.T) {
	ts := newAPITest(t)
	// Open appliance: the panel is reachable (same LAN-trust model).
	code, body := ts.call("GET", "/super", nil, "")
	if code != 200 || !strings.Contains(string(body), "SUPER OPERATOR") {
		t.Fatalf("open panel: %d %.200s", code, body)
	}
	if code, _ := ts.call("GET", "/api/super/rooms", nil, ""); code != 200 {
		t.Fatalf("open rooms: %d", code)
	}
	// With a device password: gated — API callers 401, browsers 302.
	prev := config.AuthPassword()
	if err := config.SetAuthPassword("opensesame"); err != nil {
		t.Fatalf("set pw: %v", err)
	}
	t.Cleanup(func() { _ = config.SetAuthPassword(prev) })
	if code, _ := ts.call("GET", "/api/super/rooms", nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("rooms bypassed password: %d", code)
	}
	if code, _ := ts.call("POST", "/api/super/verb", []byte(`{"code":"X","verb":"go"}`), ""); code != http.StatusUnauthorized {
		t.Fatalf("verb bypassed password: %d", code)
	}
	// Logged-in super operator passes.
	ck := loginSuper(t, ts, "opensesame")
	code, body = superGet(t, ts, "/api/super/rooms", ck)
	if code != 200 || !strings.Contains(string(body), "rooms") {
		t.Fatalf("gated rooms with login: %d %.100s", code, body)
	}
	// The panel page serves for a logged-in browser.
	req, _ := http.NewRequest("GET", ts.srv.URL+"/super", nil)
	req.AddCookie(ck)
	req.Header.Set("Accept", "text/html")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("panel: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("panel with login: %d", res.StatusCode)
	}
}

func TestSuperRoomsAndZoneFilter(t *testing.T) {
	ts := newAPITest(t)
	if err := ts.db.SetShowZone(ts.showID, "Hall A"); err != nil {
		t.Fatalf("zone: %v", err)
	}
	b, err := ts.db.CreateShow("Room B Show")
	if err != nil {
		t.Fatalf("second show: %v", err)
	}
	if err := ts.db.SetShowZone(b.ID, "Hall B"); err != nil {
		t.Fatalf("zone B: %v", err)
	}
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Welcome", DurationMS: 300_000}); err != nil {
		t.Fatalf("cue: %v", err)
	}

	code, body := superGet(t, ts, "/api/super/rooms", nil)
	if code != 200 {
		t.Fatalf("rooms: %d %s", code, body)
	}
	var out struct {
		Rooms []struct {
			Code  string `json:"code"`
			Zone  string `json:"zone"`
			Title string `json:"title"`
		} `json:"rooms"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("rooms body: %s", body)
	}
	if len(out.Rooms) != 2 {
		t.Fatalf("want 2 rooms, got %d: %s", len(out.Rooms), body)
	}
	// Zone filter.
	code, body = superGet(t, ts, "/api/super/rooms?zone=Hall%20A", nil)
	if code != 200 {
		t.Fatalf("filtered rooms: %d", code)
	}
	out.Rooms = nil
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("filtered body: %s", body)
	}
	if len(out.Rooms) != 1 || out.Rooms[0].Zone != "Hall A" || out.Rooms[0].Title != "API Test Show" {
		t.Fatalf("zone filter wrong: %s", body)
	}
}

func TestSuperVerbDrivesRoom(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Welcome", DurationMS: 300_000}); err != nil {
		t.Fatalf("cue: %v", err)
	}
	if code, b := superPost(t, ts, "/api/super/verb",
		fmt.Sprintf(`{"code":%q,"verb":"go"}`, ts.showCode), nil); code != 200 {
		t.Fatalf("verb go: %d %s", code, b)
	}
	_, body := superGet(t, ts, "/api/super/rooms", nil)
	if !strings.Contains(string(body), `"running":true`) || !strings.Contains(string(body), "Welcome") {
		t.Fatalf("go did not start the room: %s", body)
	}
	// Blank is a store verb + shows in the room card.
	if code, _ := superPost(t, ts, "/api/super/verb",
		fmt.Sprintf(`{"code":%q,"verb":"blank"}`, ts.showCode), nil); code != 200 {
		t.Fatalf("verb blank: %d", code)
	}
	if _, body := superGet(t, ts, "/api/super/rooms", nil); !strings.Contains(string(body), `"blanked":true`) {
		t.Fatalf("blank did not land: %s", body)
	}
	// Content verbs are refused (transport only), unknown code 404s.
	if code, _ := superPost(t, ts, "/api/super/verb",
		fmt.Sprintf(`{"code":%q,"verb":"daystart"}`, ts.showCode), nil); code != 400 {
		t.Errorf("content verb accepted: %d", code)
	}
	if code, _ := superPost(t, ts, "/api/super/verb",
		`{"code":"ZZZZ-9999","verb":"go"}`, nil); code != 404 {
		t.Errorf("unknown code: %d", code)
	}
}

func TestSuperBulkZoneScoped(t *testing.T) {
	ts := newAPITest(t)
	if err := ts.db.SetShowZone(ts.showID, "Hall A"); err != nil {
		t.Fatalf("zone: %v", err)
	}
	b, err := ts.db.CreateShow("Hall B Show")
	if err != nil {
		t.Fatalf("second show: %v", err)
	}
	if err := ts.db.SetShowZone(b.ID, "Hall B"); err != nil {
		t.Fatalf("zone B: %v", err)
	}
	// Blank only Hall A.
	if code, body := superPost(t, ts, "/api/super/bulk",
		`{"verb":"blank","zone":"Hall A"}`, nil); code != 200 {
		t.Fatalf("bulk: %d %s", code, body)
	}
	a, _ := ts.db.GetShow(ts.showID)
	other, _ := ts.db.GetShow(b.ID)
	if !a.Blanked {
		t.Error("zone A show not blanked")
	}
	if other.Blanked {
		t.Error("zone B show caught the bulk blank")
	}
	// Unblank everywhere (no zone = all rooms).
	if code, _ := superPost(t, ts, "/api/super/bulk", `{"verb":"unblank"}`, nil); code != 200 {
		t.Fatalf("bulk unblank: %d", code)
	}
	a, _ = ts.db.GetShow(ts.showID)
	other, _ = ts.db.GetShow(b.ID)
	if a.Blanked || other.Blanked {
		t.Error("bulk unblank missed a room")
	}
	// Garbage verb refused before touching anything.
	if gcode, _ := superPost(t, ts, "/api/super/bulk", `{"verb":"format"}`, nil); gcode != 400 {
		t.Errorf("garbage bulk verb: %d", gcode)
	}
}
