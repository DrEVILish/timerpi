// network_update.go — the UPDATE button on the box's network page: when the
// boot-time update didn't happen (VENUE-CLOUD §14), whoever looks after the
// box can install a newer signed build now. Same rules as at boot: signed,
// never older, never while a timer runs.
//
//	GET  /api/network/update  → {current, available, source}
//	POST /api/network/update  → installs the newest build, then restarts
package routes

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// updating stops a second press from starting another install.
var updating atomic.Bool

func networkUpdater(c *gin.Context) (*NetworkDeps, bool) {
	nd := networkDeps.Load()
	if nd == nil || nd.Updater == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "updates aren't available on this machine"})
		return nil, false
	}
	if nd.Updater.Key == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "this build has no release key, so it can't update"})
		return nil, false
	}
	return nd, true
}

func networkUpdateCheck(c *gin.Context) {
	nd, ok := networkUpdater(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	src, m, found := nd.Updater.Find(ctx)
	out := gin.H{"ok": true, "current": nd.Updater.Current, "available": "", "updating": updating.Load()}
	if found {
		out["available"], out["source"] = m.Version, src
	}
	c.JSON(http.StatusOK, out)
}

func networkUpdateApply(c *gin.Context) {
	nd, ok := networkUpdater(c)
	if !ok {
		return
	}
	u := nd.Updater
	if u.Busy != nil && u.Busy() {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "a timer is running; update once it has stopped"})
		return
	}
	if !updating.CompareAndSwap(false, true) {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "an update is already being installed"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	src, m, found := u.Find(ctx)
	if !found {
		updating.Store(false)
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "no newer build found"})
		return
	}
	if err := u.Apply(ctx, src, m); err != nil {
		updating.Store(false)
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "installed": m.Version})
	if u.Restart != nil {
		// Let the answer reach the browser before the box restarts.
		go func() {
			time.Sleep(time.Second)
			u.Restart()
		}()
	}
}
