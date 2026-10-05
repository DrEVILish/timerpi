package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"timerpi/config"
)

// recordResponse runs req through the engine and returns the recorder.
func recordResponse(r *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// loadConfigForTest points the config package at a fresh temp dir so tests
// don't touch real state; LoadConfig writes/reads config.json there.
func loadConfigForTest(t *testing.T) {
	t.Helper()
	t.Setenv("TIMERPI_DATA_DIR", t.TempDir())
	t.Setenv("CAPACITIMER_HTTP_PORT", "8080")
	config.LoadConfig()
}

// Default config (empty allowed_hosts) is OPEN mode: any public Host is
// accepted — reverse proxies and custom domains just work.
func TestHostAllowedOpenMode(t *testing.T) {
	loadConfigForTest(t)
	if got := config.AllowedHosts(); len(got) != 0 {
		t.Fatalf("default allowed_hosts = %v, want empty (open mode)", got)
	}

	cases := []string{
		"myshow.example.com",  // arbitrary custom domain (proxy)
		"evil.com",            // a rebinding attacker: accepted in open mode by design
		"show.example.com:80", // hostport forms
		"192.168.1.50",        // LAN IP literal
		"localhost",           // loopback name
		"timerpi.local",       // mDNS
		"pi-2.lan",            // router-style LAN name
		"timerpi",             // dotless
	}
	for _, host := range cases {
		if !hostAllowed(host) {
			t.Errorf("hostAllowed(%q) = false, want true (open mode)", host)
		}
	}
}

// A non-empty allowed_hosts switches to STRICT mode: only listed names
// (plus always-local hosts) pass; everything else gets 421 via OriginGuard.
func TestHostAllowedStrictMode(t *testing.T) {
	loadConfigForTest(t)
	if err := config.SetAllowedHosts("Show.Example.com, timerpi.drevilish.com"); err != nil {
		t.Fatal(err)
	}

	allowed := []string{
		"show.example.com",      // listed (case-insensitive match below)
		"SHOW.example.COM:8443", // listed, hostport + case
		"timerpi.drevilish.com", // second listed name
		"192.168.1.50",          // always-local set still passes
		"localhost",
		"timerpi.local",
		"pi-2.lan",
		"timerpi", // dotless
	}
	for _, host := range allowed {
		if !hostAllowed(host) {
			t.Errorf("hostAllowed(%q) = false, want true (strict mode)", host)
		}
	}

	refused := []string{
		"evil.com",
		"other.example.com", // a DIFFERENT custom domain: refused in strict mode
		"sub.show.example.com",
	}
	for _, host := range refused {
		if hostAllowed(host) {
			t.Errorf("hostAllowed(%q) = true, want false (strict mode)", host)
		}
	}
}

// Emptying the list at runtime returns to open mode.
func TestHostAllowedClearingReturnsToOpen(t *testing.T) {
	loadConfigForTest(t)
	if err := config.SetAllowedHosts("show.example.com"); err != nil {
		t.Fatal(err)
	}
	if hostAllowed("evil.com") {
		t.Fatal("strict mode not active after SetAllowedHosts")
	}
	if err := config.SetAllowedHosts(""); err != nil {
		t.Fatal(err)
	}
	if !hostAllowed("evil.com") || !hostAllowed("myshow.example.com") {
		t.Error("emptying allowed_hosts must return to open mode (any Host)")
	}
}

// SameOriginRequest is the WS upgrader's CheckOrigin hook; keep it honest.
func TestSameOriginRequest(t *testing.T) {
	req, _ := http.NewRequest("POST", "http://show.example.com/health", nil)
	req.Host = "show.example.com"
	req.Header.Set("Origin", "http://show.example.com")
	if !SameOriginRequest(req) {
		t.Error("same-origin request refused")
	}
	req.Header.Set("Origin", "http://evil.com")
	if SameOriginRequest(req) {
		t.Error("cross-origin request accepted")
	}
	req.Header.Del("Origin") // curl/scripts: not browser-driven → pass
	if !SameOriginRequest(req) {
		t.Error("non-browser request refused")
	}
	// Hostport must match the Origin's hostport too.
	req.Header.Set("Origin", "http://show.example.com:8080")
	if SameOriginRequest(req) {
		t.Error("origin with different port accepted")
	}
}

// Guard middleware end-to-end: open mode 200, strict mode 421.
func TestOriginGuardMiddleware(t *testing.T) {
	loadConfigForTest(t)

	r := New(nil)
	open, _ := http.NewRequest("GET", "http://server/health", nil)
	open.Host = "myshow.example.com"
	w := recordResponse(r, open)
	if w.Code != http.StatusOK {
		t.Errorf("open mode: GET /health with custom Host = %d, want 200", w.Code)
	}

	if err := config.SetAllowedHosts("show.example.com"); err != nil {
		t.Fatal(err)
	}
	strict, _ := http.NewRequest("GET", "http://server/health", nil)
	strict.Host = "evil.com"
	w = recordResponse(r, strict)
	if w.Code != http.StatusMisdirectedRequest {
		t.Errorf("strict mode: GET /health with unlisted Host = %d, want 421", w.Code)
	}

	ok, _ := http.NewRequest("GET", "http://server/health", nil)
	ok.Host = "show.example.com"
	w = recordResponse(r, ok)
	if w.Code != http.StatusOK {
		t.Errorf("strict mode: GET /health with listed Host = %d, want 200", w.Code)
	}
}
