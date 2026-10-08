package routes_test

// Fix round B (E2E REPORT 2026-10-08): waiting-room scoping, the cloud box
// claim, event theme push, presets, event file, themes, sign-in limits,
// caps and the smaller security fixes.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"timerpi/boards"
	"timerpi/routes"
	"timerpi/timerpi"
)

// do sends a request with the shared (Event Technician) client and extra
// headers (X-Forwarded-For picks the client IP: tests run from loopback,
// the one trusted proxy).
func (ts *apiTest) do(method, path, body string, hdr map[string]string) (int, string) {
	ts.t.Helper()
	req, _ := http.NewRequest(method, ts.srv.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		ts.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

func ip(a string) map[string]string { return map[string]string{"X-Forwarded-For": a} }

// REPORT #4: on the cloud an Event Technician sees and captures only the
// waiting screens behind their own address; on a box every screen on the
// LAN. Room moderators never see the waiting room.
func TestWaitingScopedByRoleAndIP(t *testing.T) {
	ts := newAPITest(t)
	ts.do("POST", "/api/waiting/register", `{"name":"Venue TV","host":"a"}`, ip("10.0.0.1"))
	ts.do("POST", "/api/waiting/register", `{"name":"Other TV","host":"b"}`, ip("10.0.0.2"))
	list := func(from string) map[string]int64 {
		t.Helper()
		code, body := ts.do("GET", "/api/waiting", "", ip(from))
		if code != 200 {
			t.Fatalf("list: %d %s", code, body)
		}
		var j struct {
			Waiting []struct {
				ID   int64
				Name string
			}
		}
		_ = json.Unmarshal([]byte(body), &j)
		out := map[string]int64{}
		for _, w := range j.Waiting {
			out[w.Name] = w.ID
		}
		return out
	}
	ts.deps.Role = "box"
	all := list("10.0.0.1")
	if len(all) != 2 {
		t.Fatalf("box: want both screens, got %v", all)
	}
	ts.deps.Role = "cloud"
	if got := list("10.0.0.1"); len(got) != 1 || got["Venue TV"] == 0 {
		t.Fatalf("cloud: want only the same-address screen, got %v", got)
	}
	other := fmt.Sprint(all["Other TV"])
	cap := fmt.Sprintf(`{"code":%q,"kind":"audience"}`, ts.showCode)
	if code, _ := ts.do("POST", "/api/waiting/"+other+"/capture", cap, ip("10.0.0.1")); code != 404 {
		t.Errorf("cloud capture of another network's screen: %d, want 404", code)
	}
	if code, _ := ts.do("DELETE", "/api/waiting/"+other, "", ip("10.0.0.1")); code != 404 {
		t.Errorf("cloud dismiss of another network's screen: %d, want 404", code)
	}
	if code, b := ts.do("POST", "/api/waiting/"+fmt.Sprint(all["Venue TV"])+"/capture", cap, ip("10.0.0.1")); code != 200 {
		t.Errorf("cloud capture of own screen: %d %s", code, b)
	}
	// A room moderator: no waiting room at all.
	mod := newPersona(ts)
	if code, _ := mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode+"/login", `{"pw":""}`); code != 200 {
		t.Fatal("moderator login")
	}
	if code, _ := mod.do("GET", "/api/waiting", ""); code != http.StatusUnauthorized {
		t.Errorf("moderator list: %d, want 401", code)
	}
	if code, _ := mod.do("DELETE", "/api/waiting/"+other, ""); code != http.StatusUnauthorized {
		t.Errorf("moderator dismiss: %d, want 401", code)
	}
}

// REPORT #8: a cloud's box password is claimed only with the setup code
// from the server log; a box keeps the plain first-run setup.
func TestCloudBoxSetupNeedsCode(t *testing.T) {
	db, err := timerpi.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deps := &routes.Deps{Store: db, Role: "cloud"}
	srv := httptest.NewServer(routes.New(deps))
	defer srv.Close()
	post := func(body string) int {
		res, err := http.Post(srv.URL+"/api/box/setup", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	code := deps.BoxSetupCode()
	if len(code) < 12 {
		t.Fatalf("cloud setup code %q", code)
	}
	if st := post(`{"password":"box-password"}`); st != http.StatusForbidden {
		t.Errorf("anonymous cloud claim: %d, want 403", st)
	}
	if st := post(`{"password":"box-password","setupCode":"0000"}`); st != http.StatusForbidden {
		t.Errorf("wrong code: %d, want 403", st)
	}
	if st := post(`{"password":"box-password","setupCode":"` + code + `"}`); st != 200 {
		t.Errorf("claim with the code: %d", st)
	}
	if deps.BoxSetupCode() != "" {
		t.Error("setup code still offered after the password was set")
	}
	// A box: no code needed (newAPITest's box setup proves it), none made.
	if (&routes.Deps{Store: db, Role: "box"}).BoxSetupCode() != "" {
		t.Error("a box made a setup code")
	}
}

// joinDisplay opens a named display socket and drains to "joined".
func joinDisplay(t *testing.T, ts *apiTest, screen string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	// A captured (keyed) screen: anonymous made-up names are not registry
	// screens (ws register).
	key, kerr := ts.db.ScreenKey(ts.showID, screen)
	if kerr != nil {
		t.Fatal(kerr)
	}
	_ = conn.WriteJSON(map[string]any{"v": 1, "t": "join", "role": "display", "show": ts.showCode,
		"peerId": "tv-" + screen, "joinedAt": 1000, "screen": screen, "key": key})
	for i := 0; i < 10; i++ {
		var m map[string]any
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := conn.ReadJSON(&m); err != nil {
			t.Fatalf("join: %v", err)
		}
		if m["t"] == "joined" {
			return conn
		}
	}
	t.Fatal("never joined")
	return nil
}

// REPORT #10: changing the event's default theme reaches the screens that
// use it, live.
func TestEventThemePushesToDefaultScreens(t *testing.T) {
	ts := newAPITest(t)
	if err := ts.db.UpsertScreen(ts.showID, "Stage DSM"); err != nil { // a known screen on the default theme
		t.Fatal(err)
	}
	conn := joinDisplay(t, ts, "Stage DSM")
	defer conn.Close()
	if code, b := ts.do("PATCH", "/api/events/"+ts.eventCode, `{"theme":"blue-future"}`, nil); code != 200 {
		t.Fatalf("patch: %d %s", code, b)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var m map[string]any
		conn.SetReadDeadline(deadline)
		if err := conn.ReadJSON(&m); err != nil {
			break
		}
		if m["t"] == "display" && m["theme"] == "blue-future" {
			return
		}
	}
	t.Fatal("the default-theme screen never got the new event theme")
}

// REPORT #15: a preset restores the built-in layout, display type and
// Mounted, not just the theme.
func TestPresetCarriesTemplateKindRotation(t *testing.T) {
	ts := newAPITest(t)
	tpl := boards.Templates()[0].Key
	if err := ts.db.SetScreenTemplate(ts.showID, "Stage", tpl); err != nil {
		t.Fatal(err)
	}
	if err := ts.db.SetScreenLook(ts.showID, "Stage", timerpi.ScreenPresenter, 90); err != nil {
		t.Fatal(err)
	}
	code, body := ts.do("POST", "/api/shows/"+ts.showCode+"/presets", `{"name":"Stage look"}`, nil)
	if code != 201 {
		t.Fatalf("save: %d %s", code, body)
	}
	var p struct{ ID int64 }
	_ = json.Unmarshal([]byte(body), &p)
	_ = ts.db.SetScreenTemplate(ts.showID, "Stage", "")
	_ = ts.db.SetScreenLook(ts.showID, "Stage", timerpi.ScreenAudience, 180)
	if code, b := ts.do("POST", fmt.Sprintf("/api/shows/%s/presets/%d/apply", ts.showCode, p.ID), "", nil); code != 200 {
		t.Fatalf("apply: %d %s", code, b)
	}
	s, err := ts.db.GetScreenByName(ts.showID, "Stage")
	if err != nil || s.Template != tpl || s.Kind != timerpi.ScreenPresenter || s.Rotation != 90 {
		t.Errorf("after apply: %+v (%v), want template %s presenter 90", s, err, tpl)
	}
}

// REPORT #16 (PRODUCT E4): the event downloads as one file without
// secrets and imports as a NEW event with the importer's password.
func TestEventFileRoundTrip(t *testing.T) {
	ts := newAPITest(t)
	if err := ts.db.AppendCues(ts.showID, []timerpi.Cue{{Label: "Keynote", DurationMS: 2700000, Speaker: "Ada"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.db.CreateMessage(ts.showID, "Wrap up", ""); err != nil {
		t.Fatal(err)
	}
	if err := ts.db.SetScreenConfig(ts.showID, "Lobby", "tron", 0, "Foyer"); err != nil {
		t.Fatal(err)
	}
	_, _ = ts.db.ScreenKey(ts.showID, "Lobby")
	ts.do("POST", "/api/events/"+ts.eventCode+"/room-password", `{"password":"room-secret"}`, nil)
	if code, _ := ts.anon("GET", "/api/events/"+ts.eventCode+"/export", nil, ""); code != 401 {
		t.Errorf("anonymous export: %d, want 401", code)
	}
	code, file := ts.do("GET", "/api/events/"+ts.eventCode+"/export", "", nil)
	if code != 200 {
		t.Fatalf("export: %d %s", code, file)
	}
	ev, _ := ts.db.ResolveEvent(ts.eventCode)
	room, _ := ts.db.GetShow(ts.showID)
	key, _ := ts.db.ScreenKey(ts.showID, "Lobby")
	for _, secret := range []string{ev.SuperHash, room.RoomPW, key, `"code":"` + ts.eventCode} {
		if secret != "" && strings.Contains(file, secret) {
			t.Errorf("export leaks %.20s…", secret)
		}
	}
	if !strings.Contains(file, "Keynote") || !strings.Contains(file, "Lobby") {
		t.Fatalf("export lacks content: %.300s", file)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "event.json")
	fw.Write([]byte(file))
	w.WriteField("password", "new-pass")
	w.Close()
	p := newPersona(ts)
	req, _ := http.NewRequest("POST", ts.srv.URL+"/api/events/import", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := p.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 201 {
		t.Fatalf("import: %d %s", res.StatusCode, raw)
	}
	var out struct{ Code string }
	_ = json.Unmarshal(raw, &out)
	if out.Code == "" || out.Code == ts.eventCode {
		t.Fatalf("import must make a new event: %s", raw)
	}
	nev, ok := ts.db.ResolveEvent(out.Code)
	if !ok || !timerpi.CheckPassword(nev.SuperHash, "new-pass") {
		t.Fatal("imported event lacks the importer's password")
	}
	rooms, _ := ts.db.ListRooms(nev.ID)
	if len(rooms) != 1 || rooms[0].Code == ts.showCode || rooms[0].RoomPW != "" {
		t.Fatalf("imported rooms: %+v", rooms)
	}
	cues, _ := ts.db.ListCues(rooms[0].ID)
	msgs, _ := ts.db.ListMessages(rooms[0].ID)
	scr, serr := ts.db.GetScreenByName(rooms[0].ID, "Lobby")
	if len(cues) != 1 || cues[0].Label != "Keynote" || len(msgs) != 1 || serr != nil || scr.Theme != "tron" {
		t.Errorf("round trip lost content: cues=%v msgs=%v screen=%+v", cues, msgs, scr)
	}
	// The importer is the new event's Event Technician.
	if code, _ := p.do("GET", "/api/events/"+out.Code+"/export", ""); code != 200 {
		t.Errorf("importer not signed in to the new event: %d", code)
	}
	if code, _ := ts.anon("POST", "/api/events/import", []byte(`{"file":{"nope":1},"password":"new-pass"}`), "application/json"); code != 400 {
		t.Errorf("garbage import: %d, want 400", code)
	}
}

// REPORT #18: tokens (and core) are not themes.
func TestTokensIsNotATheme(t *testing.T) {
	ts := newAPITest(t)
	if code, _ := ts.do("POST", "/api/shows/"+ts.showCode+"/screens/config", `{"name":"TV","theme":"tokens"}`, nil); code != 400 {
		t.Errorf("screen theme tokens: %d, want 400", code)
	}
	if code, _ := ts.do("PATCH", "/api/events/"+ts.eventCode, `{"theme":"tokens"}`, nil); code != 400 {
		t.Errorf("event theme tokens: %d, want 400", code)
	}
	_, body := ts.do("GET", "/api/theme", "", nil)
	if strings.Contains(body, `"tokens"`) || strings.Contains(body, `"core"`) {
		t.Errorf("theme list offers a non-theme: %s", body)
	}
}

// A flood of wrong passwords from many addresses doesn't lock the real
// technician (a clean address) out; the flooding addresses are refused.
func TestLoginFloodSparesCleanAddress(t *testing.T) {
	ts := newAPITest(t)
	p := newPersona(ts)
	login := func(addr, pw string) int {
		req, _ := http.NewRequest("POST", ts.srv.URL+"/api/events/"+ts.eventCode+"/login", strings.NewReader(`{"pw":"`+pw+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", addr)
		res, err := p.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for i := 0; i < 400; i++ {
		login(fmt.Sprintf("10.9.%d.%d", i/8, i%8+1), "guess")
	}
	login("10.9.0.1", "guess") // its second failure: under attack that is all it gets
	if st := login("10.9.0.1", "guess"); st != http.StatusTooManyRequests {
		t.Errorf("flooding address: %d, want 429", st)
	}
	if st := login("192.0.2.7", testSuperPW); st != 200 {
		t.Errorf("real technician from a clean address: %d, want 200", st)
	}
}

// A phone sign-in code stays spent across a restart (nonces in the DB).
func TestPhoneCodeSpentAcrossRestart(t *testing.T) {
	ts := newAPITest(t)
	_, body := ts.do("POST", "/api/events/"+ts.eventCode+"/phone-link", `{"base":"`+ts.srv.URL+`"}`, nil)
	var j struct{ URL string }
	_ = json.Unmarshal([]byte(body), &j)
	path := strings.TrimPrefix(j.URL, ts.srv.URL)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(base string) int {
		res, err := noFollow.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if st := get(ts.srv.URL); st != http.StatusSeeOther {
		t.Fatalf("first redeem: %d", st)
	}
	restarted := httptest.NewServer(routes.New(&routes.Deps{Store: ts.db, Tmpl: ts.deps.Tmpl, Engines: ts.engines}))
	defer restarted.Close()
	if st := get(restarted.URL); st == http.StatusSeeOther {
		t.Error("used phone code worked again after a restart")
	}
}

// Small fixes: 404s for missing screens/messages, length caps, unique
// names on room re-import, friendlier CSV import.
func TestRoundBSmallFixes(t *testing.T) {
	ts := newAPITest(t)
	if code, _ := ts.do("POST", "/api/shows/"+ts.showCode+"/screens/rename", `{"from":"Nope","to":"Other"}`, nil); code != 404 {
		t.Errorf("rename missing screen: %d, want 404", code)
	}
	if code, _ := ts.do("DELETE", "/api/shows/"+ts.showCode+"/messages/99999", "", nil); code != 404 {
		t.Errorf("delete missing message: %d, want 404", code)
	}
	long := strings.Repeat("x", 5000)
	ts.do("POST", "/api/shows/"+ts.showCode+"/messages", `{"text":"`+long+`"}`, nil)
	msgs, _ := ts.db.ListMessages(ts.showID)
	if len(msgs) != 1 || len([]rune(msgs[0].Text)) != timerpi.MaxMessageLen {
		t.Errorf("stage message not capped: %d", len([]rune(msgs[0].Text)))
	}
	if err := ts.db.AppendCues(ts.showID, []timerpi.Cue{{Label: long, Speaker: long, Notes: long + long}}); err != nil {
		t.Fatal(err)
	}
	cues, _ := ts.db.ListCues(ts.showID)
	c := cues[len(cues)-1]
	if len(c.Label) != timerpi.MaxCueLabelLen || len(c.Speaker) != timerpi.MaxCueSpeakerLen || len(c.Notes) != timerpi.MaxCueNotesLen {
		t.Errorf("cue caps: label %d speaker %d notes %d", len(c.Label), len(c.Speaker), len(c.Notes))
	}

	// Re-importing a room the event already has gets a unique name.
	_, file := ts.do("GET", "/api/shows/"+ts.showCode+"/file", "", nil)
	for _, want := range []string{"API Test Show (2)", "API Test Show (3)"} {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		fw, _ := w.CreateFormFile("file", "room.json")
		fw.Write([]byte(file))
		w.Close()
		code, body := ts.callType("POST", "/api/events/"+ts.eventCode+"/rooms/import", buf.Bytes(), w.FormDataContentType())
		if code != 201 || !strings.Contains(string(body), want) {
			t.Errorf("room re-import: %d %s, want %q", code, body, want)
		}
	}

	// CSV: Location kept; an empty title is a friendly per-row error.
	imp := ts.multipart("Label,Duration,Location\nLunch,0:45,Great Hall\n", "day.csv")
	if code, body := ts.callType("POST", "/api/shows/"+ts.showCode+"/import", imp.body, imp.ctype); code != 200 || !strings.Contains(string(body), "alert-success") {
		t.Fatalf("csv import: %d %s", code, body)
	}
	cues, _ = ts.db.ListCues(ts.showID)
	if len(cues) != 1 || cues[0].Location != "Great Hall" || cues[0].DurationMS != 45*60000 {
		t.Errorf("csv cue: %+v", cues)
	}
	imp = ts.multipart("Label,Duration\nOpening,0:10\n,0:20\n", "day.csv")
	_, body := ts.callType("POST", "/api/shows/"+ts.showCode+"/import", imp.body, imp.ctype)
	if !strings.Contains(string(body), "Row 3") || !strings.Contains(string(body), "title") || strings.Contains(string(body), "importdocs") {
		t.Errorf("empty title: %s", body)
	}
	imp = ts.multipart("\x89PNG\r\n\x1a\n0000000000000000", "photo.csv")
	if _, body := ts.callType("POST", "/api/shows/"+ts.showCode+"/import", imp.body, imp.ctype); !strings.Contains(string(body), "not a running order") {
		t.Errorf("png as running order: %s", body)
	}
}

// Uploaded images get random, unguessable ids.
func TestAssetIDsNotSequential(t *testing.T) {
	ts := newAPITest(t)
	a, err := ts.db.CreateAsset(1, "a.png", "image/png", []byte("x"))
	b, err2 := ts.db.CreateAsset(1, "b.png", "image/png", []byte("y"))
	if err != nil || err2 != nil || a.ID < 1<<40 || a.ID >= 1<<53 || b.ID == a.ID+1 {
		t.Errorf("asset ids %d, %d (%v %v)", a.ID, b.ID, err, err2)
	}
}
