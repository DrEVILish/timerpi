// super.go — the SuperOperator surface (PLAN §11.1 role ladder, phase 3).
//
// Identity: the DEVICE operator password (routes/auth.go tp_auth session).
// A browser past AuthGate is a super operator; room operators hold only
// their show's passphrase (showauth.go) and never reach this page. On an
// open appliance (no device password) the panel is reachable like every
// other operator surface — the same LAN-trust model as the rest of the
// app, documented rather than pretended away.
//
// Surface: the cross-room panel lists every show (zone-filterable) with
// live state, fires engine verbs at ONE room, and runs BULK verbs
// (blank/unblank/go/next/pause/resume) across all rooms or one zone.
package routes

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/oscbridge"
	"timerpi/timerpi"
)

// superVerbs are the verbs the panel may fire: transport + blackout, the
// safety set. Cue/message CRUD stay per-room (they are content, not
// transport, and the room operator owns them).
var superVerbs = map[string]bool{
	"go": true, "next": true, "prev": true, "pause": true,
	"resume": true, "reset": true, "blank": true, "unblank": true,
}

func registerSuperRoutes(r gin.IRouter, d *Deps) {
	r.GET("/super", d.superPage)
	r.GET("/api/super/rooms", d.apiSuperRooms)
	r.POST("/api/super/verb", d.apiSuperVerb)
	r.POST("/api/super/bulk", d.apiSuperBulk)
}

func (d *Deps) superPage(c *gin.Context) {
	if d.Store == nil {
		pageNotFound(c)
		return
	}
	d.render(c, "super", gin.H{
		"Page": "super", "DefaultTheme": config.DefaultTheme(),
	})
}

// superRoom is one room card on the panel.
type superRoom struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Zone        string `json:"zone"`
	Running     bool   `json:"running"`
	Paused      bool   `json:"paused"`
	ActiveLabel string `json:"activeLabel"`
	RemainingMS int64  `json:"remainingMS"`
	Blanked     bool   `json:"blanked"`
	Sessions    int    `json:"sessions"`
}

func (d *Deps) superRoomList(zone string) ([]superRoom, error) {
	shows, err := d.Store.ListShows()
	if err != nil {
		return nil, err
	}
	want := timerpi.SanitizeScreenName(zone)
	out := make([]superRoom, 0, len(shows))
	for _, s := range shows {
		if want != "" && timerpi.SanitizeScreenName(s.Zone) != want {
			continue
		}
		room := superRoom{Code: s.Code, Title: s.Title, Zone: s.Zone}
		if eng, err := d.engineFor(s.ID); err == nil {
			if snap, serr := eng.Snapshot(); serr == nil {
				room.Running = snap.Runtime.Running
				room.Paused = snap.Runtime.Paused
				room.RemainingMS = snap.Runtime.RemainingMS
				room.Blanked = s.Blanked
				if snap.Runtime.ActivePos > 0 {
					for _, cue := range snap.Cues {
						if cue.Pos == snap.Runtime.ActivePos {
							room.ActiveLabel = cue.Label
							break
						}
					}
				}
			}
		}
		if d.Hub != nil {
			room.Sessions = d.Hub.ShowSessions(s.ID)
		}
		out = append(out, room)
	}
	return out, nil
}

// GET /api/super/rooms?zone=Hall A — live state per room, zone-filterable.
func (d *Deps) apiSuperRooms(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	rooms, err := d.superRoomList(c.Query("zone"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "rooms": rooms})
}

// POST /api/super/verb {code, verb, pos?} — fire one engine verb at one
// room (transport verbs only; blank/unblank are store verbs like their WS
// branch). Cue-start fires the outbound media hook (onFire) exactly like
// the room operator's GO.
func (d *Deps) apiSuperVerb(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	var body struct {
		Code string `json:"code"`
		Verb string `json:"verb"`
		Pos  int64  `json:"pos"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "code required"})
		return
	}
	if !superVerbs[body.Verb] {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "verb not allowed from the super panel"})
		return
	}
	id, ok := timerpi.ResolveShowID(d.Store, body.Code)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown show code"})
		return
	}
	if err := d.superApply(id, body.Verb, body.Pos); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.logAction(id, "super", body.Verb)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/super/bulk {verb, zone?} — one verb across every room (or one
// zone's rooms). Answers with the per-room outcomes; a refused room never
// blocks the others.
func (d *Deps) apiSuperBulk(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	var body struct {
		Verb string `json:"verb"`
		Zone string `json:"zone"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	if !superVerbs[body.Verb] {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "verb not allowed from the super panel"})
		return
	}
	rooms, err := d.superRoomList(body.Zone)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	results := make([]gin.H, 0, len(rooms))
	for _, room := range rooms {
		id, ok := timerpi.ResolveShowID(d.Store, room.Code)
		if !ok {
			continue
		}
		if err := d.superApply(id, body.Verb, 0); err != nil {
			results = append(results, gin.H{"code": room.Code, "ok": false, "error": err.Error()})
			continue
		}
		d.logAction(id, "super", "bulk "+body.Verb)
		results = append(results, gin.H{"code": room.Code, "ok": true})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "results": results})
}

// superApply runs one super verb against one show (transport via the
// engine, blackout via the store — same split the WS commands use).
func (d *Deps) superApply(showID int64, verb string, pos int64) error {
	switch verb {
	case "blank":
		if err := d.Store.SetShowBlanked(showID, true); err != nil {
			return err
		}
		oscbridge.FireOut("panic", 0)
	case "unblank":
		if err := d.Store.SetShowBlanked(showID, false); err != nil {
			return err
		}
		oscbridge.FireOut("go", 0)
	default:
		eng, err := d.engineFor(showID)
		if err != nil {
			return err
		}
		args := map[string]any{}
		if pos > 0 {
			args["pos"] = pos
		}
		return eng.ApplyCmd(verb, args)
	}
	if eng, err := d.engineFor(showID); err == nil {
		_ = eng.Notify()
	}
	return nil
}
