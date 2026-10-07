// package routes tests: the JSON API + pages against REAL SQLite, engine,
// templates (parsed from the checkout's templates/ dir) and REAL
// importdocs — the same stack main.go boots, over httptest.
package routes_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"timerpi/routes"
	"timerpi/timerpi"
	"timerpi/views"
	"timerpi/ws"
)

// apiTest boots the full wired stack (no ws clients attached).
type apiTest struct {
	t         *testing.T
	srv       *httptest.Server
	db        *timerpi.DB
	engines   *timerpi.Engines // the registry routes.Deps carries (CuTePi hook tests wire OnStart on it)
	deps      *routes.Deps     // the server's deps (release, venue tests call its methods)
	showID    int64            // internal; JSON bookkeeping only (Agent L contract)
	showCode  string           // share code: the ONLY public address
	eventCode string           // parent event (the client is its SuperOperator)
}

func newAPITest(t *testing.T) *apiTest {
	t.Helper()
	dir := t.TempDir()
	db, err := timerpi.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// A fresh browser per test: every test server listens on 127.0.0.1,
	// and cookie jars ignore ports, so one shared jar collected every
	// earlier test's sessions.
	freshDefaultClient()

	ev, rooms, err := db.CreateEvent("API Test Event", testSuperPW, []string{"API Test Show"})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	show := rooms[0]

	engines := timerpi.NewEngines(db)
	tmpl, err := views.New(templatesRoot())
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	hub := ws.NewHub(engines, tmpl.Fragment)
	hub.SetStore(db)
	hub.SetMessagesFunc(db.ListMessages)
	hub.SetLogger(func(string, ...any) {})

	gin.SetMode(gin.TestMode)
	deps := &routes.Deps{Engines: engines, Store: db, Hub: hub, Tmpl: tmpl}
	r := routes.New(deps)
	srv := httptest.NewServer(r)
	t.Cleanup(func() {
		srv.Close()
		hub.Stop()
	})
	ts := &apiTest{t: t, srv: srv, db: db, engines: engines, deps: deps, showID: show.ID, showCode: show.Code, eventCode: ev.Code}
	ts.signInSuper()
	ts.signInBox()
	return ts
}

// testBoxPW is every test box's password (box settings, routes/box.go).
const testBoxPW = "box-password"

// signInBox sets the test box's password, which signs the shared client in
// to box settings.
func (ts *apiTest) signInBox() {
	ts.t.Helper()
	code, body := ts.call("POST", "/api/box/setup", []byte(`{"password":"`+testBoxPW+`"}`), "application/json")
	if code != 200 {
		ts.t.Fatalf("box setup: %d %s", code, body)
	}
}

// testSuperPW is every test event's supervisor password.
const testSuperPW = "testpw"

// signInSuper signs the shared test client in as the event's SuperOperator
// (moderator rights in every room of the event).
func (ts *apiTest) signInSuper() {
	ts.t.Helper()
	code, body := ts.call("POST", "/api/events/"+ts.eventCode+"/login", []byte(`{"pw":"`+testSuperPW+`"}`), "application/json")
	if code != 200 {
		ts.t.Fatalf("super sign-in: %d %s", code, body)
	}
}

// newRoom adds another room to the test event (the client is already
// SuperOperator there).
func (ts *apiTest) newRoom(name string) timerpi.Show {
	ts.t.Helper()
	ev, ok := ts.db.ResolveEvent(ts.eventCode)
	if !ok {
		ts.t.Fatal("test event vanished")
	}
	sh, err := ts.db.CreateRoom(ev.ID, name)
	if err != nil {
		ts.t.Fatalf("create room: %v", err)
	}
	return sh
}

// engine exposes the built engine for route-presence assertions.
func (ts *apiTest) engineRoutes(method string) []string {
	return nil
}

// templatesRoot resolves the checkout root from the package dir.
func templatesRoot() string {
	for _, root := range []string{"../templates", "templates"} {
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			return root
		}
	}
	return "templates"
}

// callType is call() with an explicit content type.
func (ts *apiTest) callType(method, path string, body []byte, ctype string) (int, []byte) {
	ts.t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, ts.srv.URL+path, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		ts.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		ts.t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, raw
}

// anon is call() from a fresh browser with no sessions (a stranger, a TV,
// or an audience phone).
func (ts *apiTest) anon(method, path string, body []byte, ctype string) (int, []byte) {
	ts.t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, _ := http.NewRequest(method, ts.srv.URL+path, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		ts.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw
}

func (ts *apiTest) call(method, path string, body []byte, ctype string) (int, []byte) {
	return ts.callType(method, path, body, ctype)
}

// ---------------------------------------------------------------------------
// Shows CRUD + snapshot.

func TestShowLifecycle(t *testing.T) {
	ts := newAPITest(t)

	// create a room in the event (SuperOperator)
	code, body := ts.call("POST", "/api/events/"+ts.eventCode+"/rooms", []byte(`{"name":"Friday Night"}`), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &created)
	if !timerpi.ValidCode(created.Code) {
		t.Errorf("create code = %q (want an 8-char Crockford code)", created.Code)
	}
	createdID, ok := timerpi.ResolveShowID(ts.db, created.Code)
	if !ok {
		t.Fatalf("created room %q does not resolve", created.Code)
	}

	// There is no public list of rooms or shows.
	if code, _ = ts.anon("GET", "/api/shows", nil, ""); code != http.StatusNotFound {
		t.Errorf("GET /api/shows: %d, want 404 (no public listing)", code)
	}

	// snapshot persists the show + cues empty, addressed BY CODE
	code, body = ts.call("GET", "/api/shows/"+created.Code, nil, "")
	if code != 200 {
		t.Fatalf("snapshot: %d %s", code, body)
	}
	var snap map[string]any
	_ = json.Unmarshal(body, &snap)
	if snap["show"].(map[string]any)["title"] != "Friday Night" {
		t.Errorf("snapshot.show = %v", snap["show"])
	}
	if snap["show"].(map[string]any)["code"] != created.Code {
		t.Errorf("snapshot.show.code = %v", snap["show"])
	}

	// Lowercase/dashed spelling resolves identically (normalize on the way
	// in; created.Code is already normalized, so apply the maps once here).
	if code, _ = ts.call("GET", "/api/shows/"+created.Code[:4]+"-"+created.Code[4:], nil, ""); code != 200 {
		t.Errorf("4-4 spelling: %d", code)
	}

	// Numeric ids are NO LONGER addresses (Agent L scope change) → 404:
	// the internal id of the freshly created show refuses just like a miss.
	if code, _ = ts.call("GET", "/api/shows/"+jsonNumber(createdID), nil, ""); code != 404 {
		t.Errorf("numeric id snapshot: %d (want 404 in the code-only world)", code)
	}
	// unknown show → 404
	if code, _ = ts.call("GET", "/api/shows/ZZZZZZZZ", nil, ""); code != 404 {
		t.Errorf("unknown show: %d", code)
	}
}

// jsonNumber is an int64→decimal-string (JSON marshal of a number).
func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// ---------------------------------------------------------------------------
// Pages render through the REAL templates (template exec errors surface).

func TestPagesRender(t *testing.T) {
	ts := newAPITest(t)

	// Seed a cue so the dashboard has rows.
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Welcome", DurationMS: 120_000, Speaker: "Leslie"}); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/", "/c/" + ts.showCode, "/d/" + ts.showCode} {
		code, body := ts.call("GET", path, nil, "")
		if code != 200 {
			t.Errorf("%s → %d (body: %.200s)", path, code, body)
			continue
		}
		// The shell pages carry the brand; the bare fragment need not.
		if !bytes.Contains(body, []byte("TimerPi")) {
			t.Errorf("%s: no TimerPi shell in body", path)
		}
	}
	// The dashboard renders the cue label + speaker through the template.
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	if !bytes.Contains(body, []byte("Welcome")) || !bytes.Contains(body, []byte("Leslie")) {
		t.Error("dashboard missing cue row content")
	}
	// frag-shows / homepage list: REMOVED (privacy) — codes are never
	// listed publicly and /api/shows strips code+title on open appliances.
	_, body = ts.call("GET", "/api/shows", nil, "")
	if bytes.Contains(body, []byte(ts.showCode)) || bytes.Contains(body, []byte("API Test Show")) {
		t.Errorf("open /api/shows leaked credentials: %.300s", body)
	}
}

// ---------------------------------------------------------------------------
// Cue replace + import.

func TestReplaceCuesAndImport(t *testing.T) {
	ts := newAPITest(t)

	// PUT full replacement (PROTOCOL wire shape).
	cues := `[{"label":"Opening","durationMS":300000,"tags":"VT"},{"label":"Talk","durationMS":600000,"speaker":"Leslie"}]`
	code, body := ts.call("PUT", "/api/shows/"+ts.showCode+"/cues", []byte(cues), "application/json")
	if code != 200 {
		t.Fatalf("PUT cues: %d %s", code, body)
	}
	snap := ts.snapshot()
	if len(snap["cues"].([]any)) != 2 {
		t.Fatalf("PUT cues: %d cues in snapshot", len(snap["cues"].([]any)))
	}

	// CSV import via multipart (real importdocs; mode replace default).
	csv := "label,duration\nOpening2,1:00\nTalk2,05:00\n"
	imp := ts.multipart(csv, "cue-list.csv")
	code, body = ts.callType("POST", "/api/shows/"+ts.showCode+"/import", imp.body, imp.ctype)
	if code != 200 || !bytes.Contains(body, []byte("alert-success")) {
		t.Fatalf("import: %d %s", code, body)
	}
	snap = ts.snapshot()
	cuesArr := snap["cues"].([]any)
	if len(cuesArr) != 2 || cuesArr[0].(map[string]any)["label"] != "Opening2" {
		t.Errorf("replace import: cues = %v", cuesArr)
	}

	// Append mode keeps existing rows.
	imp = ts.multipart(csv, "cue-list.csv")
	code, body = ts.callType("POST", "/api/shows/"+ts.showCode+"/import?mode=append", imp.body, imp.ctype)
	if code != 200 {
		t.Fatalf("append import: %d %s", code, body)
	}
	snap = ts.snapshot()
	if len(snap["cues"].([]any)) != 4 {
		t.Errorf("append: %d cues, want 4", len(snap["cues"].([]any)))
	}

	// A bad row reports VERBATIM (row numbers quoted by importdocs) and
	// replaces nothing under replace mode (salvage refused: operator never
	// sees silently-dropped rows).
	bad := "label,duration\nGood,1:00\nBad1,banana\nBad2,5:00\n"
	imp = ts.multipart(bad, "cue-list.csv")
	code, body = ts.callType("POST", "/api/shows/"+ts.showCode+"/import", imp.body, imp.ctype)
	if code != 200 || !bytes.Contains(body, []byte("row 3")) || !bytes.Contains(body, []byte("banana")) {
		t.Errorf("bad-row import: %d %s (want verbatim row error)", code, body)
	}
	snap = ts.snapshot()
	if len(snap["cues"].([]any)) != 4 {
		t.Errorf("refused bad-import must not touch cues: %d", len(snap["cues"].([]any)))
	}
}

// typeMult holds the body bytes of a multipart upload + its content type.
type typeMult struct {
	body  []byte
	ctype string
}

// multipart builds an import-ready multipart body (kind field present but
// empty → auto → ParseFilename via the file extension).
func (ts *apiTest) multipart(content, name string) typeMult {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("kind", "") // auto → ParseFilename via extension
	fw, _ := w.CreateFormFile("file", name)
	fw.Write([]byte(content))
	w.Close()
	return typeMult{body: buf.Bytes(), ctype: "multipart/form-data; boundary=" + w.Boundary()}
}

// snapshot fetches GET /api/shows/:id as a map.
func (ts *apiTest) snapshot() map[string]any {
	ts.t.Helper()
	code, body := ts.call("GET", "/api/shows/"+ts.showCode, nil, "")
	if code != 200 {
		ts.t.Fatalf("snapshot: %d %s", code, body)
	}
	var snap map[string]any
	if err := json.Unmarshal(body, &snap); err != nil {
		ts.t.Fatalf("snapshot json: %v (%s)", err, body)
	}
	return snap
}

func TestInspectorTemplateShips(t *testing.T) {
	ts := newAPITest(t)
	// Row content: seed one cue so the shipped page carries the row pencil.
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Inspector Cue", DurationMS: 300_000}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// B1: the dashboard must carry the inspector dialog + the row pencil
	// affordance (the JS contract: [data-insp] opens, #tp-inspector edits).
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	for _, sub := range []string{`id="tp-inspector"`, `data-insp="`, `tp-insp-save`, `tp-insp-alert1`, `Inspector Cue`} {
		if !bytes.Contains(body, []byte(sub)) {
			t.Errorf("inspector markup missing %q", sub)
		}
	}
}

func TestDuplicateTemplateShips(t *testing.T) {
	ts := newAPITest(t)
	// A4 + STATUS U41: Duplicate lives in the right-click / long-press row
	// menu (ftl .context-menu), not a row icon.
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Dup Me", DurationMS: 120_000}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	for _, sub := range []string{
		`id="tp-row-menu"`, `class="context-menu is-at-pointer"`, `data-row-act="dup"`, `data-pos="1"`,
	} {
		if !bytes.Contains(body, []byte(sub)) {
			t.Errorf("duplicate affordance missing %q", sub)
		}
	}
	if bytes.Contains(body, []byte(`data-cmd="cueDup"`)) || bytes.Contains(body, []byte(`data-dir="up"`)) {
		t.Error("rows still carry the duplicate icon or the up/down arrows (U40, U41)")
	}
}
