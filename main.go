// TimerPi — cue-list show timer appliance.
//
// Single binary serving HTTP + WebSocket on one port. See PLAN.md and
// PROTOCOL.md. Wiring here: config → SQLite open → engine registry →
// template registry → WS hub (+ticker) → gin → serve → graceful shutdown.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/mesh"
	"timerpi/oscbridge"
	"timerpi/routes"
	"timerpi/timerpi"
	"timerpi/views"
	"timerpi/ws"
)

const readyDir = "/run/timerpi" // EPHEMERAL (tmpfs) — splash handshake dir

// webFiles holds the pages (templates/) and browser files (public/) this
// binary was built with (STATUS C9). ftl-themes stay on disk: a pinned
// submodule, large, and independent of TimerPi's code.
//
//go:embed templates public
var webFiles embed.FS

func main() {
	debug := flag.Bool("debug", false, "gin debug mode (launcher defaults to release)")
	devTmpl := flag.Bool("dev", false, "reparse templates per request (template dev loop)")
	flag.Parse()

	config.LoadConfig()

	// Release mode default; -debug or GIN_MODE opts out.
	if os.Getenv("GIN_MODE") == "" && !*debug {
		gin.SetMode(gin.ReleaseMode)
	}

	dataDir := config.DataDir()
	db, err := timerpi.Open(filepath.Join(dataDir, "timerpi.db"))
	if err != nil {
		log.Fatalf("timerpi: opening db: %v", err)
	}
	defer db.Close()
	engines := timerpi.NewEngines(db)

	// PLAN §11.6 outbound media bridge (OSC, stitched phase 0): cue fires
	// (engine onFire → registry OnStart) go to the paired CuTePi/QLab peer;
	// BLANK routes the panic image. Target read per event from settings.
	engines.OnStart = func(showID, pos int64) { oscbridge.FireOut("cue", pos) }
	oscbridge.Target = func() string {
		kv, err := db.AllSettings()
		if err != nil || kv["osc.out.enabled"] != "1" {
			return ""
		}
		host := kv["osc.out.host"]
		if host == "" {
			return ""
		}
		port := kv["osc.out.port"]
		if port == "" {
			port = "53000" // QLab/CuTePi default
		}
		return host + ":" + port
	}

	// Template registry: parse once (hot reload is -dev only). Production
	// uses the pages and scripts embedded at build time, so the binary can
	// never serve another build's files (STATUS C9, B8); -dev reads disk.
	var tmplSet *views.Set
	var public fs.FS
	if *devTmpl {
		tmplSet, err = views.New(findTemplates())
	} else {
		tmplSet, err = views.NewFS(webFiles)
		public, _ = fs.Sub(webFiles, "public")
	}
	if err != nil {
		log.Fatalf("timerpi: parsing templates: %v", err)
	}

	// WS hub: same port, same origin rules; the ticker (250 ms) drives
	// zero crossings/alerts/auto-advance across ALL engines.
	hub := ws.NewHub(engines, tmplSet.Fragment)
	hub.SetStore(db)
	hub.SetMessagesFunc(db.ListMessages)
	// PLAN §11 audience layer: the on-air interaction rides every frame.
	hub.SetPollsFunc(db.OnAirNow)
	hub.SetSeeder(func() []int64 {
		shows, err := db.ListShows()
		if err != nil {
			return nil
		}
		ids := make([]int64, 0, len(shows))
		for _, s := range shows {
			ids = append(ids, s.ID)
		}
		return ids
	})
	hub.Start()

	// Wire the hub counter into /health.
	routes.SessionsCount = hub.Sessions

	// Device mesh (Agent H): mDNS announce/browse + PRIMARY claim/takeover.
	// Degrades to 503 identity endpoints if mesh fails to start — the
	// appliance must keep serving even with mesh persistence off.
	// TIMERPI_MESH=off keeps a test or dev server off the network: no mDNS
	// announcements another box could react to.
	var meshYB *mesh.Device
	meshErr := fmt.Errorf("disabled by TIMERPI_MESH=off")
	if !strings.EqualFold(os.Getenv("TIMERPI_MESH"), "off") {
		meshYB, meshErr = mesh.New(mesh.Options{
			Port:    config.HTTPPort(),
			Version: "v1",
			DBPath:  filepath.Join(dataDir, "timerpi.db"),
		})
	}
	if meshErr != nil {
		log.Printf("timerpi: mesh device off: %v", meshErr)
	} else {
		meshCtx, meshCancel := context.WithCancel(context.Background())
		defer meshCancel()
		meshYB.Start(meshCtx)
		defer meshYB.Stop()
		defer meshYB.Close()
		routes.InstallNetwork(&routes.NetworkDeps{
			Device:     meshYB,
			Tmpl:       tmplSet,
			ReloadTmpl: *devTmpl,
		})
	}

	// HDMI renderer (drm/): runs the 50 fps present loop when
	// TIMERPI_DISPLAY selects drm/fb; off/absent → headless as usual.
	drmSel, drmClock := startDRMClock(engines, db)
	if drmClock != nil {
		drmCtx, drmCancel := context.WithCancel(context.Background())
		defer drmCancel()
		defer drmSel.Backend.Close()
		go func() {
			if err := drmClock.Run(drmCtx); err != nil {
				log.Printf("timerpi: display loop ended: %v", err)
			}
		}()
	}

	g := routes.New(&routes.Deps{
		Engines:    engines,
		Store:      db,
		Hub:        hub,
		Tmpl:       tmplSet,
		ReloadTmpl: *devTmpl,
		Public:     public,
	})

	addr := fmt.Sprintf(":%d", config.HTTPPort())
	srv := &http.Server{
		Addr:              addr,
		Handler:           g,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Bind BEFORE declaring readiness: a failed bind must not produce a
	// ready marker telling the splash to stand down.
	lnr, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("timerpi: listen %s: %v", addr, err)
	}
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("timerpi: listening on %s (data dir %s, title %q)",
			addr, dataDir, config.Title())
		serveErr <- srv.Serve(lnr)
	}()

	// Splash handoff (Agent E): signal "app ready" only after the
	// listener is live, so the fbdev splash (scripts/splash) can release
	// the framebuffer instead of hitting its 45 s timeout. /run is
	// ephemeral tmpfs; never configured for persistence.
	readyFile := writeReadyFile()

	// Graceful shutdown: release listeners cleanly so systemd restarts
	// without orphaned sockets and mesh peers see the close.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
		log.Println("timerpi: shutting down")
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			removeReadyFile(readyFile)
			log.Fatalf("timerpi: http server: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("timerpi: shutdown: %v", err)
	}
	removeReadyFile(readyFile)
	log.Println("timerpi: stopped")
}

// writeReadyFile creates /run/timerpi/ready (0644, empty) for the splash
// service to detect "app ready" and release the framebuffer. Failure is
// non-fatal: the splash falls back to its own timeout.
func writeReadyFile() string {
	if err := os.MkdirAll(readyDir, 0o755); err != nil {
		log.Printf("timerpi: ready dir: %v", err)
		return ""
	}
	path := filepath.Join(readyDir, "ready")
	f, err := os.Create(path)
	if err != nil {
		log.Printf("timerpi: ready file: %v", err)
		return ""
	}
	if err := f.Chmod(0o644); err != nil {
		log.Printf("timerpi: ready chmod: %v", err)
	}
	f.Close()
	log.Printf("timerpi: ready (%s) — splash may release the framebuffer", path)
	return path
}

// removeReadyFile unlinks the ready marker on shutdown (splash-independent,
// best-effort).
func removeReadyFile(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path) // /run is ephemeral anyway; no persistence ever
}

// findTemplates resolves the checkout-relative template root (mirrors
// routes.findDir; kept local to avoid exporting that helper).
func findTemplates() string {
	for _, c := range []string{"templates", exeJoin("templates")} {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return "templates"
}

func exeJoin(name string) string {
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	return filepath.Join(filepath.Dir(exe), name)
}
