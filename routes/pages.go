// package routes — pages.go: the html/template pages (homepage, operator
// dashboard, display) and the htmx fragment route. Field shapes per
// CONTRACT-UI.md §2 are built by package views.
package routes

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/timerpi"
	"timerpi/views"
)

// registerPages mounts GET /, GET /c/:ident and GET /screens/:ident
// (/d/:ident lives in display.go).
// Public addressing is CODE-ONLY (Agent L scope change): :ident must be a
// normalized share code; numeric ids 404 — they are internal DB keys, not
// addresses.
func registerPages(r *gin.Engine, d *Deps) {
	r.GET("/", homePage(d))
	r.GET("/c/:ident", dashboardPage(d))
	r.GET("/screens/:ident", galleryPage(d))
	// GET /d/:ident lives in display.go (RegisterDisplay: stage passthrough +
	// ?view=next|daysheet|clock dispatch). Kept out of registerPages so the
	// two mounts can never collide in gin's route tree.
}

// resolveIdent maps a public :ident to a show id via the code rulebook
// (timerpi.ResolveShowID): NormalizeCode (typo maps, dash/space strip,
// uppercase) then `WHERE code = ?`. Digits and non-codes → found=false.
func (d *Deps) resolveIdent(ident string) (int64, bool) {
	if d == nil || d.Store == nil {
		return 0, false
	}
	return timerpi.ResolveShowID(d.Store, ident)
}

// render executes a template on the registry; -dev reparses per request.
func (d *Deps) render(c *gin.Context, name string, data any) {
	tmpl := d.Tmpl
	if tmpl == nil {
		c.String(http.StatusNotImplemented, "template registry missing (mismatched build)")
		return
	}
	if d.ReloadTmpl {
		var err error
		tmpl, err = views.New(findDir("templates"))
		if err != nil {
			log.Printf("routes: dev template reparse: %v", err)
		}
	}
	if err := tmpl.Render(c.Writer, name, data); err != nil {
		// Headers may already be out; log loudly — a blank page is a bug,
		// every route builds its data defensively.
		log.Printf("routes: template %q: %v", name, err)
	}
}

// homePage is GET /. Privacy rework (2026-10-03): the homepage NEVER lists
// shows — the share code is the credential and no operator-password-less
// appliance may leak codes/titles to a LAN passer-by. "Recent" is filled
// client-side from this browser's localStorage. Create + join + examples
// only.
func homePage(d *Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		data := views.HomeData(nil)
		data.Peers = d.peerCount()
		d.render(c, "base", data)
	}
}

// listShowRows reads the homepage metadata (shows + cue count + day total).
func (d *Deps) listShowRows() (views.ShowMetas, error) {
	if d.Store == nil {
		return nil, nil
	}
	shows, err := d.Store.ListShows()
	if err != nil {
		return nil, err
	}
	metas := make(views.ShowMetas, 0, len(shows))
	for _, s := range shows {
		total := int64(0)
		n := 0
		if cues, cerr := d.Store.ListCues(s.ID); cerr == nil {
			n = len(cues)
			sched := timerpi.ComputeSchedule(cues, 0, 1)
			total = sched.TotalMS
		}
		metas = append(metas, views.ShowMeta{Show: s, CueCount: n, TotalMS: total})
	}
	return metas, nil
}

// dashboardPage is GET /c/:ident — the operator dashboard. :ident is a
// share code (Agent L scope change): digits/unknown → friendly 404.
func dashboardPage(d *Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		showID, ok := d.resolveIdent(c.Param("ident"))
		if !ok {
			pageUnknownCode(c)
			return
		}
		// Per-show passphrase gate (showauth.go): locked shows render the
		// unlock page before ANY cue content is server-rendered.
		if d.Store != nil && !showGateByShowID(c, d, showID) {
			return
		}
		if d.Engines == nil {
			c.String(http.StatusNotImplemented, "engine wiring missing (mismatched build)")
			return
		}
		eng, err := d.Engines.Get(showID)
		if err != nil {
			pageNotFound(c)
			return
		}
		snap, err := eng.Snapshot()
		if err != nil {
			// Unknown show → 404 page (operator typed a bad id).
			pageNotFound(c)
			return
		}
		data := views.ShowData(snap, d.now(), "")
		if data == nil {
			pageError(c, err)
			return
		}
		// The snapshot carries shown-only messages; the panel also manages
		// queued rows (Show/Hide buttons) from the full DB listing.
		if d.Store != nil {
			if msgs, merr := d.Store.ListMessages(showID); merr == nil {
				data.SetMessages(msgs)
			}
		}
		data.Peers = d.peerCount()
		d.render(c, "base", data)
	}
}

// galleryPage is GET /screens/:ident — the operator screens gallery:
// live preview of every registered display, the waiting room (displays
// orphaned by a deleted show, with Capture/Dismiss), and the maximised
// layout editor. Cards render client-side from /api/shows/:code/screens.
func galleryPage(d *Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		showID, ok := d.resolveIdent(c.Param("ident"))
		if !ok {
			pageUnknownCode(c)
			return
		}
		if d.Store != nil && !showGateByShowID(c, d, showID) {
			return
		}
		if d.Engines == nil {
			c.String(http.StatusNotImplemented, "engine wiring missing (mismatched build)")
			return
		}
		eng, err := d.Engines.Get(showID)
		if err != nil {
			pageNotFound(c)
			return
		}
		snap, err := eng.Snapshot()
		if err != nil {
			pageNotFound(c)
			return
		}
		data := views.ShowData(snap, d.now(), "")
		if data == nil {
			pageError(c, err)
			return
		}
		data.Page = "screens"
		data.Nav = "screens"
		data.Peers = d.peerCount()
		d.render(c, "base", data)
	}
}

// showGateByShowID is the show-arm of showGate for handlers that only have
// the resolved id: loads the row, then applies the passphrase check.
func showGateByShowID(c *gin.Context, d *Deps, showID int64) bool {
	sh, err := d.Store.GetShow(showID)
	if err != nil {
		return true // unknown/merged row: let the normal 404/snapshot paths answer
	}
	return showGate(c, sh)
}

// now anchors IsDone flags; wall clock elsewhere.
func (d *Deps) now() int64 { return time.Now().UnixMilli() }

// hostname picks the mDNS-friendly display name (device config, else OS).
func hostname() string {
	if n := strings.TrimSpace(config.DeviceName()); n != "" {
		return n
	}
	if hn, err := os.Hostname(); err == nil {
		return hn
	}
	return ""
}

// peerCount is informational (CONTRACT-UI .Peers); hub optional.
func (d *Deps) peerCount() int {
	if d.Hub != nil {
		return d.Hub.Sessions()
	}
	if SessionsCount != nil {
		return SessionsCount()
	}
	return 0
}

// pageError logs and answers a plain 500 (template data bugs surface here
// during integration).
func pageError(c *gin.Context, err error) {
	log.Printf("routes: %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
	c.String(http.StatusInternalServerError, "server error")
}

func pageNotFound(c *gin.Context) {
	c.String(http.StatusNotFound, "no such show")
}

// pageUnknownCode is the friendly code-only 404 (Agent L scope change):
// public addresses are share codes; digits or a miss tell the operator so.
func pageUnknownCode(c *gin.Context) {
	c.String(http.StatusNotFound, "Unknown session code — check the 4-4 code on the operator dashboard.")
}
