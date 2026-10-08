package routes

// box.go — the box password (BUGLOG RC5; PRODUCT §7 decision 2026-10-06).
//
// Box settings (hostname, network role, OSC, default theme) belong to
// whoever looks after the box, not to every event's SuperOperator: anyone
// on the network can create an event, so an event password must never
// unlock them. The box password is set the first time someone opens box
// settings and signs in a `tp_box` session cookie (HMAC over the stored
// hash, so changing it signs every holder out).
//
//	GET  /box                  sign-in, first-time setup, or change/sign out
//	POST /api/box/setup        {password, setupCode} only while none is set
//
// On the cloud anyone on the internet can open /box, so the first-time
// setup also needs the one-time setup code the server prints to its log at
// startup (REPORT #8): only whoever runs the server can claim it. A box is
// on the venue LAN and keeps the plain first-run setup.
//	POST /api/box/login        {password}
//	POST /api/box/password     {current, password}   change (signed in)
//	POST /api/box/logout

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/timerpi"
)

const (
	boxCookieName     = "tp_box"
	minBoxPasswordLen = 8
)

func boxToken(secret []byte, hash string) string {
	return issueToken(secret, "box", hash) // issued-at + HMAC (RS1)
}

// boxSigned reports whether the request holds a valid box session.
func (d *Deps) boxSigned(c *gin.Context) bool {
	if d.Store == nil {
		return false
	}
	hash, err := d.Store.BoxPasswordHash()
	if err != nil || hash == "" {
		return false // unset or unreadable: fail closed
	}
	ck, err := c.Cookie(boxCookieName)
	return err == nil && checkToken(d.Store.SessionSecret(), ck, "box", hash)
}

func (d *Deps) setBoxSession(c *gin.Context, hash string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(boxCookieName, boxToken(d.Store.SessionSecret(), hash), sessionMaxAge, "/", "", secureCookie(c), true)
}

func registerBox(r *gin.Engine, d *Deps) {
	r.GET("/box", d.boxPage)
	r.POST("/api/box/setup", d.apiBoxSetup)
	r.POST("/api/box/login", d.apiBoxLogin)
	r.POST("/api/box/password", d.apiBoxPassword)
	r.POST("/api/box/logout", func(c *gin.Context) {
		c.SetCookie(boxCookieName, "", -1, "/", "", secureCookie(c), true)
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}

// BoxSetupCode is the cloud's one-time box setup code ("" on a box or
// once a box password is set). The first call makes it and logs it.
func (d *Deps) BoxSetupCode() string {
	if !d.isCloud() || d.Store == nil {
		return ""
	}
	if hash, err := d.Store.BoxPasswordHash(); err != nil || hash != "" {
		return ""
	}
	d.boxSetupOnce.Do(func() {
		var b [8]byte
		_, _ = rand.Read(b[:])
		d.boxSetup = hex.EncodeToString(b[:])
		log.Printf("routes: box setup code %s — open /box on this server and enter it to set the box password", d.boxSetup)
	})
	return d.boxSetup
}

func (d *Deps) boxPage(c *gin.Context) {
	if d.Store == nil {
		c.String(http.StatusServiceUnavailable, "store missing")
		return
	}
	hash, err := d.Store.BoxPasswordHash()
	if err != nil {
		c.String(http.StatusInternalServerError, "box settings unavailable")
		return
	}
	next := c.Query("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/settings"
	}
	d.render(c, "box", gin.H{
		"Setup":        hash == "",
		"NeedCode":     hash == "" && d.BoxSetupCode() != "",
		"SignedIn":     d.boxSigned(c),
		"Next":         next,
		"MinLen":       minBoxPasswordLen,
		"DefaultTheme": config.DefaultTheme(),
	})
}

type boxBody struct {
	Password string `json:"password"`
	Current  string `json:"current"`
	// SetupCode is the cloud's one-time setup code (BoxSetupCode).
	SetupCode string `json:"setupCode"`
}

func readBoxBody(c *gin.Context) (boxBody, bool) {
	var b boxBody
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad request"})
		return b, false
	}
	return b, true
}

func validBoxPassword(c *gin.Context, pw string) bool {
	if utf8.RuneCountInString(pw) < minBoxPasswordLen || len(pw) > 256 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "The box password needs at least 8 characters"})
		return false
	}
	return true
}

func (d *Deps) apiBoxSetup(c *gin.Context) {
	b, ok := readBoxBody(c)
	if !ok || !validBoxPassword(c, b.Password) {
		return
	}
	hash, err := d.Store.BoxPasswordHash()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": "box settings unavailable"})
		return
	}
	if hash != "" {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "This box already has a password. Sign in instead."})
		return
	}
	if want := d.BoxSetupCode(); want != "" {
		if !loginAllowed(c, "boxsetup") {
			return
		}
		got := strings.ToLower(strings.TrimSpace(b.SetupCode))
		ok := subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
		loginResult(c, "boxsetup", ok)
		if !ok {
			c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "Enter the setup code from the server log (it is printed when the server starts)."})
			return
		}
	}
	if err := d.Store.SetBoxPassword(b.Password); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	hash, _ = d.Store.BoxPasswordHash()
	d.setBoxSession(c, hash)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) apiBoxLogin(c *gin.Context) {
	b, ok := readBoxBody(c)
	if !ok {
		return
	}
	hash, err := d.Store.BoxPasswordHash()
	if err != nil || hash == "" {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "This box has no password yet. Set one first."})
		return
	}
	if !loginAllowed(c, "box") {
		return
	}
	ok = timerpi.CheckPassword(hash, b.Password)
	loginResult(c, "box", ok)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "Wrong box password"})
		return
	}
	d.setBoxSession(c, hash)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) apiBoxPassword(c *gin.Context) {
	b, ok := readBoxBody(c)
	if !ok {
		return
	}
	hash, err := d.Store.BoxPasswordHash()
	if err != nil || hash == "" {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "This box has no password yet. Set one first."})
		return
	}
	if !d.boxSigned(c) {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "Sign in to box settings first"})
		return
	}
	if !loginAllowed(c, "box") {
		return
	}
	ok = timerpi.CheckPassword(hash, b.Current)
	loginResult(c, "box", ok)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "Wrong current box password"})
		return
	}
	if !validBoxPassword(c, b.Password) {
		return
	}
	if err := d.Store.SetBoxPassword(b.Password); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	hash, _ = d.Store.BoxPasswordHash()
	d.setBoxSession(c, hash) // every other holder is signed out
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
