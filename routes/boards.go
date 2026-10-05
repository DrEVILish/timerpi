// Package routes — boards.go: the customizable display board (Agent N).
//
// Route: GET /d/:ident?view=board&board=<bid> (default board = the show's
// first board, seeded as "Main"). The ?view=board dispatch lives in
// display.go's displayVariants (extended, not replaced); the branch calls
// boardView here. REST is code-addressed (numeric show idents → 404 via
// requireShow, Agent L rule):
//
//	GET    /api/shows/:ident/boards        list (seeds the default board)
//	POST   /api/shows/:ident/boards        {name} → 201 (factory layout)
//	PUT    /api/shows/:ident/boards/:bid   {name?, layout?} → 200
//	DELETE /api/shows/:ident/boards/:bid   → 200
package routes

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"timerpi/boards"
	"timerpi/config"
	"timerpi/timerpi"
	"timerpi/views"
)

// RegisterBoards mounts the display-board REST surface (one mount line in
// routes.go's New, next to registerAPI).
func RegisterBoards(r *gin.Engine, d *Deps) {
	if d == nil {
		return
	}
	// PLAN §11.2: the Rooms display templates (Go is the single source of
	// truth; the board chrome's Event/Room/Main/DSM buttons fetch these).
	r.GET("/api/board-templates", d.apiBoardTemplates)

	g := r.Group("/api/shows/:ident")
	g.GET("/boards", d.apiBoardsList)
	g.POST("/boards", d.apiBoardsCreate)
	g.PUT("/boards/:bid", d.apiBoardsUpdate)
	g.DELETE("/boards/:bid", d.apiBoardsDelete)
}

// boardStore runs boards.Migrate against the SAME sqlite handle the timerpi
// package owns (additive table only; timerpi/db.go untouched).
func (d *Deps) boardStore() bool {
	if d.Store == nil {
		return false
	}
	return boards.Migrate(d.Store.DB) == nil
}

// boardBid parses the :bid path param (board row ids are internal ints).
func boardBid(c *gin.Context) (int64, bool) {
	bid, err := strconv.ParseInt(c.Param("bid"), 10, 64)
	if err != nil || bid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad board id"})
		return 0, false
	}
	return bid, true
}

// boardJSON is the REST shape for one board.
func boardJSON(b boards.Board) gin.H {
	return gin.H{
		"id":        b.ID,
		"name":      b.Name,
		"layout":    json.RawMessage([]byte(b.Layout)),
		"updatedAt": b.UpdatedAt,
	}
}

// GET /api/shows/:ident/boards — list (seeds the show default on first hit).
func (d *Deps) apiBoardsList(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	if !d.boardStore() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db wiring missing"})
		return
	}
	list, err := boards.ListBoards(d.Store.DB, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(list) == 0 {
		def, cerr := boards.EnsureDefaultBoard(d.Store.DB, id)
		if cerr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": cerr.Error()})
			return
		}
		list = []boards.Board{def}
	}
	out := make([]gin.H, 0, len(list))
	for _, b := range list {
		out = append(out, boardJSON(b))
	}
	c.JSON(http.StatusOK, out)
}

// POST /api/shows/:ident/boards {name} → 201 (factory layout).
func (d *Deps) apiBoardsCreate(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	if !d.boardStore() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db wiring missing"})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body must be {\"name\": …}"})
		return
	}
	b, err := boards.CreateBoard(d.Store.DB, id, body.Name, "")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, boardJSON(b))
}

// PUT /api/shows/:ident/boards/:bid {name?, layout?} → 200.
// layout is the full layout_json document (validated: unknown types and
// overlaps answer 400).
func (d *Deps) apiBoardsUpdate(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	if !d.boardStore() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db wiring missing"})
		return
	}
	bid, ok := boardBid(c)
	if !ok {
		return
	}
	var body struct {
		Name   *string         `json:"name"`
		Layout json.RawMessage `json:"layout"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body must be {\"name\"?, \"layout\"?}"})
		return
	}
	b, err := boards.GetBoard(d.Store.DB, id, bid)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such board"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if body.Name != nil {
		b, err = boards.RenameBoard(d.Store.DB, id, bid, *body.Name)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	if len(body.Layout) > 0 {
		b, err = boards.StoreLayout(d.Store.DB, id, bid, string(body.Layout))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}
	d.notifyShow(id) // fan out: editors + open boards re-adopt
	c.JSON(http.StatusOK, boardJSON(b))
}

// DELETE /api/shows/:ident/boards/:bid → 200.
func (d *Deps) apiBoardsDelete(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	if !d.boardStore() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db wiring missing"})
		return
	}
	bid, ok := boardBid(c)
	if !ok {
		return
	}
	if _, err := boards.GetBoard(d.Store.DB, id, bid); err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such board"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := boards.DeleteBoard(d.Store.DB, id, bid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Screens still pointing at the dead board would navigate locked TVs
	// to a 404 on every join push — fall them back to the show board.
	if err := d.Store.ClearScreenBoard(id, bid); err != nil {
		log.Printf("routes: clear screen board %d: %v", bid, err)
	}
	d.notifyControls(id)
	d.notifyShow(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------------------------------------------------------------------------
// Board page: GET /d/:ident?view=board&board=<bid>.
// ---------------------------------------------------------------------------

// stripParam drops one query parameter from an encoded query string.
func stripParam(raw, key string) string {
	vals := strings.FieldsFunc(raw, func(r rune) bool { return r == '&' })
	keep := make([]string, 0, len(vals))
	for _, v := range vals {
		if !strings.HasPrefix(v, key+"=") && v != key {
			keep = append(keep, v)
		}
	}
	return strings.Join(keep, "&")
}

// boardView renders display_board.html for the requested (or default) board.
func (d *Deps) boardView(c *gin.Context, showID int64, snap timerpi.Snapshot) {
	if !d.boardStore() {
		c.String(http.StatusServiceUnavailable, "board wiring missing")
		return
	}
	var board boards.Board
	if raw := strings.TrimSpace(c.Query("board")); raw != "" {
		bid, perr := strconv.ParseInt(raw, 10, 64)
		// Operator review round: a stale/deleted board id on a TV URL must
		// NEVER render a raw error page mid-event — fall back to the show's
		// default board so the room keeps showing something (the URL the
		// gallery serves always carries the resolved id).
		if board, gerr := boards.GetBoard(d.Store.DB, showID, bid); perr == nil && bid > 0 && gerr == nil {
			d.render(c, "display_board", d.boardData(c, snap, showID, board))
			return
		}
	}
	board, berr := boards.EnsureDefaultBoard(d.Store.DB, showID)
	if berr != nil {
		c.String(http.StatusInternalServerError, "board seeding failed")
		return
	}
	// The stale ?board= param must not win again: strip it from the URL the
	// page carries (join re-navigation reads location.search).
	c.Request.URL.RawQuery = stripParam(c.Request.URL.RawQuery, "board")
	d.render(c, "display_board", d.boardData(c, snap, showID, board))
}

// boardData builds the display_board dot: CONTRACT view data + board tiles
// + server-rendered initials (zero-JS readable) + join card.
func (d *Deps) boardData(c *gin.Context, snap timerpi.Snapshot, showID int64, board boards.Board) *views.BoardPage {
	now := d.now()
	pd := views.ShowData(snap, now, hostname())
	pd.Role = "display" // lands on <body data-role>
	pd.Nav = "display"
	pd.Peers = d.peerCount()
	// Boards read better with the day's first cue up front when idle (same
	// rule as the variant boards: server pre-render and first WS adopt agree).
	if pd.Next.Pos == 0 && len(pd.Cues) > 0 {
		first := pd.Cues[0]
		pd.Next = views.NextCue{Pos: first.Pos, Label: first.Label, DurFmt: first.DurFmt, StartFmt: first.StartFmt}
	}

	layout := board.Parsed()
	widgets := make([]views.BoardWidgetVM, 0, len(layout.Widgets))
	for _, w := range layout.Widgets {
		widgets = append(widgets, views.BoardWidgetVM{
			ID: w.ID, Type: w.Type, X: w.X, Y: w.Y, W: w.W, H: w.H,
			Style: views.BoardWidgetStyle(w.X, w.Y, w.W, w.H),
			Opts:  w.Opts,
		})
	}
	list, _ := boards.ListBoards(d.Store.DB, showID)
	infos := make([]views.BoardInfo, 0, len(list))
	for _, b := range list {
		infos = append(infos, views.BoardInfo{ID: b.ID, Name: b.Name})
	}

	countdownFmt, countdownState := boardCountdown(snap, now)
	return &views.BoardPage{
		PageData:       pd,
		Board:          views.BoardVM{ID: board.ID, Name: board.Name, Widgets: widgets},
		Boards:         infos,
		Join:           boardJoinOf(c, snap, board.ID),
		BoardID:        board.ID,
		NowFmt:         views.FmtTimeOfDay(now),
		CountdownFmt:   countdownFmt,
		CountdownState: countdownState,
		ProgressPct:    boardProgress(snap, now),
		DayPct:         boardDayPct(snap, now),
		RateFmt:        fmt.Sprintf("×%.2f", snap.Runtime.Rate),
		// Edit auth (NOTES-board §5.4 — the gap left open until operator auth
		// landed): ?edit=1 composes ONLY without a password set, or with proof
		// of identity. /d/ stays login-free for stage TVs, but the EDITING
		// chrome is operator furniture, not display furniture — the REST is
		// already behind A1's AuthGate, so this just stops shipping a toolbar
		// that can only ever answer 401s.
		Editable:   c.Query("edit") == "1" && boardEditAllowed(c),
		LayoutJSON: template.JS(board.Layout),
	}
}

// boardEditAllowed keeps ?edit=1 honest: free-form while no operator password
// exists, credential-checked once one does (cookie or Basic; the /d/ page
// itself never needs either, the editor does).
func boardEditAllowed(c *gin.Context) bool {
	return !config.HasAuth() || requestAuthed(c)
}

// boardJoinOf derives the share affordance off THIS request (absolute board
// URL incl. query, so the QR lands on the exact same board). Code-addressed
// (Agent L): /api qr + /c links use the show CODE.
func boardJoinOf(c *gin.Context, snap timerpi.Snapshot, bid int64) views.BoardJoinVM {
	self := *c.Request.URL
	self.Host = c.Request.Host
	self.Scheme = "http"
	if c.Request.TLS != nil {
		self.Scheme = "https"
	}
	selfURL := self.String()
	code := timerpi.NormalizeCode(snap.Show.Code)
	return views.BoardJoinVM{
		Self:    selfURL,
		QR:      "/api/shows/" + code + "/qr?data=" + url.QueryEscape(selfURL) + "&size=132",
		Control: "/c/" + code,
		Host:    hostname(),
	}
}

// boardCountdown is the zero-JS initial for the countdown tile: remaining
// text + clock state, computed with the canonical engine formula.
func boardCountdown(snap timerpi.Snapshot, now int64) (string, string) {
	rt := snap.Runtime
	if rt.ActivePos == 0 {
		return "--:--", "idle"
	}
	var active *timerpi.Cue
	for i := range snap.Cues {
		if snap.Cues[i].Pos == rt.ActivePos {
			active = &snap.Cues[i]
			break
		}
	}
	if active == nil {
		return "--:--", "idle"
	}
	stored := timerpi.Runtime{
		ShowID: snap.Show.ID, ActivePos: rt.ActivePos, PrevPos: rt.PrevPos,
		NextPos: rt.NextPos, Paused: rt.Paused, Running: rt.Running,
		EndAction: rt.EndAction, AnchorTS: rt.AnchorTS, Rate: rt.Rate,
		PausedElapsedMS: rt.PausedElapsedMS, DayStartTS: rt.DayStartTS,
	}
	rem, overtime, alert := timerpi.DisplayedRemaining(active, stored, now)
	switch {
	case active.TimerKind == timerpi.TimerClock:
		return views.FmtTimeOfDay(now), "running"
	case active.TimerKind == timerpi.TimerCountStop:
		return views.FmtDur(rem), "running"
	case !rt.Running && !rt.Paused && stored.PausedElapsedMS >= active.DurationMS && active.DurationMS > 0:
		if active.EndAction == timerpi.EndBlank || rt.Blank {
			return "—", "blank"
		}
		return views.FmtDur(0), "held"
	case rt.Blank:
		return "—", "blank"
	case rt.Paused:
		return views.FmtDur(rem), "paused"
	case overtime:
		return "+" + views.FmtDur(-rem), "overtime"
	case alert == 2:
		return views.FmtDur(rem), "alert2"
	case alert == 1:
		return views.FmtDur(rem), "alert1"
	case rt.Running:
		return views.FmtDur(rem), "running"
	default:
		return views.FmtDur(active.DurationMS), "armed"
	}
}

// boardProgress is the zero-JS initial cue-progress width ("42.50%").
func boardProgress(snap timerpi.Snapshot, now int64) string {
	rt := snap.Runtime
	var active *timerpi.Cue
	for i := range snap.Cues {
		if snap.Cues[i].Pos == rt.ActivePos {
			active = &snap.Cues[i]
			break
		}
	}
	if active == nil || active.DurationMS <= 0 {
		return "0.00%"
	}
	rate := rt.Rate
	if rate <= 0 {
		rate = timerpi.DefaultRate
	}
	elapsed := rt.PausedElapsedMS
	if rt.Running && !rt.Paused && rt.AnchorTS > 0 {
		elapsed += int64(float64(now-rt.AnchorTS) * rate)
	}
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > active.DurationMS {
		elapsed = active.DurationMS
	}
	return fmt.Sprintf("%.2f%%", float64(elapsed)/float64(active.DurationMS)*100)
}

// boardDayPct is the zero-JS initial day-progress width.
func boardDayPct(snap timerpi.Snapshot, now int64) string {
	sched := timerpi.ComputeScheduleRuntime(snap.Cues, boardRuntimeOf(snap), now)
	if sched.TotalMS <= 0 || sched.DayStartTS <= 0 {
		return "0.00%"
	}
	rel := now - sched.DayStartTS
	if rel < 0 {
		rel = 0
	}
	if rel > sched.TotalMS {
		rel = sched.TotalMS
	}
	return fmt.Sprintf("%.2f%%", float64(rel)/float64(sched.TotalMS)*100)
}

// boardRuntimeOf extracts the stored Runtime half of a RuntimeView
// (schedule math takes the struct, not the view).
func boardRuntimeOf(snap timerpi.Snapshot) timerpi.Runtime {
	rtv := snap.Runtime
	return timerpi.Runtime{
		ShowID: snap.Show.ID, ActivePos: rtv.ActivePos, PrevPos: rtv.PrevPos,
		NextPos: rtv.NextPos, Paused: rtv.Paused, Running: rtv.Running,
		EndAction: rtv.EndAction, AnchorTS: rtv.AnchorTS, Rate: rtv.Rate,
		PausedElapsedMS: rtv.PausedElapsedMS, DayStartTS: rtv.DayStartTS,
	}
}

// GET /api/board-templates — the named Rooms layouts (event/room/main/dsm).
func (d *Deps) apiBoardTemplates(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "templates": boards.TemplateLayouts()})
}
