// assets.go — venue image upload/serve (PLAN §11.2 phase 2: the `map`
// slot). Every image belongs to an event (BUGLOG RW8): uploads and the
// picker name their scope (?event=CODE for the SuperOperator, ?room=CODE
// for a moderator) and see only that event's images plus legacy unowned
// ones; deleting needs the owning event's SuperOperator (legacy: box
// admin). Reads are public — display pages must render maps without
// logging in, and a picture of the floor plan is not a credential.
package routes

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"timerpi/timerpi"
)

// maxAssetBytes — a floor plan at 4 MiB is generous; SQLite blobs this
// size are cheap and bodyCeiling (8 MiB) still covers the multipart envelope.
const maxAssetBytes = 4 << 20

func registerAssetRoutes(r gin.IRouter, d *Deps) {
	g := r.Group("/api")
	g.POST("/assets", d.apiAssetUpload)
	g.GET("/assets", d.apiAssetList)
	g.DELETE("/assets/:id", d.apiAssetDelete)
	// Public read (AuthGate exempts the /assets/ GET prefix).
	r.GET("/assets/:id", d.apiAssetGet)
}

// assetScope resolves ?event=CODE (SuperOperator) or ?room=CODE
// (moderator of that room) to the event whose images the request may use.
// It writes the error itself.
func (d *Deps) assetScope(c *gin.Context) (int64, bool) {
	if code := c.Query("event"); code != "" {
		if ev, ok := d.Store.ResolveEvent(code); ok && d.isSuper(c, ev) {
			return ev.ID, true
		}
	} else if code := c.Query("room"); code != "" {
		if id, ok := timerpi.ResolveShowID(d.Store, code); ok && d.canModerate(c, id) {
			if room, err := d.Store.GetShow(id); err == nil {
				return room.EventID, true
			}
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "name the event (?event=) or room (?room=)"})
		return 0, false
	}
	c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "operator sign-in required"})
	return 0, false
}

// GET /api/assets?event=|room= — {id,name} pairs for the picker (map
// config selects an asset instead of typing an id).
func (d *Deps) apiAssetList(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	evID, ok := d.assetScope(c)
	if !ok {
		return
	}
	list, err := d.Store.ListAssets(evID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "assets": list})
}

// POST /api/assets — multipart image (field "file"). Mime is SNIFFED from
// the bytes (extension never decides — same rule CuTePi uses for media).
func (d *Deps) apiAssetUpload(c *gin.Context) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return
	}
	evID, ok := d.assetScope(c)
	if !ok {
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
	mime, isImg := sniffImage(data)
	if !isImg {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "only image uploads"})
		return
	}
	a, err := d.Store.CreateAsset(evID, fh.Filename, mime, data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": a.ID, "url": assetURL(a.ID)})
}

func assetURL(id int64) string { return "/assets/" + strconv.FormatInt(id, 10) }

// sniffImage returns the image type sniffed from the bytes. Every way into
// the asset store (upload, bundle import) goes through it: a declared type
// is never trusted (BUGLOG RC4). SVG never sniffs as an image, so script
// inside one can't get in either.
func sniffImage(data []byte) (string, bool) {
	if len(data) == 0 || len(data) > maxAssetBytes {
		return "", false
	}
	mime := http.DetectContentType(data)
	return mime, strings.HasPrefix(mime, "image/")
}

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
	// Never let the browser treat an asset as a page (BUGLOG RC4): rows
	// stored before the import fix may carry any declared type, so the
	// type is re-checked on the way out too.
	mime := a.Mime
	if _, ok := sniffImage(a.Bytes); !ok || !strings.HasPrefix(mime, "image/") {
		mime = "application/octet-stream"
		c.Header("Content-Disposition", "attachment")
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; sandbox")
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Data(http.StatusOK, mime, a.Bytes)
}

// DELETE /api/assets/:id — housekeeping by the owning event's
// SuperOperator (a legacy unowned image: box admin).
func (d *Deps) apiAssetDelete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad asset id"})
		return
	}
	a, err := d.Store.GetAsset(id)
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "no such asset"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	allowed := false
	if a.EventID == 0 {
		allowed = d.isBoxAdmin(c)
	} else if ev, gerr := d.Store.GetEvent(a.EventID); gerr == nil {
		allowed = d.isSuper(c, ev)
	}
	if !allowed {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "only the event's Event Technician may delete its images"})
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
