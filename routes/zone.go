// zone.go — the retired zone walk-in (STATUS C10, BUGLOG RW7).
//
// Zones were a free-text label grouping rooms across the whole box; the
// event replaced them (PRODUCT §7, 2026-10-05) and the live event walk-in
// (walkin.go) replaced /zone/<name>. The old page was public and listed
// every matching room on the box with its code, across events, so it is
// gone: old links land on the home page. Bundles still carry a legacy zone
// label and map (showfile.go) for round-trips.
package routes

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func registerZoneRoutes(r gin.IRouter, d *Deps) {
	r.GET("/zone/:name", func(c *gin.Context) { c.Redirect(http.StatusFound, "/") })
}
