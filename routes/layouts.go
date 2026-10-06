package routes

// layouts.go — making a named event layout from a built-in (or another
// layout) and pointing screens at it (STATUS U11, PRODUCT 2026-10-06).
//
//	GET  /api/shows/:ident/layout-targets?kind=walkin
//	     screens of that display type in every room of the event the caller
//	     can moderate, grouped by room (SuperOperator: all rooms)
//	POST /api/shows/:ident/layouts {name, template|fromBoard, screens:[{room,name}]}
//	     → {ok, boardId}: the new event layout; the listed screens use it

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"timerpi/boards"
	"timerpi/timerpi"
)

func registerLayoutRoutes(r gin.IRouter, d *Deps) {
	r.GET("/api/shows/:ident/layout-targets", d.apiLayoutTargets)
	r.POST("/api/shows/:ident/layouts", d.apiLayoutCopy)
}

type layoutTargetScreen struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	BoardID  int64  `json:"boardId"`
	Template string `json:"template"`
}

type layoutTargetRoom struct {
	Room    string               `json:"room"`
	Name    string               `json:"name"`
	Here    bool                 `json:"here"`
	Screens []layoutTargetScreen `json:"screens"`
}

// eventRoomsFor lists the rooms of showID's event the caller may moderate.
func (d *Deps) eventRoomsFor(c *gin.Context, showID int64) []timerpi.Show {
	here, err := d.Store.GetShow(showID)
	if err != nil {
		return nil
	}
	rooms, err := d.Store.ListRooms(here.EventID)
	if err != nil || here.EventID == 0 {
		return []timerpi.Show{here}
	}
	out := make([]timerpi.Show, 0, len(rooms))
	for _, r := range rooms {
		if r.ID == showID || d.canModerate(c, r.ID) {
			out = append(out, r)
		}
	}
	return out
}

func (d *Deps) apiLayoutTargets(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	kind := strings.TrimSpace(c.Query("kind"))
	out := []layoutTargetRoom{}
	for _, r := range d.eventRoomsFor(c, id) {
		list, err := d.Store.ListScreens(r.ID)
		if err != nil {
			continue
		}
		tr := layoutTargetRoom{Room: r.Code, Name: r.Title, Here: r.ID == id, Screens: []layoutTargetScreen{}}
		for _, s := range list {
			if kind != "" && s.Kind != kind {
				continue
			}
			tr.Screens = append(tr.Screens, layoutTargetScreen{Name: s.Name, Kind: s.Kind, BoardID: s.BoardID, Template: s.Template})
		}
		if len(tr.Screens) > 0 || tr.Here {
			out = append(out, tr)
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "rooms": out})
}

func (d *Deps) apiLayoutCopy(c *gin.Context) {
	id, ok := d.requireSuperOfShow(c)
	if !ok {
		return
	}
	var body struct {
		Name      string `json:"name"`
		Template  string `json:"template"`
		FromBoard int64  `json:"fromBoard"`
		Screens   []struct {
			Room string `json:"room"`
			Name string `json:"name"`
		} `json:"screens"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Give the new layout a name"})
		return
	}
	var raw string
	switch {
	case body.Template != "":
		key, known := templateKey(body.Template)
		if !known {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown template"})
			return
		}
		b, _ := json.Marshal(boards.TemplateLayouts()[key])
		raw = string(b)
	case body.FromBoard > 0:
		src, err := boards.GetBoard(d.Store.DB, id, body.FromBoard)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown layout"})
			return
		}
		raw = src.Layout
	}
	// Resolve every target before writing anything: each screen's room must
	// be one the caller moderates in this event.
	allowed := map[string]timerpi.Show{}
	for _, r := range d.eventRoomsFor(c, id) {
		allowed[r.Code] = r
	}
	type target struct {
		room timerpi.Show
		name string
	}
	var targets []target
	for _, s := range body.Screens {
		r, ok := allowed[timerpi.NormalizeCode(s.Room)]
		name := timerpi.SanitizeScreenName(s.Name)
		if !ok || name == "" {
			c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "you can only switch screens of rooms you moderate in this event"})
			return
		}
		targets = append(targets, target{r, name})
	}
	b, err := boards.CreateBoard(d.Store.DB, id, name, raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	touched := map[int64]bool{id: true}
	for _, t := range targets {
		cur, _ := d.Store.GetScreenByName(t.room.ID, t.name)
		if err := d.Store.SetScreenConfig(t.room.ID, t.name, cur.Theme, b.ID, cur.Room); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		d.pushScreen(t.room.ID, t.name)
		touched[t.room.ID] = true
	}
	for rid := range touched {
		d.notifyControls(rid)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "boardId": b.ID, "name": b.Name})
}
