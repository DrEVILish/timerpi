package routes_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// persona is one browser with its own cookie jar (a moderator's tablet,
// the SuperOperator's laptop, a stranger).
type persona struct {
	t      *testing.T
	ts     *apiTest
	client *http.Client
}

func newPersona(ts *apiTest) *persona {
	jar, _ := cookiejar.New(nil)
	return &persona{t: ts.t, ts: ts, client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

func (p *persona) do(method, path, body string, accept ...string) (int, string) {
	p.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, p.ts.srv.URL+path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if len(accept) > 0 {
		req.Header.Set("Accept", accept[0])
	}
	res, err := p.client.Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// wsJoin dials /ws with this persona's cookies and returns the first frame.
func (p *persona) wsJoin(role, room string) map[string]any {
	p.t.Helper()
	d := websocket.Dialer{Jar: p.client.Jar, HandshakeTimeout: 3 * time.Second}
	url := "ws" + strings.TrimPrefix(p.ts.srv.URL, "http") + "/ws"
	conn, _, err := d.Dial(url, http.Header{"Origin": []string{p.ts.srv.URL}})
	if err != nil {
		p.t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.WriteJSON(map[string]any{"v": 1, "t": "join", "role": role, "show": room, "peerId": "p-" + role, "joinedAt": 1})
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m map[string]any
	if err := conn.ReadJSON(&m); err != nil {
		p.t.Fatalf("read: %v", err)
	}
	return m
}

func TestEventCreateAndLobby(t *testing.T) {
	ts := newAPITest(t)
	boss := newPersona(ts)

	code, body := boss.do("POST", "/api/events", `{"name":"Conf 2026","password":"chief1","rooms":["Room A","Room B",""]}`)
	if code != http.StatusCreated {
		t.Fatalf("create event: %d %s", code, body)
	}
	var created struct{ Code, Admin string }
	_ = json.Unmarshal([]byte(body), &created)

	// Weak or missing supervisor passwords are refused (6+ characters).
	if code, _ := boss.do("POST", "/api/events", `{"name":"X","password":"","rooms":["A"]}`); code != 400 {
		t.Errorf("event without supervisor password: %d, want 400", code)
	}
	if code, _ := boss.do("POST", "/api/events", `{"name":"X","password":"chief","rooms":["A"]}`); code != 400 {
		t.Errorf("5-character supervisor password: %d, want 400", code)
	}

	// The creator is SuperOperator immediately.
	if code, _ := boss.do("GET", created.Admin, "", "text/html"); code != 200 {
		t.Fatalf("admin page for creator: %d", code)
	}
	// The lobby lists both rooms (blank names skipped) for anyone with the code.
	stranger := newPersona(ts)
	code, body = stranger.do("GET", "/api/events/"+created.Code, "")
	if code != 200 || !strings.Contains(body, "Room A") || !strings.Contains(body, "Room B") || strings.Count(body, `"name":"Room`) != 2 {
		t.Fatalf("lobby: %d %s", code, body)
	}
	if strings.Contains(body, `"isSuper":true`) {
		t.Errorf("stranger reported as SuperOperator")
	}
	// A stranger is bounced from the admin page to the lobby's sign-in.
	if code, _ := stranger.do("GET", created.Admin, "", "text/html"); code != http.StatusFound {
		t.Errorf("stranger admin page: %d, want 302", code)
	}
	if code, _ := stranger.do("PATCH", "/api/events/"+created.Code, `{"name":"pwned"}`); code != 401 {
		t.Errorf("stranger PATCH: %d, want 401", code)
	}
	// Wrong / right supervisor password.
	if code, _ := stranger.do("POST", "/api/events/"+created.Code+"/login", `{"pw":"nope"}`); code != 401 {
		t.Errorf("wrong supervisor password: %d", code)
	}
	if code, _ := stranger.do("POST", "/api/events/"+created.Code+"/login", `{"pw":"chief1"}`); code != 200 {
		t.Errorf("right supervisor password: %d", code)
	}
	if code, _ := stranger.do("PATCH", "/api/events/"+created.Code, `{"name":"Conf 2026 (day 1)"}`); code != 200 {
		t.Errorf("signed-in PATCH: %d", code)
	}
}

func TestModeratorRoomIsolation(t *testing.T) {
	ts := newAPITest(t)
	roomA := ts.showCode
	roomB := ts.newRoom("Room B").Code
	if err := ts.db.SetRoomPassword(ts.showID, "mod-a"); err != nil {
		t.Fatal(err)
	}

	mod := newPersona(ts)
	// Without a session: dashboard denied, API 401, WS controls refused.
	if code, _ := mod.do("GET", "/c/"+roomA, "", "text/html"); code != 401 {
		t.Errorf("anon dashboard: %d, want 401", code)
	}
	if code, _ := mod.do("POST", "/api/shows/"+roomA+"/blank", `{"on":true}`); code != 401 {
		t.Errorf("anon blank: %d, want 401", code)
	}
	if m := mod.wsJoin("controls", roomA); m["t"] != "err" {
		t.Errorf("anon controls join: %v, want err", m)
	}
	// Screens and phones never need a session.
	if m := mod.wsJoin("display", roomA); m["t"] != "joined" {
		t.Errorf("display join: %v", m)
	}
	if code, _ := mod.do("GET", "/d/"+roomA, "", "text/html"); code != 200 {
		t.Errorf("screen page: %d", code)
	}

	// Room A has a password: wrong one refused, right one admits.
	if code, _ := mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+roomA+"/login", `{"pw":"x"}`); code != 401 {
		t.Errorf("wrong room password: %d", code)
	}
	if code, _ := mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+roomA+"/login", `{"pw":"mod-a"}`); code != 200 {
		t.Fatalf("right room password: %d", code)
	}
	if code, _ := mod.do("GET", "/c/"+roomA, "", "text/html"); code != 200 {
		t.Errorf("moderator dashboard: %d", code)
	}
	if m := mod.wsJoin("controls", roomA); m["t"] != "joined" {
		t.Errorf("moderator controls join: %v", m)
	}
	// ...but Room A's moderator cannot touch Room B.
	if code, _ := mod.do("POST", "/api/shows/"+roomB+"/blank", `{"on":true}`); code != 401 {
		t.Errorf("cross-room blank: %d, want 401", code)
	}
	if code, _ := mod.do("GET", "/c/"+roomB, "", "text/html"); code != 401 {
		t.Errorf("cross-room dashboard: %d, want 401", code)
	}
	// Room B has no password: the event code + room pick is enough.
	if code, _ := mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+roomB+"/login", `{"pw":""}`); code != 200 {
		t.Errorf("open room login: %d", code)
	}
	if code, _ := mod.do("POST", "/api/shows/"+roomB+"/blank", `{"on":false}`); code != 200 {
		t.Errorf("room B after sign-in: %d", code)
	}

	// Changing Room A's password signs its moderators out.
	if code, _ := ts.call("PATCH", "/api/events/"+ts.eventCode+"/rooms/"+roomA, []byte(`{"password":"mod-a2"}`), "application/json"); code != 200 {
		t.Fatalf("super sets room password: %d", code)
	}
	if code, _ := mod.do("POST", "/api/shows/"+roomA+"/blank", `{"on":true}`); code != 401 {
		t.Errorf("stale moderator session after password change: %d, want 401", code)
	}
}

func TestSuperOperatorEventControl(t *testing.T) {
	ts := newAPITest(t)
	roomB := ts.newRoom("Room B").Code

	// The SuperOperator moderates every room.
	for _, room := range []string{ts.showCode, roomB} {
		if code, _ := ts.call("POST", "/api/shows/"+room+"/blank", []byte(`{"on":false}`), "application/json"); code != 200 {
			t.Errorf("super blank %s: %d", room, code)
		}
	}
	// Event-wide blackout, then live state shows both rooms dark.
	if code, body := ts.call("POST", "/api/events/"+ts.eventCode+"/verb", []byte(`{"verb":"blank"}`), "application/json"); code != 200 {
		t.Fatalf("event blank: %d %s", code, body)
	}
	code, body := ts.call("GET", "/api/events/"+ts.eventCode+"/live", nil, "")
	if code != 200 || strings.Count(string(body), `"blanked":true`) != 2 {
		t.Fatalf("live after event blank: %d %s", code, body)
	}
	// Same moderator password for every room in one call.
	if code, _ := ts.call("POST", "/api/events/"+ts.eventCode+"/room-password", []byte(`{"password":"all"}`), "application/json"); code != 200 {
		t.Fatalf("room-password all: %d", code)
	}
	_, body = ts.call("GET", "/api/events/"+ts.eventCode, nil, "")
	if strings.Count(string(body), `"hasPassword":true`) != 2 {
		t.Errorf("not every room got the password: %s", body)
	}
	// Rooms: add, rename, reorder, delete.
	_, body = ts.call("POST", "/api/events/"+ts.eventCode+"/rooms", []byte(`{"name":"Room C"}`), "application/json")
	var c struct{ Code string }
	_ = json.Unmarshal(body, &c)
	if code, _ := ts.call("PATCH", "/api/events/"+ts.eventCode+"/rooms/"+c.Code, []byte(`{"name":"Annex","pos":1}`), "application/json"); code != 200 {
		t.Fatalf("patch room: %d", code)
	}
	_, body = ts.call("GET", "/api/events/"+ts.eventCode, nil, "")
	if i := strings.Index(string(body), "Annex"); i < 0 || i > strings.Index(string(body), "API Test Show") {
		t.Errorf("rename/reorder did not land first: %s", body)
	}
	if code, _ := ts.call("DELETE", "/api/events/"+ts.eventCode+"/rooms/"+c.Code, nil, ""); code != 200 {
		t.Errorf("delete room: %d", code)
	}
	// Box settings need the box password (box_test.go covers it fully).
	if code, _ := ts.anon("POST", "/api/theme", []byte(`{"theme":"tron"}`), "application/json"); code != 401 {
		t.Errorf("anon theme change: %d, want 401", code)
	}
}

// Logout drops every session on this browser.
func TestLogoutClearsSessions(t *testing.T) {
	ts := newAPITest(t)
	p := newPersona(ts)
	if code, _ := p.do("POST", "/api/events/"+ts.eventCode+"/login", `{"pw":"`+testSuperPW+`"}`); code != 200 {
		t.Fatal("login")
	}
	if code, _ := p.do("POST", "/api/shows/"+ts.showCode+"/blank", `{"on":false}`); code != 200 {
		t.Fatal("pre-logout access")
	}
	p.do("GET", "/logout", "")
	if code, _ := p.do("POST", "/api/shows/"+ts.showCode+"/blank", `{"on":false}`); code != 401 {
		t.Errorf("after logout: %d, want 401", code)
	}
}
