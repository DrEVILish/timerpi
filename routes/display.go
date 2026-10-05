// Package routes — display.go: venue display-variant boards (Agent J).
//
// One screen URL, dispatched on ?view= (contract addendum documented in
// reviews/NOTES-display.md; templates live in templates/display_variants.html
// + templates/fragments/d-*.html, all owned here):
//
//	GET /d/:ident                   → fullscreen stage (Fix-1's display.html;
//	                                  thin passthrough of pages.go's body)
//	GET /d/:ident?view=next         → NEXT-UP board (+ time-until card)
//	GET /d/:ident?view=daysheet     → whole-day schedule table (printable)
//	GET /d/:ident?view=clock        → lobby/pause filler clock
//	GET /d/:ident?view=stage        → 302 to the canonical bare URL
//
// Per-screen quirks (query only, VALIDATED HERE, applied by the template as
// scoped CSS vars / body data — no shared JS involvement):
//
//	?accent=%237C3AED   hex only (#hex or bare hex, 3/6 digits) → --tp-accent
//	?bg=%23000000       hex only → --tp-bg
//	?fit=cover|contain|shadow → <body data-fit> (unknown → "contain")
//	?print=1            (daysheet) print-optimized look on screen
//
// Bad hex answers 400; screens bookmark the URL so a typo must not serve a
// board that silently ignores the override.
//
// CONSOLIDATION SEAM (one line — see reviews/NOTES-display.md): routes.New
// must drop pages.go's `r.GET("/d/:ident", displayPage(d))` and add
// `RegisterDisplay(r, d)` after registerAPI(r, d). Until then this handler
// is exercised by routes/display_test.go + throwaway instances only. The
// registration is guarded: when /d/:ident is already mounted it logs and
// SKIPS (gin panics on duplicate registration — never let an integration
// order flip blow up the server boot).
//
// The inline client for the variant boards is display_variants.html's own
// module (imports public/src/engine.js read-only for client-parity math);
// shared JS in public/src/*.js is untouched.
package routes

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"timerpi/config"

	"timerpi/timerpi"
	"timerpi/views"
)

// displayViews are the variant views dispatched in display_variants.html;
// "" and "stage" are handled before this set is consulted.
const (
	viewStage    = "stage" // redirect passthrough (canonical URL owns the stage)
	defaultStage = ""      // bare URL default = the existing fullscreen timer
)

// RegisterDisplay mounts GET /d/:ident with the view dispatch above. See
// the package doc block for the one-line consolidation seam. Agent L
// (scope change): the path param is :ident — share CODE only (matching
// pages.go's registration keeps gin's route tree one-wildcard-safe; a
// differing name here would panic Register). Resolution is code-only via
// Deps.resolveIdent.
func RegisterDisplay(r gin.IRouter, d *Deps) {
	if d == nil {
		return
	}
	if eng, ok := r.(*gin.Engine); ok {
		for _, ri := range eng.Routes() {
			if ri.Method == http.MethodGet && ri.Path == "/d/:ident" {
				log.Printf("routes: /d/:ident already registered (pages.go displayPage); " +
					"RegisterDisplay skipped — consolidation must swap the pages.go line per reviews/NOTES-display.md")
				return
			}
		}
	}
	r.GET("/d/:ident", d.displayVariants)
	// PLAN §11.3 phase 1: /d/ with NO code — a walk-in display in ready
	// mode: registers into the waiting room immediately and hops to the
	// captured show when an operator claims it. Gin serves static +
	// param siblings fine; /d without the slash 301s here (default).
	r.GET("/d/", d.displayReady)
}

// displayReady is GET /d/ — the no-code walk-in surface. The template IS
// the waiting overlay (body[data-waiting] server-side); waiting.js's
// runWaiting() starts the register/poll loop. Identity rules are the
// mesh's own: URL ?screen= wins, then sessionStorage, else generated
// Screen-XXXX — so several /d/ tabs on one host stay separate rows.
func (d *Deps) displayReady(c *gin.Context) {
	d.render(c, "dready", gin.H{"DefaultTheme": config.DefaultTheme()})
}

// displayVariants is GET /d/:ident — view dispatch + quirk validation.
// Agent L (scope change): :ident is the share CODE (code-only addressing);
// a numeric id or any non-code → friendly 404.
func (d *Deps) displayVariants(c *gin.Context) {
	view := c.Query("view")
	if view == "" {
		view = defaultStage
	}
	if view == viewStage {
		// Officially documented passthrough: ?view=stage is a normalizing
		// redirect to the canonical URL, which serves the stage directly
		// (no loop — the no-query case renders the stage in place).
		c.Redirect(http.StatusFound, "/d/"+c.Param("ident"))
		return
	}

	switch view {
	case defaultStage, "next", "daysheet", "clock", "board":
	default:
		pageNotFound(c)
		return
	}

	// Per-screen quirks — validate BEFORE touching the engine (screens
	// bookmark these URLs; a typo'd color must fail fast, not serve a
	// board that quietly drops the override).
	accent, err := hexParam(c, "accent")
	if err != nil {
		c.String(http.StatusBadRequest, "invalid accent: %v", err)
		return
	}
	bg, err := hexParam(c, "bg")
	if err != nil {
		c.String(http.StatusBadRequest, "invalid bg: %v", err)
		return
	}

	showID, ok := d.resolveIdent(c.Param("ident"))
	if !ok {
		pageUnknownCode(c)
		return
	}
	if d.Engines == nil {
		c.String(http.StatusNotImplemented, "engine wiring missing (mismatched build)")
		return
	}
	eng, err := d.Engines.Get(showID)
	if err != nil {
		pageUnknownCode(c)
		return
	}
	// Per-show passphrase gate (showauth note: TVs are NOT exempted — an
	// "extra password" that display pages skipped would be no extra
	// security; a TV enters it once and the 7-day cookie holds).
	if d.Store != nil && !showGateByShowID(c, d, showID) {
		return
	}
	snap, err := eng.Snapshot()
	if err != nil {
		pageUnknownCode(c)
		return
	}

	if view == defaultStage {
		// Stage passthrough: exactly pages.go displayPage's body (the stage
		// template stays Fix-1's; consolidator may delete displayPage —
		// this branch is then the WHOLE /d/ route).
		data := views.ShowData(snap, d.now(), hostname())
		data.Role = "display" // lands on <body data-role>
		data.Nav = "display"
		data.Peers = d.peerCount()
		d.render(c, "display", data)
		return
	}

	if view == "board" {
		// Customizable display board (Agent N, routes/boards.go): composed
		// from draggable widgets; ?board=<id>, default = show default.
		d.boardView(c, showID, snap)
		return
	}

	data := d.variantData(c, snap, showID, view, accent, bg)
	d.render(c, "display_variants", data)
}

// ---------------------------------------------------------------------------
// Variant page data (self-contained shapes routed on the dot; page shell is
// a standalone document like display.html — CONTRACT-UI §1 note).

// variantData is the dot for display_variants.html + fragments/d-*.html.
// PageData is embedded so the CONTRACT-UI field names keep working (.Show,
// .Next, .Schedule, .Cues…); the extra fields carry the dispatch + quirks.
type variantData struct {
	*views.PageData
	View        string     // "next" | "daysheet" | "clock"
	Accent      string     // "" (default #7C3AED) or validated #hex
	BGFmt       string     // validated #hex for --tp-bg ("BGFmt" stays clear of any future PageData field)
	Fit         string     // "contain" | "cover" | "shadow"
	Print       bool       // ?print=1
	Join        joinVM     // screen-side join affordance (QR card)
	Daysheet    []dayRowVM // whole-day rows (daysheet view)
	NextSpeaker string     // next cue's speaker (views.NextCue has none)
	NextStart   string     // scheduled start of the next cue (HH:MM:SS / offset)
}

// joinVM backs the small "add display" corner card: mini QR of THIS URL,
// the control-room link, and the mDNS host hint. All client-cheap: one
// server-rendered <img src> against the existing /api/shows/:id/qr route.
type joinVM struct {
	Self    string // absolute URL of this board (view + quirks preserved)
	QR      string // /api/shows/:id/qr?data=<urlencoded Self>&size=132
	Control string // /c/:id — operator control room
	Host    string // "<hostname>.local" (empty when unknown)
}

// variantData builds the dot: snapshot → CONTRACT view data + view dispatch.
func (d *Deps) variantData(c *gin.Context, snap timerpi.Snapshot, showID int64, view, accent, bg string) variantData {
	pd := views.ShowData(snap, d.now(), hostname())
	// ShowData fills .Next only from runtime.NextPos; an idle show has none,
	// while boards read better with the day's first cue up front (the variant
	// module's client mirror picks the SAME first cue when nextPos==0, so
	// server pre-render and first WS adopt agree).
	if pd.Next.Pos == 0 && len(pd.Cues) > 0 {
		first := pd.Cues[0]
		pd.Next = views.NextCue{
			Pos:      first.Pos,
			Label:    first.Label,
			DurFmt:   first.DurFmt,
			StartFmt: first.StartFmt,
		}
	}
	data := variantData{
		PageData:  pd,
		View:      view,
		Accent:    accent,
		BGFmt:     bg,
		Fit:       fitParam(c),
		Print:     c.Query("print") == "1",
		Join:      joinVMof(c, snap),
		Daysheet:  dayRows(pd),
		NextStart: pd.Next.StartFmt,
	}
	if next := pd.Next.Pos; next != 0 {
		for _, cue := range snap.Cues {
			if cue.Pos == next {
				data.NextSpeaker = cue.Speaker
				break
			}
		}
	}
	return data
}

// joinVMof derives the corner-card fields off THIS request (absolute URL
// incl. query, so the QR lands on the exact same view + quirks). Scheme: the
// request's when TLS-terminated here, else http (LAN appliance). The /qr
// route clamps size 64..1024; 132 is the corner-card target.
// Agent L (scope change): the card's links address the show by CODE (numeric
// ids are internal; /c/<id> and /api/shows/<id>/qr are dead routes now).
func joinVMof(c *gin.Context, snap timerpi.Snapshot) joinVM {
	self := *c.Request.URL
	self.Host = c.Request.Host
	self.Scheme = "http"
	if c.Request.TLS != nil {
		self.Scheme = "https"
	}
	selfURL := self.String()
	code := timerpi.NormalizeCode(snap.Show.Code)
	return joinVM{
		Self:    selfURL,
		QR:      "/api/shows/" + code + "/qr?data=" + url.QueryEscape(selfURL) + "&size=132",
		Control: "/c/" + code,
		Host:    hostname(),
	}
}

// dayRowVM is one whole-day schedule row (daysheet view; server-computed
// start/end via views' ComputeSchedule — degrade-to-client only happens in
// the inline JS when a live WS re-renders after a mid-show edit).
type dayRowVM struct {
	Pos      int64
	Label    string
	Speaker  string
	DurFmt   string
	StartFmt string
	EndFmt   string
	HoldFmt  string
	Color    string
	IsBreak  bool
	IsDone   bool
	IsActive bool
	IsNext   bool
	IsPast   bool
}

// dayRows pairs PageData.Cues (CONTRACT computed Start/End/Dur) with the
// day-bar segments (CONTRACT IsDone/IsActive at snapshot instant) — reusing
// exactly the schedule computation ShowData already ran, no second pass.
func dayRows(pd *views.PageData) []dayRowVM {
	if pd == nil {
		return nil
	}
	segs := make(map[int64]views.SegmentVM, len(pd.Schedule.Segments))
	for _, s := range pd.Schedule.Segments {
		segs[s.Pos] = s
	}
	rows := make([]dayRowVM, 0, len(pd.Cues))
	for _, c := range pd.Cues {
		seg := segs[c.Pos]
		rows = append(rows, dayRowVM{
			Pos:      c.Pos,
			Label:    c.Label,
			Speaker:  c.Speaker,
			DurFmt:   c.DurFmt,
			StartFmt: c.StartFmt,
			EndFmt:   c.EndFmt,
			HoldFmt:  c.HoldFmt,
			Color:    c.Color,
			IsBreak:  c.IsBreak,
			IsDone:   seg.IsDone,
			IsActive: seg.IsActive,
			IsNext:   c.IsNext,
			IsPast:   pd.Runtime.ActivePos > c.Pos && pd.Runtime.ActivePos != 0,
		})
	}
	return rows
}

// ---------------------------------------------------------------------------
// Query validation helpers.

// hexParam reads + validates a color override: "#rrggbb" (URL-encoded as
// %23…) or bare hex, 3 or 6 hex digits, any case. Returns the normalised
// "#hex" for the template's scoped CSS var, or "" when the param is absent.
// ALLOWED RULE (documented for screens): hex-only — names, rgba(), var(),
// url() and anything else is rejected (CSS injection surface stays closed;
// the caller answers 400 with the reason).
func hexParam(c *gin.Context, name string) (string, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return "", nil
	}
	raw = strings.TrimPrefix(raw, "#")
	h := raw
	switch len(raw) {
	case 3, 6:
		for _, r := range h {
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return "", errInvalidHex{name, raw, "hex digits only"}
			}
		}
	default:
		return "", errInvalidHex{name, raw, "3 or 6 hex digits"}
	}
	out := normaliseHex(raw)
	// Brand guardrails (Fix-3 locked + CONTRACT-UI §7): green is logo-only
	// artwork and orange is banned — reject the tokens even as operator
	// overrides so an accent mistake can never rebrand a venue screen.
	switch out {
	case "#22c55e":
		return "", errInvalidHex{name, raw, "green is logo-only (brand lock)"}
	case "#f97316", "#ff8800", "#ffa500":
		return "", errInvalidHex{name, raw, "orange is banned (brand lock)"}
	}
	return out, nil
}

// normaliseHex rebuilds the value as lowercase #rrggbb (3-digit expanded)
// so the printed CSS var is uniform regardless of caller case.
func normaliseHex(raw string) string {
	if len(raw) == 3 {
		var b strings.Builder
		b.WriteByte('#')
		for _, r := range raw {
			b.WriteRune(r)
			b.WriteRune(r)
		}
		return strings.ToLower(b.String())
	}
	return "#" + strings.ToLower(raw)
}

type errInvalidHex struct {
	name, raw, why string
}

func (e errInvalidHex) Error() string {
	return e.name + " must be #hex (" + e.why + "), got " + strconv.Quote(e.raw)
}

// fitParam is the screen-fit quirk; unknown values degrade to "contain"
// (color-style typos fail loudly, layout preferences prefer robustness).
func fitParam(c *gin.Context) string {
	switch c.Query("fit") {
	case "cover":
		return "cover"
	case "shadow":
		return "shadow"
	default:
		return "contain"
	}
}
