// waiting.go — the waiting room for orphaned displays (gallery batch).
//
// A display whose show vanished (deleted / never existed) lands in the
// mesh's badshow state; it registers itself here, shows its logo
// full-screen with "waiting for connection…", and polls. Any operator
// browser can then LIST the waiting displays and CAPTURE one into a live
// show — the claim makes the display navigate to /d/<code>?screen=<name>,
// keeping its identity (so theme/board assignments follow it).
package routes

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/timerpi"
	"timerpi/views"
)

// registerWaitingRoutes mounts the /api/waiting surface. The two display
// paths (register/mine) are AuthGate-exempt (a stage TV never logs in);
// operator paths stay gated.
func registerWaitingRoutes(g *gin.RouterGroup, d *Deps) {
	g.POST("/waiting/register", d.apiWaitingRegister)
	g.GET("/waiting/mine", d.apiWaitingMine)
	g.GET("/waiting", d.apiWaitingList)
	g.POST("/waiting/:id/capture", d.apiWaitingCapture)
	g.DELETE("/waiting/:id", d.apiWaitingDelete)
}

// POST /api/waiting/register {name, host}
// waitingLimit: registrations per client IP per minute (BUGLOG RW16). A
// waiting screen registers once per page load, so this only bites a loop.
var waitingLimit = &windowLimiter{max: 30, window: time.Minute, bound: 20_000}

func (d *Deps) apiWaitingRegister(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	var body struct {
		Name  string `json:"name"`
		Host  string `json:"host"`
		Token string `json:"token"`
		Code  string `json:"code"` // a box's pairing code (VENUE-CLOUD §4)
	}
	_ = c.ShouldBindJSON(&body) // form fallback below keeps curl honest
	if body.Name == "" {
		body.Name = c.PostForm("name")
		body.Host = c.PostForm("host")
	}
	if timerpi.SanitizeScreenName(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "screen name required"})
		return
	}
	if !waitingLimit.allow(c.ClientIP(), time.Now()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "too many screens registering from this address"})
		return
	}
	register := func() error { return d.Store.RegisterWaitingToken(body.Name, body.Host, body.Token) }
	if body.Code != "" {
		if body.Token == "" || !timerpi.ValidPairCode(body.Code) {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "a box registers with a token and a 6-digit code"})
			return
		}
		register = func() error { return d.Store.RegisterBoxWaiting(body.Name, body.Host, body.Token, body.Code) }
	}
	if err := register(); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, timerpi.ErrWaitingFull) {
			status = http.StatusTooManyRequests
		}
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/waiting/mine?name=…&host=… — poll for a capture; the assignment
// is consumed exactly once.
func (d *Deps) apiWaitingMine(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	if timerpi.SanitizeScreenName(c.Query("name")) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "name required"})
		return
	}
	code, screen, isBox, err := d.Store.ClaimWaitingBox(c.Query("name"), c.Query("host"), c.Query("token"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	// The captured screen's key travels with the hop (BUGLOG RW9): only
	// keyed screens receive operator content.
	key := ""
	if code != "" && screen != "" {
		if sid, ok := timerpi.ResolveShowID(d.Store, code); ok {
			key, _ = d.Store.ScreenKey(sid, screen)
		}
	}
	out := gin.H{"ok": true, "assigned": code, "screen": screen, "key": key}
	if isBox && code != "" {
		// A paired box also gets its event: the mesh key signs its
		// announcements and the cloud link (VENUE-CLOUD §4–§5).
		if p, ok := d.boxPairing(code); ok {
			out["pairing"] = p
		}
	}
	c.JSON(http.StatusOK, out)
}

// waitingJSON is the operator list shape.
func waitingJSON(ws []timerpi.WaitingScreen) []gin.H {
	out := make([]gin.H, 0, len(ws))
	for _, w := range ws {
		out = append(out, gin.H{"id": w.ID, "name": w.Name, "host": w.Host, "lastSeen": w.LastSeen,
			"seenAgo": views.FmtAgo(w.LastSeen)})
	}
	return out
}

// GET /api/waiting — operator list of displays awaiting a show.
func (d *Deps) apiWaitingList(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	ws, err := d.Store.ListWaiting()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "waiting": waitingJSON(ws)})
}

// POST /api/waiting/:id/capture {code} — assign a waiting display to a show.
func (d *Deps) apiWaitingCapture(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad waiting id"})
		return
	}
	var body captureBody
	_ = c.ShouldBindJSON(&body)
	code, name, ok := d.captureWaiting(c, id, body)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "code": code, "name": name})
}

// captureBody is the capture modal: the operator names the screen and sets
// its theme, location, layout, display type and rotation in one step.
type captureBody struct {
	Code     string `json:"code"` // the room's code
	Name     string `json:"name"`
	Theme    string `json:"theme"`
	Room     string `json:"room"` // the screen's location label
	BoardID  int64  `json:"boardId"`
	Template string `json:"template"` // §Layout round: template drives the board
	Kind     string `json:"kind"`     // audience | walkin | presenter
	Rotation int    `json:"rotation"` // 0/90/180/270
}

// captureWaiting assigns waiting row id to a room with the modal's
// settings. It answers the error itself and returns ok=false on failure.
func (d *Deps) captureWaiting(c *gin.Context, id int64, body captureBody) (string, string, bool) {
	// PLAN §11.2 capture modal: the config lands on the screens registry
	// BEFORE the display hops, so its first join already carries theme +
	// board assignment.
	sid, ok := timerpi.ResolveShowID(d.Store, body.Code)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown show code"})
		return "", "", false
	}
	sh, err := d.Store.GetShow(sid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown show code"})
		return "", "", false
	}
	if !d.superOfShow(c, sid) { // screens: SuperOperator only (STATUS U25)
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "only the Event Technician sets up screens"})
		return "", "", false
	}
	if !timerpi.ValidScreenKind(body.Kind) || !timerpi.ValidRotation(body.Rotation) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad display type or rotation"})
		return "", "", false
	}
	w, err := d.Store.GetWaiting(id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"ok": false, "error": "waiting display gone"})
		return "", "", false
	}
	// The operator names the screen; blank = keep the display's own name.
	name := timerpi.SanitizeScreenName(body.Name)
	if name == "" {
		name = w.Name
	}
	if !screenThemeRe.MatchString(body.Theme) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad theme name"})
		return "", "", false
	}
	boardID := body.BoardID
	tplKey := ""
	if tpl := strings.TrimSpace(body.Template); tpl != "" {
		k, known := templateKey(tpl)
		if !known {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown template"})
			return "", "", false
		}
		tplKey, boardID = k, 0 // the screen shows the built-in directly (U10)
	}
	if boardID < 0 || !d.boardKnown(sid, boardID) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown board"})
		return "", "", false
	}
	// Take the waiting row first (RW38): a second operator capturing the
	// same screen loses here, before writing any config of its own.
	if err := d.Store.AssignWaiting(id, sh.Code, name); err != nil {
		status := http.StatusInternalServerError
		msg := err.Error()
		if errors.Is(err, sql.ErrNoRows) {
			status, msg = http.StatusConflict, "someone else just set this screen up"
		}
		c.JSON(status, gin.H{"ok": false, "error": msg})
		return "", "", false
	}
	if err := d.Store.SetScreenConfig(sid, name, body.Theme, boardID, body.Room); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return "", "", false
	}
	if err := d.Store.SetScreenLook(sid, name, body.Kind, body.Rotation); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return "", "", false
	}
	if tplKey != "" {
		if err := d.Store.SetScreenTemplate(sid, name, tplKey); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return "", "", false
		}
	}
	d.notifyControls(sid)
	return sh.Code, name, true
}

// DELETE /api/waiting/:id — dismiss a waiting row (the display keeps
// waiting; it re-registers on its next poll).
func (d *Deps) apiWaitingDelete(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad waiting id"})
		return
	}
	if err := d.Store.DeleteWaiting(id); err != nil {
		status := http.StatusInternalServerError
		if err == sql.ErrNoRows {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
