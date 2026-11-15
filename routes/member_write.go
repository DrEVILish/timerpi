package routes

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// memberWriteError keeps replicas read-only until they hold authority.
func (d *Deps) memberWriteError() (string, error) {
	if d.BoxWriteTarget == nil {
		return "", nil
	}
	primary, writable := d.BoxWriteTarget()
	if writable {
		return "", nil
	}
	if primary == "" {
		return "", fmt.Errorf("This box is waiting for the event's primary. Changes are paused during the election.")
	}
	return primary, fmt.Errorf("This box is a read-only event copy. Connect to the primary at %s to make changes.", primary)
}

// replicaMutation excludes server-local identity, authentication and observation.
func replicaMutation(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[0] != "api" {
		return false
	}
	switch parts[1] {
	case "shows":
		return len(parts) >= 3 && !(len(parts) == 4 && parts[3] == "client-log")
	case "events":
		if len(parts) >= 4 && (parts[len(parts)-1] == "login" || parts[3] == "phone-link") {
			return false
		}
		return true
	case "audience":
		return len(parts) == 4 && (parts[3] == "vote" || parts[3] == "ask")
	case "assets":
		return true
	case "waiting":
		return len(parts) == 4 && parts[3] == "capture"
	}
	return false
}

func (d *Deps) memberWriteGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if !replicaMutation(c.Request.URL.Path) {
			c.Next()
			return
		}
		if primary, err := d.memberWriteError(); err != nil {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"ok": false, "primary": primary, "error": err.Error()})
			return
		}
		c.Next()
	}
}
