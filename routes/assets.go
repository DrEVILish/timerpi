// assets.go — venue image upload/serve (PLAN §11.2 phase 2: the `map`
// slot) + the per-zone map pointer. Uploads are operator-gated by the
// global AuthGate (POST/DELETE under /api); reads are public — display
// pages must render maps without logging in, and a picture of the floor
// plan is not a credential.
package routes

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// maxAssetBytes — a floor plan at 4 MiB is generous; SQLite blobs this
// size are cheap and bodyCeiling (8 MiB) still covers the multipart envelope.
const maxAssetBytes = 4 << 20

func registerAssetRoutes(r gin.IRouter, d *Deps) {
	g := r.Group("/api")
	g.POST("/assets", d.apiAssetUpload)
	g.DELETE("/assets/:id", d.apiAssetDelete)
	g.POST("/zone-map", d.apiZoneMap)
	// Public read (AuthGate exempts the /assets/ GET prefix).
	r.GET("/assets/:id", d.apiAssetGet)
}

// POST /api/assets — multipart image (field "file"). Mime is SNIFFED from
// the bytes (extension never decides — same rule CuTePi uses for media).
func (d *Deps) apiAssetUpload(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	fh, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "multipart file required"})
		return
	}
	if fh.Size > maxAssetBytes {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "image too large (4 MiB max)"})
		return
	}
	f, err := fh.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unreadable upload"})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAssetBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unreadable upload"})
		return
	}
	if len(data) > maxAssetBytes {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "image too large (4 MiB max)"})
		return
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "only image uploads"})
		return
	}
	a, err := d.Store.CreateAsset(fh.Filename, mime, data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": a.ID, "url": assetURL(a.ID)})
}

func assetURL(id int64) string { return "/assets/" + strconv.FormatInt(id, 10) }

// GET /assets/:id — the bytes, immutable (ids are never rewritten).
func (d *Deps) apiAssetGet(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusNotFound, gin.H{"ok": false})
		return
	}
	a, err := d.Store.GetAsset(id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"ok": false})
		return
	}
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, a.Mime, a.Bytes)
}

// DELETE /api/assets/:id — operator housekeeping.
func (d *Deps) apiAssetDelete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad asset id"})
		return
	}
	if err := d.Store.DeleteAsset(id); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /api/zone-map {zone, assetId} — point a zone label at a map image
// (assetId 0 clears). The walk-in board shows it when set.
func (d *Deps) apiZoneMap(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	var body struct {
		Zone    string `json:"zone"`
		AssetID int64  `json:"assetId"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Zone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "zone required"})
		return
	}
	if err := d.Store.SetZoneMap(body.Zone, body.AssetID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
