package routes

// login_limit.go — brute-force brake on every password check (BUGLOG RW12):
// supervisor, room and box sign-ins. Failures are counted per client IP
// and target (event/room/box); past the limit that IP is refused with 429
// before any hashing, so a flood of guesses costs the Pi nothing. A
// success clears that IP's count.
//
// Per target across all IPs there is a second, much higher budget: once
// it is used up only addresses that have already failed on that target
// themselves are refused. A clean address (the real technician) always
// gets its tries, so an attacker spread over many IPs can't lock the real
// password out; each of the attacker's addresses just gets fewer guesses.

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	loginWindow           = 5 * time.Minute
	loginFailsPerIP       = 8   // per IP and target
	loginFailsPerTarg     = 300 // per target, every IP together (then: failed IPs only)
	loginFailsUnderAttack = 2   // an IP's failures allowed once the target budget is spent
)

var loginFails struct {
	sync.Mutex
	m map[string]windowCount
}

func failCount(key string, now time.Time) windowCount {
	w := loginFails.m[key]
	if now.Sub(w.start) > loginWindow {
		return windowCount{start: now}
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
	if ipW.n < loginFailsPerIP && (tW.n < loginFailsPerTarg || ipW.n < loginFailsUnderAttack) {
		return true
	}
	start := ipW.start
	wait := max(1, int(loginWindow.Seconds()-now.Sub(start).Seconds()))
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
		loginFails.m = map[string]windowCount{}
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
