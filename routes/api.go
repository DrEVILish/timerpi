// package routes — api.go: the JSON API (PROTOCOL §REST plus the message
// endpoints the CONTRACT-UI leaves to WS, provided as REST conveniences).
package routes

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/oscbridge"

	"timerpi/boards"
	"timerpi/timerpi"
)

// registerAPI mounts /api/... (see PROTOCOL §REST) and the su share QR.
// Public addressing is CODE-ONLY (Agent L scope change): every `:ident`
// is a share code; numeric show ids 404 (internal DB key only).
func registerAPI(r *gin.Engine, d *Deps) {
	g := r.Group("/api")

	g.GET("/shows/:ident", d.apiShowSnapshot)
	g.DELETE("/shows/:ident", d.apiDeleteShow)
	g.POST("/shows/:ident/clone", d.apiCloneShow)
	g.POST("/shows/:ident/blank", d.apiShowBlank)
	g.DELETE("/shows/:ident/sessions/:peer", d.apiSessionDelete)
	g.GET("/shows/:ident/actions", d.apiShowActions)
	g.POST("/shows/:ident/client-log", d.apiClientLog)
	g.GET("/shows/:ident/client-log", d.apiClientLogTail)
	g.GET("/shows/:ident/qr", d.apiShowQR)
	g.GET("/shows/:ident/walkin", d.apiWalkin) // event walk-in feed (open)

	// Screens + presets (F1/F2; screens.go).
	registerScreens(g, d)

	// Waiting room for orphaned displays (waiting.go).
	registerWaitingRoutes(g, d)

	// BACKWARD-CONTRACT (PROTOCOL): full cue replace/import.
	g.PUT("/shows/:ident/cues", d.apiReplaceCues)

	// Day memo (A7): gated like show content because it reads/writes it.
	g.POST("/shows/:ident/notes", d.apiShowNotes)

	// B5: remote-control aliases (webhooks / Companion / Stream Deck).
	// One endpoint per carrying-verb, engine-identical semantics.
	g.POST("/shows/:ident/cmd/:action", d.apiShowCmd)

	// B4: scheduled day start — persist "HH:MM" + anchor today to it.
	g.POST("/shows/:ident/daystart", d.apiShowDayStart)

	// B2: full-slot reorder target slot for automations (drag ships over WS).
	g.POST("/shows/:ident/moveto", d.apiMoveTo)

	// Mesh master push (browser P2P fallback; last-writer-wins).
	g.POST("/shows/:ident/sync", d.apiSync)

	// Import (multipart) + example docs — seams live in import.go.
	g.POST("/shows/:ident/import", d.apiImport)
	g.GET("/import-example", d.apiImportExample)
	g.GET("/shows/:ident/import-example", d.apiImportExampleForShow)

	// Message REST conveniences (CONTRACT-UI drives these via WS; the REST
	// shapes here mirror that command mapping).
	g.POST("/shows/:ident/messages", d.apiCreateMessage)
	g.POST("/shows/:ident/messages/:mid/show", d.apiShowMessage(true))
	g.POST("/shows/:ident/messages/:mid/hide", d.apiShowMessage(false))
	g.DELETE("/shows/:ident/messages/:mid", d.apiDeleteMessage)
}

// requireShow resolves the `:ident` param to the show's INTERNAL row id
// (Agent L scope change, code-only addressing): the ident goes through
// timerpi.ResolveShowID (NormalizeCode + `WHERE code = ?`), digits and
// misses are refused. Callers stop here on !ok — the response the client
// sees is already committed (404 shape). The engine still runs on the
// numeric id internally (registry key); JSON/responses keep `code` for
// the client-facing address.
func (d *Deps) requireShow(c *gin.Context) (int64, bool) {
	if d.Store == nil || d.Engines == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db/engine wiring missing"})
		return 0, false
	}
	id, ok := timerpi.ResolveShowID(d.Store, c.Param("ident"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown session code"})
		return 0, false
	}
	return id, true
}

// engineFor loads the registry engine for a show.
func (d *Deps) engineFor(showID int64) (*timerpi.Engine, error) {
	return d.Engines.Get(showID)
}

// ------------------------------------------------------------------ shows --

// POST /api/shows/:ident/notes {text} — the DAY-MEMO autosave endpoint (A7).
// Show-gated like content; stores verbatim (operator voice), no length cap
// beyond sanity (4000 chars: a day note, not a novel).
func (d *Deps) apiShowNotes(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if strings.HasPrefix(c.ContentType(), "application/json") {
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
			return
		}
	} else {
		body.Text = c.PostForm("text")
	}
	if len(body.Text) > 4000 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "day notes cap at 4000 characters"})
		return
	}
	if err := d.Store.SetShowNotes(id, body.Text); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "savedAt": time.Now().UnixMilli()})
}

// POST /api/shows/:ident/daystart {"hhmm":"09:00"|"20:30"|""} — B4: persist
// the scheduled day start and anchor today to it in the same breath (the
// day-bar becomes real wall-clock times immediately). Empty clears the
// automation without moving an existing anchor.
func (d *Deps) apiShowDayStart(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var body struct {
		HHMM string `json:"hhmm"`
	}
	if strings.HasPrefix(c.ContentType(), "application/json") {
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
			return
		}
	} else {
		body.HHMM = c.PostForm("hhmm")
	}
	if body.HHMM != "" && timerpi.DayStartTSFrom(body.HHMM, time.Now().UnixMilli()) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "day start must be HH:MM 00:00–23:59"})
		return
	}
	if err := d.Store.SetShowDayStart(id, body.HHMM); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	eng, err := d.engineFor(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": true}) // persisted; no engine to re-anchor (deleted?)
		return
	}
	if ts := timerpi.DayStartTSFrom(body.HHMM, time.Now().UnixMilli()); ts != 0 {
		if err := eng.ApplyCmd("daystart", map[string]any{"ts": ts}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		if serr := eng.Notify(); serr != nil {
			c.JSON(http.StatusOK, gin.H{"ok": true, "anchored": true, "notifyErr": serr.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "anchored": true, "ts": ts})
		return
	}
	if err := eng.Notify(); err != nil {
		c.JSON(http.StatusOK, gin.H{"ok": true, "notifyErr": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "anchored": false})
}

// automation (Bitfocus Companion, Stream Deck webhooks, curl one-liners).
// Actions map 1:1 onto the WS-command engine verbs (PROTOCOL §commands) so
// the semantics can never drift from the operator surface:
//
//	go | start | pause | resume | reset | next | prev | jump | rate | daystart
//
// Body is the same JSON args the WS command takes ({"pos":..},{"rate":0.9},
// {"ts":..}). Rate is clamped to the ×0.5–×2.0 contract here so a stray
// automation script cannot break the re-anchor math. Show-gated
// (passphrase) and operator-auth-gated like every mutating surface;
// OriginGuard still refuses browser-driven cross-site POSTs, while
// non-browser callers (curl, devices) pass as ever.
// POST /api/shows/:ident/moveto {"pos":N,"to":M} — B2: full-slot cue
// reposition (the drag commit's REST twin for automations). Show-gated;
// out-of-range targets clamp at the DB layer; same-slot is a friendly no-op.
func (d *Deps) apiMoveTo(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var body struct {
		Pos int64 `json:"pos"`
		To  int64 `json:"to"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {\"pos\":N,\"to\":M}"})
		return
	}
	if body.Pos <= 0 || body.To <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "pos and to are 1-based cue positions"})
		return
	}
	if err := d.Store.MoveCue(id, body.Pos, body.To); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.logAction(id, "cueMove", fmt.Sprintf("pos %d to %d", body.Pos, body.To))
	if eng, gerr := d.engineFor(id); gerr == nil {
		if nerr := eng.Notify(); nerr != nil {
			log.Printf("routes: notify after moveto: %v", nerr)
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "pos": body.Pos, "to": body.To})
}

// POST /api/shows/:ident/cmd/:action — B5: remote-control aliases for room
// automation (Bitfocus Companion, Stream Deck webhooks, curl one-liners).
// Actions map 1:1 onto the WS-command engine verbs (PROTOCOL §commands) so
// the semantics can never drift from the operator surface:
//
//	go | start | pause | resume | reset | next | prev | jump | rate | daystart
//
// Body is the same JSON args the WS command takes ({"pos":..},{"rate":0.9},
// {"ts":..}). Rate is clamped to the ×0.5–×2.0 contract here so a stray
// automation script cannot break the re-anchor math. Show-gated
// (passphrase) and operator-auth-gated like every mutating surface;
// OriginGuard still refuses browser-driven cross-site POSTs, while
// non-browser callers (curl, devices) pass as ever.
func (d *Deps) apiShowCmd(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	eng, err := d.engineFor(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such show"})
		return
	}
	action := c.Param("action")
	switch action {
	case "go", "start", "pause", "resume", "reset", "next", "prev", "jump", "rate", "daystart":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown action " + action})
		return
	}
	args := map[string]any{}
	if strings.HasPrefix(c.ContentType(), "application/json") {
		_ = c.ShouldBindJSON(&args)
	}
	if action == "rate" {
		rate, _ := args["rate"].(float64)
		if rate == 0 { // missing → default, mirrors the WS path's behavior quality
			rate = 1.0
		}
		args["rate"] = timerpi.ClampRate(rate)
	}
	if action == "jump" && argNum(args["pos"]) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "jump needs pos"})
		return
	}
	if aerr := eng.ApplyCmd(action, args); aerr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": aerr.Error()})
		return
	}
	d.logAction(id, "cmd:"+action, "")
	snap, serr := eng.Snapshot()
	if serr != nil {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "action": action, "snapshot": snap})
}

// argNum floats out any JSON bookkeeping value for the jump guard.
func argNum(v any) float64 {
	n, _ := v.(float64)
	return n
}

// GET /api/shows/:ident — the snapshot (same JSON as the WS `state` frame).
func (d *Deps) apiShowSnapshot(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	eng, err := d.engineFor(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such show"})
		return
	}
	snap, err := eng.Snapshot()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such show"})
		return
	}
	c.JSON(http.StatusOK, snap)
}

// requireSuperOfShow resolves :ident and demands a SuperOperator session
// for the room's event: creating and deleting rooms is the SuperOperator's
// job (PRODUCT §3), never a moderator's (BUGLOG RW5).
func (d *Deps) requireSuperOfShow(c *gin.Context) (int64, bool) {
	id, ok := d.requireShow(c)
	if !ok {
		return 0, false
	}
	room, err := d.Store.GetShow(id)
	if err == nil {
		if ev, eerr := d.Store.GetEvent(room.EventID); eerr == nil && d.isSuper(c, ev) {
			return id, true
		}
	}
	c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "SuperOperator sign-in required"})
	return 0, false
}

// DELETE /api/shows/:id — drop the room + its engine (SuperOperator).
func (d *Deps) apiDeleteShow(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	if err := boards.RehomeRoomLayouts(d.Store.DB, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := d.Store.DeleteShow(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	d.forgetRoom(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/shows/:ident/clone {"title"?} — duplicate the day (E1):
// fresh code, same cues/notes/day-start, no passphrase, fresh runtime.
// SuperOperator only: a clone is a new room of the event (BUGLOG RW5).
func (d *Deps) apiCloneShow(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if strings.HasPrefix(c.ContentType(), "application/json") {
		_ = c.ShouldBindJSON(&body)
	}
	dst, err := d.Store.CloneShow(id, body.Title)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cues, err := d.Store.ListCues(dst.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	d.logAction(dst.ID, "clone", dst.Title)
	code := timerpi.NormalizeCode(dst.Code)
	c.JSON(http.StatusCreated, gin.H{
		"ok":       true,
		"id":       dst.ID,
		"code":     code,
		"title":    dst.Title,
		"cueCount": len(cues),
		"control":  "/c/" + code,
		"display":  "/d/" + code,
	})
}

// POST /api/shows/:ident/blank {"on":bool} — raise/clear the global
// display blackout (E3). Show-gated; the fanned snapshot carries the flag
// so every screen flips together. Operator-auth rides the middleware.
func (d *Deps) apiShowBlank(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var body struct {
		On *bool `json:"on"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.On == nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {\"on\":bool}"})
		return
	}
	if err := d.Store.SetShowBlanked(id, *body.On); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.logAction(id, "blank", fmt.Sprintf("%v", *body.On))
	// Outbound media bridge: BLANK cuts CuTePi to its panic holding image;
	// unblank resumes the playout peer.
	if *body.On {
		oscbridge.FireOut("panic", 0)
	} else {
		oscbridge.FireOut("go", 0)
	}
	if eng, gerr := d.engineFor(id); gerr == nil {
		if nerr := eng.Notify(); nerr != nil {
			log.Printf("routes: notify after blank: %v", nerr)
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "blanked": *body.On})
}

// GET /api/shows/:ident/actions?limit=N — the E4 operator-log tail,
// newest first (default 50, max 200). Show-gated like content.
func (d *Deps) apiShowActions(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	acts, err := d.Store.ListActions(id, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "actions": acts})
}

// DELETE /api/shows/:ident/sessions/:peer — drop a live display/peer session
// ("delete session" in the gallery/panel). 404 when no such live session.
func (d *Deps) apiSessionDelete(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	if d.Hub == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "hub missing"})
		return
	}
	peer := c.Param("peer")
	if peer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "peer required"})
		return
	}
	kicked := d.Hub.KickSession(id, peer)
	if kicked == 0 {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "no such live session"})
		return
	}
	d.logAction(id, "sessionKick", peer)
	d.notifyControls(id)
	c.JSON(http.StatusOK, gin.H{"ok": true, "kicked": kicked})
}

// POST /api/shows/:ident/client-log {entries:[{kind,message,source}]} —
// browser error reports (F4: window.onerror / unhandledrejection, batched
// client-side). Show-gated (reports may quote cue labels = show content).
// Each entry is stored (capped tail) AND journaled (journal survives even
// DB trouble). At most 25 entries per request.
// clientLogLimit: error reports per client IP per minute (BUGLOG RW16). A
// healthy screen sends a handful; a loop gets 429.
var clientLogLimit = &windowLimiter{max: 30, window: time.Minute, bound: 20_000}

func (d *Deps) apiClientLog(c *gin.Context) {
	// Open: screens report errors without signing in (stored capped).
	id, ok := d.requireShow(c)
	if !ok {
		return
	}
	var body struct {
		Entries []struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
			Source  string `json:"source"`
		} `json:"entries"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Entries) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "body must be {\"entries\":[{kind,message,source}]}"})
		return
	}
	if !clientLogLimit.allow(c.ClientIP(), time.Now()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "too many reports"})
		return
	}
	n := 0
	var first string
	batch := make([]timerpi.ClientError, 0, 25)
	for _, e := range body.Entries {
		if n >= 25 {
			break
		}
		if strings.TrimSpace(e.Message) == "" {
			continue
		}
		batch = append(batch, timerpi.ClientError{Kind: e.Kind, Message: e.Message, Source: e.Source})
		if n == 0 {
			// Flatten before journalling: attacker-controlled text must not
			// forge extra journal lines (or ANSI) via CR/LF.
			first = journalSafe(e.Kind) + ": " + journalSafe(e.Message)
		}
		n++
	}
	d.Store.LogClientErrors(id, batch)
	if n > 0 {
		log.Printf("routes: %d client error(s) [show %d] first=%s", n, id, first)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "logged": n})
}

// GET /api/shows/:ident/client-log?limit=N — newest-first error tail
// (default 50, max 200). Show-gated like the reports themselves.
func (d *Deps) apiClientLogTail(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	limit := 50
	if raw := c.Query("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	errs, err := d.Store.ListClientErrors(id, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "errors": errs})
}

// --------------------------------------------------------------- cue PUT --

// PUT /api/shows/:id/cues — full cue replace/import (JSON body of cues,
// already in PROTOCOL wire shape). Digest the whole day in one go.
func (d *Deps) apiReplaceCues(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var cues []timerpi.Cue
	if err := c.ShouldBindJSON(&cues); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body must be a JSON array of cues"})
		return
	}
	if err := d.Store.ReplaceCues(id, cues); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	d.logAction(id, "cueReplace", fmt.Sprintf("%d cues", len(cues)))
	if eng, gerr := d.engineFor(id); gerr == nil {
		if nerr := eng.Notify(); nerr != nil {
			log.Printf("routes: notify after cue replace: %v", nerr)
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(cues)})
}

// ----------------------------------------------------------------- sync --

// syncPayload is the POST /api/shows/:id/sync body (PROTOCOL snapshot +
// OFFLINE-EDIT additive fields). Per-cue `updatedAt` rides the cue objects
// (timerpi.Cue.UpdatedAt); bodies that carry any per-cue stamp or tombstone
// take the per-cue merge path, older bodies keep the legacy whole-snapshot
// LWW below.
type syncPayload struct {
	UpdatedAt  int64                  `json:"updatedAt"`
	Show       timerpi.Show           `json:"show"`
	Runtime    timerpi.Runtime        `json:"runtime"`
	Cues       []timerpi.Cue          `json:"cues"`
	Messages   []timerpi.Message      `json:"messages"`
	Tombstones []timerpi.CueTombstone `json:"tombstones"`
}

// hasCueStamps reports whether the body carries offline-edit merge data:
// any per-cue updatedAt or any tombstone. Old clients send neither and stay
// on the legacy whole-snapshot path.
func (p *syncPayload) hasCueStamps() bool {
	if len(p.Tombstones) > 0 {
		return true
	}
	for _, c := range p.Cues {
		if c.UpdatedAt != 0 {
			return true
		}
	}
	return false
}

// POST /api/shows/:id/sync — mesh master push (offline master state won).
// Semantics (PROTOCOL §mesh): the snapshot's `updatedAt` dominates — a
// payload older than the server's stamp is refused (current snapshot is
// returned so the peer adopts server state); a newer one is applied:
// cues replaced, runtime + messages restored, then a fresh engine loads it
// and broadcasts.
func (d *Deps) apiSync(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var payload syncPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body must be the PROTOCOL snapshot JSON"})
		return
	}

	// Offline-edit merge (docs/OFFLINE-EDIT.md): per-cue LWW + tombstones.
	// Never stale-refused — staleness is resolved per cue — while runtime,
	// messages and title stay snapshot-LWW inside.
	if payload.hasCueStamps() {
		d.apiSyncMerge(c, id, &payload)
		return
	}

	cur := d.Store.UpdatedStamp(id)
	if payload.UpdatedAt < cur {
		// Server-side progressed beyond the offline master's memory: serve
		// the current truth so the peer converges (adopt-or-broadcast).
		eng, gerr := d.engineFor(id)
		if gerr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such show"})
			return
		}
		snap, _ := eng.Snapshot()
		c.JSON(http.StatusOK, gin.H{"ok": false, "stale": true, "snapshot": snap})
		return
	}

	if err := d.applySync(id, &payload); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Fresh engine loads the new DB state (runtime + cues) cleanly.
	d.Engines.Drop(id)
	if d.Hub != nil {
		if h, forge := d.Hub.(interface{ Reload(int64) error }); forge {
			if rerr := h.Reload(id); rerr != nil {
				log.Printf("routes: hub reload after sync: %v", rerr)
			}
		}
	}
	eng, gerr := d.engineFor(id)
	if gerr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such show"})
		return
	}
	snap, _ := eng.Snapshot()
	c.JSON(http.StatusOK, gin.H{"ok": true, "merged": false, "mergedCues": 0, "snapshot": snap})
}

// apiSyncMerge applies an offline master's diverged cue list per cue
// (timerpi.MergeCues): cues ALWAYS merge, even when the body's snapshot
// stamp predates the server (per-cue recency decides). Runtime, messages
// and title have no per-field stamps, so they follow snapshot LWW: applied
// only when the body is at least as new as the server; otherwise the
// server's newer transport/message state stands.
func (d *Deps) apiSyncMerge(c *gin.Context, id int64, p *syncPayload) {
	cur := d.Store.UpdatedStamp(id)
	serverCues, lerr := d.Store.ListCues(id)
	if lerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": lerr.Error()})
		return
	}
	merged, remote := timerpi.MergeCues(serverCues, p.Cues, p.Tombstones)
	if err := d.Store.ReplaceCuesStamped(id, merged); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if p.UpdatedAt >= cur {
		if err := d.applySyncMeta(id, p); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else if err := d.Store.TouchShow(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Fresh engine loads the merged DB state, then the existing oob/state
	// fanout carries it to every connected page (no new protocol frames).
	d.Engines.Drop(id)
	if d.Hub != nil {
		if h, forge := d.Hub.(interface{ Reload(int64) error }); forge {
			if rerr := h.Reload(id); rerr != nil {
				log.Printf("routes: hub reload after merge: %v", rerr)
			}
		}
	}
	eng, gerr := d.engineFor(id)
	if gerr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such show"})
		return
	}
	snap, _ := eng.Snapshot()
	c.JSON(http.StatusOK, gin.H{"ok": true, "merged": remote > 0, "mergedCues": remote, "snapshot": snap})
}

// applySync writes the pushed snapshot into the DB (show title, cues,
// runtime, messages). A fresh engine then re-loads everything.
func (d *Deps) applySync(id int64, p *syncPayload) error {
	if len(p.Cues) > 0 {
		// Stamped replace: zero stamps default to now, so the legacy path
		// behaves exactly like ReplaceCues while preserving stamps when
		// the merge path reuses this write.
		if rerr := d.Store.ReplaceCuesStamped(id, p.Cues); rerr != nil {
			return rerr
		}
	}
	return d.applySyncMeta(id, p)
}

// applySyncMeta writes the snapshot-LWW half of a sync (title, runtime,
// messages) plus the show stamp bump. The merge path calls it only when
// the body's snapshot stamp is at least as new as the server's.
func (d *Deps) applySyncMeta(id int64, p *syncPayload) error {
	if title := strings.TrimSpace(p.Show.Title); title != "" {
		if cur, gerr := d.Store.GetShow(id); gerr == nil && cur.Title != title {
			if rerr := d.Store.RenameShow(id, title); rerr != nil {
				return rerr
			}
		}
	}
	// Runtime: preserve the schema identity; overwrite everything else.
	if p.Runtime.ShowID == 0 || p.Runtime.ShowID == id {
		rt := p.Runtime
		rt.ShowID = id
		if rt.Rate <= 0 {
			rt.Rate = timerpi.DefaultRate
		}
		if serr := d.Store.SaveRuntime(rt); serr != nil {
			return serr
		}
	}
	// Messages: re-create the listing; restore shown state where the
	// master had overlays up (shownAt survives the round trip).
	if msgs, gerr := d.Store.ListMessages(id); gerr == nil {
		for _, m := range msgs {
			_ = d.Store.DeleteMessage(id, m.ID)
		}
	}
	for _, m := range p.Messages {
		nm, cerr := d.Store.CreateMessage(id, m.Text, m.Color)
		if cerr != nil {
			continue
		}
		if m.ShownAt > 0 {
			_ = d.Store.ShowMessage(id, nm.ID, m.ShownAt)
		}
	}
	// Bump the show stamp so future pushes compare against a fresh world.
	return d.Store.TouchShow(id)
}

// ------------------------------------------------------------ messages --

// POST /api/shows/:id/messages {text, color?, show?} → {"id":…,"ok":true}.
func (d *Deps) apiCreateMessage(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var body struct {
		Text  string `json:"text"`
		Color string `json:"color"`
		Show  bool   `json:"show"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body must be {\"text\": …}"})
		return
	}
	if body.Color != "" && !timerpi.ValidColor(body.Color) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid color"})
		return
	}
	msg, err := d.Store.CreateMessage(id, strings.TrimSpace(body.Text), body.Color)
	if err == nil && body.Show {
		err = d.Store.ShowMessage(id, msg.ID, msgNow())
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	d.logAction(id, "addMsg", body.Text)
	d.notifyShow(id)
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": msg.ID})
}

// POST /api/shows/:id/messages/:mid/show|hide.
func (d *Deps) apiShowMessage(show bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := d.requireShowGated(c)
		if !ok {
			return
		}
		mid, merr := strconv.ParseInt(c.Param("mid"), 10, 64)
		if merr != nil || mid <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "bad message id"})
			return
		}
		var err error
		if show {
			err = d.Store.ShowMessage(id, mid, msgNow())
		} else {
			err = d.Store.ClearMessage(id, mid)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		d.notifyShow(id)
		c.JSON(http.StatusOK, gin.H{"ok": true, "shown": show})
	}
}

// DELETE /api/shows/:id/messages/:mid.
func (d *Deps) apiDeleteMessage(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	mid, merr := strconv.ParseInt(c.Param("mid"), 10, 64)
	if merr != nil || mid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad message id"})
		return
	}
	if err := d.Store.DeleteMessage(id, mid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	d.notifyShow(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// notifyShow re-broadcasts after a DB-side mutation (NOTES-timerpi: any
// CRUD bypassing the engine must Notify).
func (d *Deps) notifyShow(id int64) {
	if eng, err := d.engineFor(id); err == nil {
		if nerr := eng.Notify(); nerr != nil {
			log.Printf("routes: notify show %d: %v", id, nerr)
		}
	}
}

// journalSafe flattens untrusted text for a single journal line.
func journalSafe(s string) string {
	s = timerpi.ClipUTF8(s, 200)
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
}

// logAction records one REST mutation in the per-show action log (E4).
// Actor is always "api" here — WS commands log role:peer at the hub.
func (d *Deps) logAction(id int64, action, detail string) {
	if d.Store == nil {
		return
	}
	d.Store.LogAction(id, "api", action, detail)
}

func msgNow() int64 { return time.Now().UnixMilli() }

// --------------------------------------------------------------------- qr --

// GET /api/shows/:showid/qr?data=…&size=N — PNG QR for the share/open-
// display links (skip2/go-qrcode; CONTRACT-UI frag-share calls it).
func (d *Deps) apiShowQR(c *gin.Context) {
	size, serr := strconv.Atoi(c.Query("size"))
	if serr != nil || size <= 0 {
		size = 320
	}
	if size < 64 {
		size = 64
	}
	if size > 1024 {
		size = 1024
	}
	data := c.Query("data")
	if strings.TrimSpace(data) == "" {
		c.String(http.StatusBadRequest, "qr needs a data payload")
		return
	}
	// Share-panel payloads are relative ("/d/<code>") — expand to the
	// request's absolute URL. X-Forwarded-Proto wins behind a TLS proxy
	// (openresty blocks query paths that start with "/d/" — owner-round
	// finding: the dashboard QR 403'd through the edge in that exact form).
	if strings.HasPrefix(data, "/") {
		data = requestOrigin(c) + data
	}
	if len(data) > 512 {
		c.String(http.StatusBadRequest, "qr data too long (max 512 chars)")
		return
	}
	if _, ok := d.requireShow(c); !ok {
		return
	}
	png, err := qrPNG(data, size)
	if err != nil {
		log.Printf("routes: qr: %v", err)
		c.String(http.StatusInternalServerError, "qr rendering failed")
		return
	}
	c.Header("Content-Disposition", `inline; filename="timerpi-share.png"`)
	c.Data(http.StatusOK, "image/png", png)
}

// requestOrigin is scheme://host of the request as the browser sees it
// (X-Forwarded-Proto wins behind a TLS proxy).
func requestOrigin(c *gin.Context) string {
	scheme := "http"
	if c.Request.Header.Get("X-Forwarded-Proto") == "https" ||
		(c.Request.TLS != nil && c.Request.TLS.HandshakeComplete) {
		scheme = "https"
	}
	return scheme + "://" + c.Request.Host
}
