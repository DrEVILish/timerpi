// Package routes wires TimerPi's HTTP surface: pages (html/template), the
// JSON API, static assets, vendored themes, health, and the origin guard.
// The WS hub lives in package ws and is mounted through the HubMount
// interface (no import cycle: ws imports routes for SameOriginRequest).
package routes

import (
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/timerpi"
	"timerpi/views"
)

// startedAt backs the uptime field in /health.
var startedAt = time.Now()

// SessionsCount is the standalone seam for /health when no Deps.Hub is
// injected (skeleton/testing mode). main.go installs the hub's counter.
var SessionsCount func() int

// HubMount is the subset of ws.Hub routes needs (interface, not import:
// package ws imports routes for SameOriginRequest).
type HubMount interface {
	Register(gin.IRouter)
	Sessions() int
	SessionsByRole() map[string]int
	// F1 screens: targeted push + live presence (screen name → live
	// session count). Implemented by ws.Hub.
	SendToScreen(showID int64, screen string, frames ...[]byte) int
	ScreenSessions(showID int64) map[string]int
	SendToRole(showID int64, role string, frame []byte) int
	KickSession(showID int64, peerID string) int
	ScreenPeers(showID int64) map[string][][2]string
	// Super panel: total live sessions per show (PLAN §11.1).
	ShowSessions(showID int64) int
	// PLAN §11.5 audience lane: poll-only delta broadcast + lane count.
	BroadcastPoll(showID int64)
	AudSessions() int
}

// Deps carries the wired services; nil fields degrade to skeleton behavior
// (health/static/origin only, pages/API answer 501/503) so tests and
// `go build ./...` keep working while the domain side is mid-flight.
type Deps struct {
	Engines *timerpi.Engines
	Store   *timerpi.DB
	Hub     HubMount
	Tmpl    *views.Set
	// ReloadTmpl reparses the template set per request (-dev flag);
	// production stays parsed-once.
	ReloadTmpl bool
	// Public is the public/ tree to serve (STATUS C9: the binary's embedded
	// copy in production). Nil = public/ on disk (tests, -dev).
	Public fs.FS
}

// bodyCeiling bounds every request body: 8 MiB for plain JSON/form posts,
// 32 MiB for the import/bundle surfaces that legitimately carry documents.
// Caps apply before handlers decode, so a multi-GB POST is rejected as a
// bind error instead of being absorbed into memory first.
func bodyCeiling() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut {
			limit := int64(8 << 20)
			if strings.Contains(c.Request.URL.Path, "/import") {
				limit = 32 << 20
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// New builds the gin engine.
func New(d *Deps) *gin.Engine {
	if !isDev() {
		gin.SetMode(gin.ReleaseMode)
	}
	if d == nil {
		d = &Deps{}
	}
	r := gin.New()
	// Only a reverse proxy on this box may set the client IP (audience
	// device minting is budgeted per IP; X-Forwarded-For from anyone else
	// would let a script pick its own address).
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "::1"})
	// Cache policy (wholesale): HTML, API and WS-upgrade responses always
	// revalidate — an appliance on a LAN cannot do cache-busting deploys, so
	// a stale <main> document would pin old template markup AND old ?v=
	// asset references forever. Static file mounts re-set the same header
	// themselves (registerStatic/registerFTL).
	r.Use(func(c *gin.Context) { c.Header("Cache-Control", "no-cache"); c.Next() })
	r.Use(gin.Recovery(), bodyCeiling(), OriginGuard(), d.accessGate())

	registerFTL(r) // /ftl/ — vendored ftl-themes (theme picker fonts)
	registerHealth(r, d, SessionsCount)
	registerTheme(r)      // B7: appliance default theme (GET/POST /api/theme)
	registerPages(r, d)   // GET /, /c/:ident, /screens/:ident (/d/ in display.go)
	RegisterDisplay(r, d) // GET /d/:ident — stage passthrough + view dispatch
	RegisterSetup(r, d)   // GET /setup + /setup/sheet + /api/setup/*
	RegisterNetwork(r)    // GET /settings + /api/network/* (503s until InstallNetwork)
	registerAPI(r, d)     // REST per PROTOCOL §REST + CONTRACT-UI §3
	// PLAN §11 (Rooms v2, stitched phase 0): audience surface, zone walk-in
	// pages, OSC bridge settings; the OSC listener boots on saved settings.
	registerAudienceRoutes(r, d)
	registerZoneRoutes(r, d)
	registerOscRoutes(r, d)
	registerAssetRoutes(r, d)
	registerEvents(r, d) // /e/:code lobby + admin, /api/events/*
	registerBox(r, d)    // /box + /api/box/* — the box password (settings)
	if d.Store != nil {
		if oserr := d.oscSync(); oserr != nil {
			log.Printf("routes: osc listener boot: %v", oserr)
		}
	}
	RegisterBoards(r, d) // display-board CRUD (Agent N; ?view=board renders in display.go)
	if d.Hub != nil {    // WS upgrade — same port, same origin rules
		d.Hub.Register(r)
	}
	registerStatic(r, d.Public) // public/ assets as catch-all (NoRoute)

	return r
}

func isDev() bool {
	return os.Getenv("TIMERPI_DATA_DIR") != ""
}

// findDir locates a repository-relative asset directory: the binary runs
// from the checkout in dev, and the systemd unit pins WorkingDirectory —
// but try the executable's directory as fallback.
func findDir(name string) string {
	candidates := []string{name}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), name))
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return name // let the caller's os.Stat produce the real error
}

// noCache is the static-asset freshness policy: always revalidate (the
// FileServer answers 304 unless the mtime moved), never render stale
// CSS/JS/htmx after an appliance update. An appliance on a LAN cannot do
// cache-busting deploys, and heuristic caching left stale CSS in tabs for
// hours (seen live during the current-cue-rail fix).
func noCache() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		c.Next()
	}
}

// registerFTL mounts the vendored ftl-themes at /ftl/ — the whole repo root
// so dist/ and assets/ stay siblings: theme CSS at /ftl/dist/<theme>.css
// references fonts as ../assets/fonts/... which then resolves to
// /ftl/assets/fonts/... . themes.json + per-theme icon bundles serve from
// there too (theme-loader pattern, CONTRACT-UI §7).
func registerFTL(r *gin.Engine) {
	dir := findDir(filepath.Join("third_party", "ftl-themes"))
	if _, err := os.Stat(dir); err != nil {
		log.Printf("routes: ftl-themes tree missing, /ftl disabled: %v", err)
		return
	}
	r.Group("/ftl", noCache()).Static("/", dir)
}

// registerHealth is the readiness probe. Sessions wiring: the injected
// hub's counter when present, else the standalone SessionsCount seam.
func registerHealth(r *gin.Engine, d *Deps, standalone func() int) {
	// Browsers probe /favicon.ico on pages without an icon link.
	r.GET("/favicon.ico", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/img/timerpi.svg") })
	r.GET("/health", func(c *gin.Context) {
		var connected map[string]int
		if d.Hub != nil {
			connected = d.Hub.SessionsByRole()
		}
		total := 0
		for _, n := range connected {
			total += n
		}
		if connected == nil && standalone != nil {
			total = standalone()
		}
		c.JSON(http.StatusOK, gin.H{
			"ok":      true,
			"version": appVersion(),
			"uptime":  time.Since(startedAt).Truncate(time.Second).String(),
			"device":  config.DeviceName(),
			"title":   config.Title(),
			"sessions": gin.H{
				"connected": total,
			},
		})
	})
}

// registerStatic serves public/ (css, src/htmx, img) at the site root as a
// catch-all: any path without a route tries the static tree, so "/" stays
// owned by the homepage. Additionally serves PATH-REV assets
// (/css/v11/timerpi.css → public/css/timerpi.css): appliances behind
// middleboxes that ignore the no-cache policy and cache-busting queries
// only see fresh bytes when the URL PATH changes per revision; legacy
// rev-less paths keep resolving (old bookmarks/templates keep working).
func registerStatic(r *gin.Engine, pub fs.FS) {
	if pub == nil {
		pub = os.DirFS(findDir("public"))
	}
	files := http.FileServer(http.FS(pub))
	rev := regexp.MustCompile(`^/(css|src|img)/v[0-9]+(/.*)$`)
	r.NoRoute(func(c *gin.Context) {
		// Only serve real files; directories (no index.html in most) and
		// misses fall through to the app's 404.
		p := c.Request.URL.Path
		if m := rev.FindStringSubmatch(p); m != nil {
			// strip the vN segment IN THE REQUEST — http.FileServer maps from
			// URL.Path itself, so re-shaping only our local variable left it
			// looking for v11 inside the real tree (that was the 404).
			c.Request.URL.Path = "/" + m[1] + m[2]
			p = c.Request.URL.Path
		}
		p = path.Clean(p)
		if p == "." || p == "/" {
			c.String(http.StatusNotFound, "not found")
			return
		}
		if st, err := fs.Stat(pub, strings.TrimPrefix(p, "/")); err == nil && st.Mode().IsRegular() {
			c.Header("Cache-Control", "no-cache") // same freshness policy as /ftl
			files.ServeHTTP(c.Writer, c.Request)
			return
		}
		c.String(http.StatusNotFound, "not found")
	})
}
