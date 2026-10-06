package routes

// audience_device.go — who a phone is (BUGLOG RW2, RW3, RS10).
//
// A phone's identity for votes, upvotes and submissions is a device id the
// SERVER issues in a signed HttpOnly cookie (tp_aud = id.hmac), never a
// value the phone sends: a script that invents a fresh "peer" per request
// used to get unlimited votes. Minting is budgeted per client IP, so a
// script that drops its cookie every time gets a handful of identities,
// not thousands. Behind a reverse proxy on the same box the real client
// IP comes from X-Forwarded-For (only loopback proxies are trusted).

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/timerpi"
)

const (
	audCookieName = "tp_aud"
	audCookieAge  = 30 * 24 * 3600
	audMintWindow = 10 * time.Minute
)

// audMint: new device ids per client IP per window. A phone needs one; a
// family on one hotspot a few.
var audMint = &windowLimiter{max: 20, window: audMintWindow, bound: 50_000}

func audToken(secret []byte, id string) string {
	return id + "." + timerpi.SignSession(secret, "aud", id)
}

// audiencePeer returns this phone's server-issued device id, minting one
// (and setting the cookie) when the request has no valid one. ok=false
// means the mint budget is spent; a 429 has been written.
func (d *Deps) audiencePeer(c *gin.Context) (string, bool) {
	secret := d.Store.SessionSecret()
	if ck, err := c.Cookie(audCookieName); err == nil {
		if id, _, found := strings.Cut(ck, "."); found && tokenEq(ck, audToken(secret, id)) {
			return id, true
		}
	}
	if !audMint.allow(c.ClientIP(), time.Now()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "Too many new devices from this network — try again in a few minutes"})
		return "", false
	}
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
		return "", false
	}
	id := "d-" + hex.EncodeToString(b[:])
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(audCookieName, audToken(secret, id), audCookieAge, "/", "", secureCookie(c), true)
	return id, true
}

// throttle is a per-key "at most once per window" guard. take reserves the
// slot atomically (no check-then-mark race, RS10); release hands it back
// when the request is then refused for another reason.
type throttle struct {
	mu     sync.Mutex
	last   map[string]int64
	window int64 // ms
	max    int   // map bound (reset beyond it)
}

func (t *throttle) take(key string, now int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil || len(t.last) > t.max {
		t.last = map[string]int64{}
	}
	if now-t.last[key] < t.window {
		return false
	}
	t.last[key] = now
	return true
}

func (t *throttle) release(key string, now int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last[key] == now {
		delete(t.last, key)
	}
}
