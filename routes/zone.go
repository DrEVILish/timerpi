// zone.go — event zones (proposal #3): shows group by a zone label
// ("Hall A"); /zone/<name> is the walk-in/event display: wall clock, one
// card per room (current session + next session time) and the room's full
// day schedule (from the per-show ComputeSchedule). Server-rendered with a
// soft client-side refresh loop — walk-in displays don't join the WS hub
// (they span shows).
package routes

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/timerpi"
)

func registerZoneRoutes(r gin.IRouter, d *Deps) {
	r.POST("/api/shows/:ident/zone", d.apiShowZone)
	r.GET("/zone/:name", d.zonePage)
}

// POST /api/shows/:ident/zone {"zone":"Hall A"} — set/clear the zone label
// (empty clears). Show-gated; fanned to boards so the zone chip moves.
func (d *Deps) apiShowZone(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var body struct {
		Zone string `json:"zone"`
	}
	_ = c.ShouldBindJSON(&body)
	if err := d.Store.SetShowZone(id, body.Zone); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	zone := timerpi.SanitizeScreenName(body.Zone)
	d.logAction(id, "zone", fmt.Sprintf("%q", zone))
	if eng, err := d.engineFor(id); err == nil {
		if nerr := eng.Notify(); nerr != nil {
			log.Printf("routes: notify after zone: %v", nerr)
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "zone": zone})
}

// zoneRoom is one event-board card.
type zoneRoom struct {
	Code    string      `json:"code"`
	Title   string      `json:"title"`
	Now     *zoneSesst  `json:"now,omitempty"`
	Next    *zoneNextVM `json:"next,omitempty"`
	Sched   []zoneRowVM `json:"sched"`
	Started bool        `json:"started"`
}

type zoneSesst struct {
	Label   string `json:"label"`
	Speaker string `json:"speaker"`
	Running bool   `json:"running"`
	LeftMS  int64  `json:"leftMS"`
	Started bool   `json:"started"`
}

type zoneNextVM struct {
	Label   string `json:"label"`
	StartHM string `json:"startHM"`
}

type zoneRowVM struct {
	StartHM string `json:"startHM"`
	EndHM   string `json:"endHM"`
	Label   string `json:"label"`
	Speaker string `json:"speaker"`
	Break   bool   `json:"break"`
	Now     bool   `json:"now"`
	Done    bool   `json:"done"`
}

// zoneVM is the whole walk-in page's server data; the template loops rooms.
type zoneVM struct {
	Zone    string     `json:"zone"`
	NowHM   string     `json:"nowHM"`
	Rooms   []zoneRoom `json:"rooms"`
	Updated string     `json:"updated"`
}

func hmOf(ts int64) string {
	return time.UnixMilli(ts).Format("15:04")
}

func (d *Deps) zonePage(c *gin.Context) {
	if d.Store == nil {
		pageNotFound(c)
		return
	}
	name := timerpi.SanitizeScreenName(c.Param("name"))
	shows, err := d.Store.ListShows()
	if err != nil {
		pageError(c, err)
		return
	}
	zsh := make([]timerpi.Show, 0, 4)
	for _, s := range shows {
		if timerpi.SanitizeScreenName(s.Zone) == name {
			zsh = append(zsh, s)
		}
	}
	vm := zoneVM{Zone: name, NowHM: time.Now().Format("15:04:05"),
		Updated: time.Now().Format("15:04:05")}
	now := d.now()
	for _, s := range zsh {
		room := zoneRoom{Code: s.Code, Title: s.Title, Sched: []zoneRowVM{}}
		eng, engErr := d.engineFor(s.ID)
		if engErr != nil {
			log.Printf("routes: zone engine %s: %v", s.Code, engErr)
		} else {
			snap, serr := eng.Snapshot()
			if serr != nil {
				pageError(c, serr)
				return
			}
			sched := timerpi.ComputeSchedule(snap.Cues, snap.Runtime.DayStartTS, snap.Runtime.Rate)
			room.Started = snap.Runtime.DayStartTS > 0
			for i := range sched.Rows {
				r := sched.Rows[i]
				row := zoneRowVM{StartHM: hmOf(r.StartTS), EndHM: hmOf(r.EndTS),
					Label: r.Label, Speaker: r.Speaker, Break: r.Break,
					Now: r.StartTS <= now && now < r.EndTS, Done: r.EndTS <= now}
				room.Sched = append(room.Sched, row)
				if r.StartTS <= now && now < r.EndTS {
					room.Now = &zoneSesst{Label: r.Label, Speaker: r.Speaker, Running: true}
				} else if room.Next == nil && r.StartTS > now {
					room.Next = &zoneNextVM{Label: r.Label, StartHM: hmOf(r.StartTS)}
				}
			}
		}
		vm.Rooms = append(vm.Rooms, room)
	}
	c.Header("Cache-Control", "no-cache")
	d.render(c, "zone", gin.H{"Data": vm, "Page": "zone"})
}
