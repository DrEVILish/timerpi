// update.go — every TimerPi serves its builds to other boxes for the
// boot-time updater (update package, VENUE-CLOUD §14). Open: the builds
// are signed and boxes verify them, whoever serves them.
//
//	GET /api/update/manifest?arch=arm64  — the signed manifest
//	GET /api/update/binary?arch=arm64    — the binary it describes
package routes

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/update"
)

// selfBinary is the running executable (a var so tests can point it).
var selfBinary = func() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

func registerUpdate(r gin.IRouter) {
	r.GET("/api/update/manifest", func(c *gin.Context) {
		_, m, err := update.Build(config.DataDir(), selfBinary(), c.Query("arch"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "no signed build for this architecture"})
			return
		}
		c.JSON(http.StatusOK, m)
	})
	r.GET("/api/update/binary", func(c *gin.Context) {
		bin, _, err := update.Build(config.DataDir(), selfBinary(), c.Query("arch"))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "no signed build for this architecture"})
			return
		}
		c.Header("Content-Type", "application/octet-stream")
		c.File(bin)
	})
}
