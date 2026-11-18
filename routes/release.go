// release.go — events end (VENUE-CLOUD §3, STATUS N12). Four hours after
// an event's end its screens are released: their settings and keys are
// forgotten and open screens go back to the ready card, waiting to be set
// up again. Release waits until every started cue has stopped, including pauses.
// Deleting an event releases everything at once (DeleteEvent drops the
// screens with the rooms).
//
//	GET /api/pairing/status?event=CODE — open; boxes poll it to learn their
//	    event was released or deleted ({exists, released, endsAt, now}).
package routes

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/timerpi"
)

// releaseNow is the clock the releaser reads (tests move it).
var releaseNow = time.Now

// ReleaseDue releases every event whose end + 4 h has passed, unless one
// of its rooms has an active cue, running or paused. It returns the released events.
func (d *Deps) ReleaseDue() []timerpi.Event {
	if d.Store == nil {
		return nil
	}
	now := releaseNow()
	due, err := d.Store.EventsDue(now)
	if err != nil {
		log.Printf("routes: release check: %v", err)
		return nil
	}
	var out []timerpi.Event
	for _, ev := range due {
		if ev.Home == "venue" {
			continue // the venue's primary releases it and reports back
		}
		if d.eventRunning(ev.ID) {
			continue
		}
		if _, err := d.memberWriteError(); err != nil {
			break // paired replicas and electing boxes wait for the primary
		}
		finalPending := d.OnRelease != nil && d.BoxEvent != nil && ev.Code == d.BoxEvent()
		gone, err := d.Store.ReleaseEventScreens(ev.ID, now, finalPending)
		if err != nil {
			log.Printf("routes: release %s: %v", ev.Code, err)
			continue
		}
		if gone == nil {
			continue
		}
		for sid, names := range gone {
			if d.Hub != nil {
				peers := d.Hub.ScreenPeers(sid)
				for _, n := range names {
					for _, pr := range peers[n] {
						d.Hub.KickSession(sid, pr[0])
					}
				}
			}
			d.notifyControls(sid)
		}
		log.Printf("routes: event %s ended: released its screens", ev.Code)
		if d.OnRelease != nil {
			d.OnRelease(ev)
		}
		out = append(out, ev)
	}
	return out
}

// eventRunning includes paused cues, which are still eligible to resume.
func (d *Deps) eventRunning(eventID int64) bool {
	if d.Engines == nil {
		return false
	}
	for _, id := range d.eventRoomIDs(eventID) {
		if eng, err := d.Engines.Get(id); err == nil {
			if eng.Runtime().Running {
				return true
			}
		}
	}
	return false
}

// StartReleaser checks once a minute until ctx ends.
func (d *Deps) StartReleaser(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			d.ReleaseDue()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func registerPairingStatus(r gin.IRouter, d *Deps) {
	r.GET("/api/pairing/status", func(c *gin.Context) {
		out := gin.H{"ok": true, "now": time.Now().UnixMilli(), "exists": false}
		if ev, ok := d.Store.ResolveEvent(strings.TrimSpace(c.Query("event"))); ok {
			out["exists"] = true
			out["released"] = ev.ReleasedAt > 0
			out["endsAt"] = ev.EndsAt
		}
		c.JSON(http.StatusOK, out)
	})
}
