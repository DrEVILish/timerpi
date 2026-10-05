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
func (d *Deps) apiWaitingRegister(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	var body struct {
		Name string `json:"name"`
		Host string `json:"host"`
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
	if err := d.Store.RegisterWaiting(body.Name, body.Host); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
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
	code, screen, err := d.Store.ClaimWaiting(c.Query("name"), c.Query("host"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "assigned": code, "screen": screen})
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
	// PLAN §11.2 capture modal: the operator names the screen and sets its
	// Theme / Room(location) / Layout in one step; the config lands on the
	// screens registry BEFORE the display hops, so its first join already
	// carries theme + board assignment.
	var body struct {
		Code     string `json:"code"`
		Name     string `json:"name"`
		Theme    string `json:"theme"`
		Room     string `json:"room"`
		BoardID  int64  `json:"boardId"`
		Template string `json:"template"` // §Layout round: template drives the board
		Kind     string `json:"kind"`     // audience | walkin | presenter
		Rotation int    `json:"rotation"` // 0/90/180/270
	}
	_ = c.ShouldBindJSON(&body)
	sid, ok := timerpi.ResolveShowID(d.Store, body.Code)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown show code"})
		return
	}
	sh, err := d.Store.GetShow(sid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown show code"})
		return
	}
	if !d.canModerate(c, sid) {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "you can only capture screens into a room you moderate"})
		return
	}
	if !timerpi.ValidScreenKind(body.Kind) || !timerpi.ValidRotation(body.Rotation) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad display type or rotation"})
		return
	}
	w, err := d.Store.GetWaiting(id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"ok": false, "error": "waiting display gone"})
		return
	}
	// The operator names the screen; blank = keep the display's own name.
	name := timerpi.SanitizeScreenName(body.Name)
	if name == "" {
		name = w.Name
	}
	if !screenThemeRe.MatchString(body.Theme) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad theme name"})
		return
	}
	boardID := body.BoardID
	if tpl := strings.TrimSpace(body.Template); tpl != "" {
		bid, berr := d.screenTemplateBoard(sid, name, tpl)
		if berr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": berr.Error()})
			return
		}
		boardID = bid
	}
	if boardID < 0 || !d.boardKnown(sid, boardID) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown board"})
		return
	}
	if err := d.Store.SetScreenConfig(sid, name, body.Theme, boardID, body.Room); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if err := d.Store.SetScreenLook(sid, name, body.Kind, body.Rotation); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if err := d.Store.AssignWaiting(id, sh.Code, name); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.notifyControls(sid)
	c.JSON(http.StatusOK, gin.H{"ok": true, "code": sh.Code, "name": name})
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
