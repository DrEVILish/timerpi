package routes

// login_limit.go — brute-force brake on every password check (BUGLOG RW12):
// supervisor, room and box sign-ins. Failures are counted per client IP
// and target (event/room/box) and per target across all IPs; past the
// limit the check is refused with 429 before any hashing, so a flood of
// guesses costs the Pi nothing. A success clears that IP's count.

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	loginWindow       = 5 * time.Minute
	loginFailsPerIP   = 8  // per IP and target
	loginFailsPerTarg = 60 // per target, every IP together
)

type failWin struct {
	start time.Time
	n     int
}

var loginFails struct {
	sync.Mutex
	m map[string]failWin
}

func failCount(key string, now time.Time) failWin {
	w := loginFails.m[key]
	if now.Sub(w.start) > loginWindow {
		return failWin{start: now}
	}
	return w
}

// loginAllowed answers 429 (and returns false) when ip or the target has
// failed too often lately.
func loginAllowed(c *gin.Context, target string) bool {
	now := time.Now()
	ipKey := c.ClientIP() + "|" + target
	loginFails.Lock()
	ipW, tW := failCount(ipKey, now), failCount("*|"+target, now)
	loginFails.Unlock()
	if ipW.n < loginFailsPerIP && tW.n < loginFailsPerTarg {
		return true
	}
	start := ipW.start
	if tW.n >= loginFailsPerTarg {
		start = tW.start
	}
	wait := int(loginWindow.Seconds() - now.Sub(start).Seconds())
	if wait < 1 {
		wait = 1
	}
	c.Header("Retry-After", strconv.Itoa(wait))
	c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "Too many wrong passwords. Wait a few minutes and try again."})
	return false
}

// loginResult records the outcome of one password check.
func loginResult(c *gin.Context, target string, ok bool) {
	now := time.Now()
	ipKey := c.ClientIP() + "|" + target
	loginFails.Lock()
	defer loginFails.Unlock()
	if loginFails.m == nil || len(loginFails.m) > 50_000 {
		loginFails.m = map[string]failWin{}
	}
	if ok {
		delete(loginFails.m, ipKey)
		return
	}
	for _, k := range []string{ipKey, "*|" + target} {
		w := failCount(k, now)
		w.n++
		loginFails.m[k] = w
	}
}
