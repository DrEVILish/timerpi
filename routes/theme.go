package routes

// theme.go — the box default theme (which ftl bundle fresh browsers and
// unconfigured screens use). Setting it is a box-admin action (access.go).

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"timerpi/config"
)

// ---- B7: appliance default theme (which ftl bundle fresh browsers use) ---

// installedThemes lists the vendored ftl dist bundles by filename
// (authoritative — no hard-coded list to rot).
func installedThemes() []string {
	dir := findDir(filepath.Join("third_party", "ftl-themes", "dist"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"blue-future"}
	}
	out := []string{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".css") && name != "core.css" { // core is the base layer, not a theme
			out = append(out, strings.TrimSuffix(name, ".css"))
		}
	}
	return out
}

// themeKnown: "" (the default) or an installed theme bundle. Unknown names
// used to be saved as "" and answered ok (BUGLOG RS16).
func themeKnown(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	for _, t := range installedThemes() {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}

// registerTheme wires the appliance default theme into /settings surfaces:
// GET answers current + available bundles, POST rewrites the fallback used
// by every fresh operator browser (showFolder puts it where the person who
// just renamed a hostname sets it — device identity, not per-show).
func registerTheme(r *gin.Engine) {
	r.GET("/api/theme", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"current":  config.DefaultTheme(),
			"fallback": "blue-future",
			"themes":   installedThemes(),
		})
	})
	r.POST("/api/theme", func(c *gin.Context) {
		var body struct {
			Theme string `json:"theme"`
		}
		if strings.HasPrefix(c.ContentType(), "application/json") {
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
				return
			}
		} else {
			body.Theme = c.PostForm("theme")
		}
		if !themeKnown(body.Theme) {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "Unknown theme " + strconv.Quote(body.Theme)})
			return
		}
		if err := config.SetDefaultTheme(body.Theme); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "theme": config.DefaultTheme()})
	})
}
