// pairing.go — pairing a box to an event (VENUE-CLOUD §4, STATUS N13).
//
// An unpaired box shows a 6-digit code on its own screen and registers it
// in the waiting room of the cloud and of any primary box it can see. The
// Event Technician types the code on the event page: that captures the
// box's waiting row into a room (like the Screens capture) and the box's
// next poll of /api/waiting/mine carries its event, the event mesh key and
// the event end.
//
//	POST /api/events/:code/pair {pairCode, code (room), name, theme, room,
//	     template, kind, rotation}   Event Technician
package routes

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/timerpi"
)

// pairLimit: pairing attempts per event per minute (a code is 6 digits).
var pairLimit = &windowLimiter{max: 10, window: time.Minute, bound: 10_000}

// boxPairing is what a newly paired box receives with its capture.
func (d *Deps) boxPairing(roomCode string) (gin.H, bool) {
	sid, ok := timerpi.ResolveShowID(d.Store, roomCode)
	if !ok {
		return nil, false
	}
	sh, err := d.Store.GetShow(sid)
	if err != nil {
		return nil, false
	}
	ev, err := d.Store.GetEvent(sh.EventID)
	if err != nil {
		return nil, false
	}
	key, err := d.Store.EventMeshKey(ev.ID)
	if err != nil {
		return nil, false
	}
	return gin.H{"event": ev.Code, "eventName": ev.Name, "meshKey": key, "endsAt": ev.EndsAt, "room": sh.Code}, true
}

func (d *Deps) apiEventPair(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	var body struct {
		captureBody
		PairCode string `json:"pairCode"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	if !pairLimit.allow(ev.Code, time.Now()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "Too many tries — wait a minute"})
		return
	}
	code := strings.ReplaceAll(strings.TrimSpace(body.PairCode), " ", "")
	room, ok := d.Store.ResolveShowInEvent(ev.ID, body.Code)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Pick a room of this event"})
		return
	}
	w, err := d.Store.WaitingByPairCode(code)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "No box is showing that code. Check the code on the box's screen (it changes every 10 minutes)."})
		return
	}
	body.Code = room.Code
	if _, name, ok := d.captureWaiting(c, w.ID, body.captureBody); ok {
		c.JSON(http.StatusOK, gin.H{"ok": true, "name": name, "room": room.Title})
	}
}

// registerBoxScreen mounts the box's own screen (the kiosk opens /d/box):
// its pairing code while it waits, else its screen on the event's primary
// box in a full-screen frame that follows takeovers and re-pairing.
func registerBoxScreen(r gin.IRouter, d *Deps) {
	r.GET("/d/box", func(c *gin.Context) { d.render(c, "dbox", gin.H{"DefaultTheme": config.DefaultTheme()}) })
	r.GET("/api/pairing/self", func(c *gin.Context) {
		// The code proves someone stands at this box: only the box's own
		// browser reads it, never the LAN.
		host, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "only this box's own screen"})
			return
		}
		if d.BoxSelf == nil {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "not a box"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "self": d.BoxSelf()})
	})
}
