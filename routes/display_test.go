// package routes tests — display variants (Agent J): the venue boards
// against REAL templates + engine + SQLite over httptest, exercising the
// RegisterDisplay seam directly (an engine WITHOUT registerPages, since
// routes.New still owns /d/:showid pre-consolidation — see
// reviews/NOTES-display.md).
package routes_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"timerpi/importdocs"
	"timerpi/routes"
	"timerpi/timerpi"
	"timerpi/views"
)

// displayTest boots ONLY the display-variant surface (RegisterDisplay) with
// store + engine + real templates. The daysheet smoke test seeds "Audit
// Show" with the canonical importdocs example cues (the same document that
// was imported into the review show).
type displayTest struct {
	t        *testing.T
	srv      *httptest.Server
	db       *timerpi.DB
	showID   int64
	showCode string // share code (Agent L): display boards address it
}

func newDisplayTest(t *testing.T) *displayTest {
	t.Helper()
	dir := t.TempDir()
	db, err := timerpi.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	show, err := db.CreateShow("Audit Show")
	if err != nil {
		t.Fatalf("create show: %v", err)
	}
	engines := timerpi.NewEngines(db)
	tmpl, err := views.New(templatesRoot())
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	routes.RegisterDisplay(r, &routes.Deps{Engines: engines, Store: db, Tmpl: tmpl})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return &displayTest{t: t, srv: srv, db: db, showID: show.ID, showCode: show.Code}
}

// seedAuditShow fills the test show with the 8 example cues via the canonical
// round-trip (ExampleJSON → ParseJSON → ToTimerpiCues → ReplaceCues).
func (ts *displayTest) seedAuditShow() {
	ts.t.Helper()
	cues, err := importdocs.ParseJSON(importdocs.ExampleJSON())
	if err != nil {
		ts.t.Fatalf("parse example json: %v", err)
	}
	tpCues, err := importdocs.ToTimerpiCues(cues)
	if err != nil {
		ts.t.Fatalf("to timerpi cues: %v", err)
	}
	if err := ts.db.ReplaceCues(ts.showID, tpCues); err != nil {
		ts.t.Fatalf("replace cues: %v", err)
	}
}

func (ts *displayTest) get(path string) (int, string) {
	ts.t.Helper()
	res, err := http.Get(ts.srv.URL + path)
	if err != nil {
		ts.t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		ts.t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, string(raw)
}

// want is a mini assertion helper: every substring must appear in body.
func (ts *displayTest) want(code int, body, name string, subs ...string) {
	ts.t.Helper()
	if code != http.StatusOK {
		ts.t.Errorf("%s: %d (body: %.200s)", name, code, body)
		return
	}
	for _, s := range subs {
		if !strings.Contains(body, s) {
			ts.t.Errorf("%s: missing %q in body: %.400s", name, s, body)
		}
	}
}

// ---------------------------------------------------------------------------
// ParseFS: the whole templates dir (incl. display_variants.html + the
// d-*.html fragments) parses standalone; the variant specifics (.View/.Join
// dots) are exercised by the route tests below — fragment exec only happens
// through the variant page dot by design (NOTES-display.md contract note).
func TestDisplayVariantsParseFS(t *testing.T) {
	if _, err := views.New(templatesRoot()); err != nil {
		t.Fatalf("parse: %v", err)
	}
}

// Every view answers with its expected ids + seeded content.
func TestDisplayVariantViews(t *testing.T) {
	ts := newDisplayTest(t)
	ts.seedAuditShow()

	// NEXT-UP board (idle → first cue is next, pre-filled server-side).
	code, body := ts.get("/d/" + ts.showCode + "?view=next")
	ts.want(code, body, "next",
		`id="d-next"`, `data-view="next"`, `id="d-next-label"`,
		`Opening keynote`, `id="d-next-until"`, `id="d-next-start"`,
		`NEXT`, `id="d-label"`, `id="d-speaker"`,
		`id="d-join-qr"`, `id="tp-live-msg"`, `id="tp-offline"`)

	// DAYSHEET (all 8 cues, computed starts as offsets while unanchored).
	code, body = ts.get("/d/" + ts.showCode + "?view=daysheet&print=1")
	ts.want(code, body, "daysheet",
		`id="d-daysheet"`, `id="d-daysheet-rows"`, `Opening keynote`,
		`Coffee break`, `Lunch break`, `Closing remarks &amp; awards`,
		`Leslie Knope`, `Ron Swanson`, "Audit Show", `Day not anchored`,
		`data-print="true"`)

	// CLOCK (lobby board — its own id, NOT the stage countdown #d-clock).
	code, body = ts.get("/d/" + ts.showCode + "?view=clock")
	ts.want(code, body, "clock",
		`id="d-vclock"`, `id="d-vclock-time"`, "Audit Show", `data-view="clock"`)

	// Stage passthrough: bare URL is the existing fullscreen timer (Fix-1's
	// template; its #d-stage id must still be there).
	code, body = ts.get("/d/" + ts.showCode)
	ts.want(code, body, "stage", `id="d-stage"`, `data-state="idle"`)

	// ?view=stage redirects to the canonical bare URL (documented
	// passthrough). Probe raw: a normal client would follow the redirect.
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	res, err := hc.Get(ts.srv.URL + "/d/" + ts.showCode + "?view=stage")
	if err != nil {
		t.Fatalf("view=stage: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Errorf("view=stage: want 302, got %d", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); loc != "/d/"+ts.showCode {
		t.Errorf("view=stage Location = %q", loc)
	}

	// Unknown view / show id → 404 (Agent L code-only contract: digits are
	// the legacy numeric world and resolve to nothing, same as any miss).
	for _, path := range []string{
		"/d/" + ts.showCode + "?view=bogus",
		"/d/424242?view=next",
		"/d/notanumber",
	} {
		code, _ = ts.get(path)
		if code != http.StatusNotFound {
			t.Errorf("%s: want 404 got %d", path, code)
		}
	}
}

// Daysheet smoke against the seeded Audit Show: all 8 rows, computed times,
// break rows marked, running-row classes present server-side.
func TestDisplayVariantsDaysheetAuditShow(t *testing.T) {
	ts := newDisplayTest(t)
	ts.seedAuditShow()

	code, body := ts.get("/d/" + ts.showCode + "?view=daysheet")
	ts.want(code, body, "daysheet", `id="d-daysheet-rows"`)

	// Every seeded cue has a server-rendered row.
	for _, label := range []string{"Opening keynote", "Tech outlook talk", "Coffee break", "Changeover", "VT: highlights reel", "Panel discussion", "Lunch break", "Closing remarks &amp; awards"} {
		if !strings.Contains(body, label) {
			t.Errorf("daysheet missing row %q", label)
		}
	}
	if n := strings.Count(body, `data-pos="`); n < 8 {
		t.Errorf("daysheet rows: %d data-pos (want 8)", n)
	}
	// Unanchored day → OFFSET times, not midnight wall clocks (documents the
	// degrade path the client continues after live adoption). html/template
	// escapes the leading '+' of the offset format.
	if !strings.Contains(body, "&#43;0:00") {
		t.Errorf("daysheet: first row should carry the +0:00 offset start")
	}
	if !strings.Contains(body, "45:00") || !strings.Contains(body, "30:00") {
		t.Errorf("daysheet: run durations missing")
	}
}

// URL override validation: hex accepted (+normalized), everything else 400.
func TestDisplayVariantsHexOverride(t *testing.T) {
	ts := newDisplayTest(t)

	// #hex (URL-encoded) + bare hex normalize to the CSS var.
	for _, q := range []string{"accent=%237C3AED", "accent=7C3AED", "accent=%237c3aed"} {
		code, body := ts.get("/d/" + ts.showCode + "?view=clock&" + q)
		ts.want(code, body, q, `--tp-accent:#7c3aed`)
	}
	// 3-digit expands.
	code, body := ts.get("/d/" + ts.showCode + "?view=clock&accent=7AB")
	ts.want(code, body, "accent=7AB", `--tp-accent:#77aabb`)

	// bg lands on its own var.
	code, body = ts.get("/d/" + ts.showCode + "?view=clock&bg=%23000000")
	ts.want(code, body, "bg", `--tp-bg:#000000`)

	// fit + print flip body attrs, unknown fit degrades to contain.
	code, body = ts.get("/d/" + ts.showCode + "?view=clock&fit=cover")
	ts.want(code, body, "fit=cover", `data-fit="cover"`)
	code, body = ts.get("/d/" + ts.showCode + "?view=clock&fit=nonsense")
	ts.want(code, body, "fit=nonsense", `data-fit="contain"`)

	// Bad values → 400: not hex, wrong length, css escape attempts, banned
	// brand tokens.
	for _, q := range []string{
		"accent=javascript:alert", "accent=%23zz11", "accent=zz12",
		"accent=12", "accent=12345678", "bg=red", "bg=%2312",
		"accent=22C55E", // green — logo-only (brand lock)
		"bg=F97316",     // orange — banned (brand lock)
	} {
		code, _ = ts.get("/d/" + ts.showCode + "?view=clock&" + q)
		if code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", q, code)
		}
	}
}

// Registration guard: RegisterDisplay with nil deps is a no-op; a second
// call on an engine that already has /d/:ident must NEVER panic (gin would
// panic on a duplicate mount — server boot must not die on wiring order).
// Agent L: with code-only addressing the mounted handler's own 404 copy
// ("Unknown session code") is what proves the request reached the SKIPPED
// mount's still-registered first handler (gin's NoRoute 404 body differs).
func TestDisplayVariantsRegistrationGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	routes.RegisterDisplay(r, nil) // nil wiring: silent no-op

	// First real mount answers (code-only resolve refuses BEFORE the
	// engines check; the earlier 501 degrade shape is gone per the contract).
	routes.RegisterDisplay(r, &routes.Deps{})

	// Re-registration on an already-mounted engine: skip + log, no panic.
	routes.RegisterDisplay(r, &routes.Deps{})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/d/1?view=clock") // digits: legacy world → refused
	if err != nil {
		t.Fatalf("GET /d/1: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusNotFound || !strings.Contains(string(raw), "Unknown session code") {
		t.Fatalf("mounted handler after skip: want friendly 404, got %d (%.100s)", res.StatusCode, string(raw))
	}
}
