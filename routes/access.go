package routes

// access.go — who may do what (PRODUCT §3.1). One place, three questions:
//
//	isSuper(c, event)      — SuperOperator of that event (supervisor session)
//	canModerate(c, room)   — moderator of that room, or SuperOperator of its event
//	isBoxAdmin(c)          — may change box settings: holds a box session
//	                         (box password, box.go); event passwords never do
//
// Sessions are HttpOnly cookies holding an HMAC over the code(s) and the
// stored password hash (timerpi.SignSession), so a password change signs
// every old holder out:
//
//	tp_ev_<EVENTCODE>  supervisor session
//	tp_rm_<ROOMCODE>   moderator session for one room
//	tp_box             box settings session
//
// Screens (/d/), audience pages (/a/), the event walk-in and static assets
// are open: they never carry operator controls.

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/timerpi"
)

const sessionMaxAge = 14 * 24 * 3600

func superCookieName(evCode string) string  { return "tp_ev_" + evCode }
func roomCookieName(roomCode string) string { return "tp_rm_" + roomCode }

// Session tokens are "<issued unix seconds>.<HMAC over the parts and the
// issue time>". A token older than sessionMaxAge is refused server-side,
// whatever the browser kept (BUGLOG RS1: tokens used to be valid forever,
// so a copied cookie for a password-less room could never be revoked).
var tokenNow = func() int64 { return time.Now().Unix() }

func issueToken(secret []byte, parts ...string) string {
	at := strconv.FormatInt(tokenNow(), 10)
	return at + "." + timerpi.SignSession(secret, append(parts, at)...)
}

func checkToken(secret []byte, tok string, parts ...string) bool {
	at, _, ok := strings.Cut(tok, ".")
	if !ok {
		return false
	}
	issued, err := strconv.ParseInt(at, 10, 64)
	now := tokenNow()
	if err != nil || issued > now+300 || now-issued > sessionMaxAge {
		return false
	}
	return tokenEq(tok, at+"."+timerpi.SignSession(secret, append(parts, at)...))
}

func superToken(secret []byte, ev timerpi.Event) string {
	return issueToken(secret, "ev", ev.Code, ev.SuperHash)
}

// SuperSessionToken is a fresh supervisor session token for ev (tests that
// build cookies by hand, e.g. the ws package).
func SuperSessionToken(store *timerpi.DB, ev timerpi.Event) string {
	return superToken(store.SessionSecret(), ev)
}

func superTokenOK(secret []byte, tok string, ev timerpi.Event) bool {
	return checkToken(secret, tok, "ev", ev.Code, ev.SuperHash)
}

func roomToken(secret []byte, ev timerpi.Event, room timerpi.Show) string {
	return issueToken(secret, "rm", ev.Code, room.Code, room.RoomPW)
}

func roomTokenOK(secret []byte, tok string, ev timerpi.Event, room timerpi.Show) bool {
	return checkToken(secret, tok, "rm", ev.Code, room.Code, room.RoomPW)
}

// secureCookie: mark cookies Secure when the browser reached us over
// HTTPS, directly or through a proxy (BUGLOG RS2).
func secureCookie(c *gin.Context) bool {
	return c.Request.TLS != nil || strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

// maxSessionCookies bounds how many tp_ev_/tp_rm_ cookies one request may
// make us look up (BUGLOG RS4: ~1 MB of fake names meant tens of
// thousands of queries). Only well-formed, unexpired tokens count, so old
// cookies never crowd out a live one.
const maxSessionCookies = 32

// tokenLooksLive is the free pre-check before any DB lookup: the shape
// "<issued>.<64 hex>" with an issue time inside sessionMaxAge.
func tokenLooksLive(tok string) bool {
	at, mac, ok := strings.Cut(tok, ".")
	if !ok || len(mac) != 64 {
		return false
	}
	issued, err := strconv.ParseInt(at, 10, 64)
	now := tokenNow()
	return err == nil && issued <= now+300 && now-issued <= sessionMaxAge
}

func tokenEq(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// cookieMap flattens request cookies (the WS arm passes the upgrade
// request's cookies through the same checks).
func cookieMap(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, ck := range r.Cookies() {
		out[ck.Name] = ck.Value
	}
	return out
}

// SuperFromCookies reports whether the cookies hold a supervisor session
// for ev.
func SuperFromCookies(store *timerpi.DB, cookies map[string]string, ev timerpi.Event) bool {
	if store == nil || ev.ID == 0 {
		return false
	}
	return superTokenOK(store.SessionSecret(), cookies[superCookieName(ev.Code)], ev)
}

// ModerateFromCookies reports whether the cookies may moderate the room
// (its own moderator session, or the event's supervisor session). The WS
// hub calls this for "controls" joins.
func ModerateFromCookies(store *timerpi.DB, cookies map[string]string, showID int64) bool {
	if store == nil {
		return false
	}
	room, err := store.GetShow(showID)
	if err != nil {
		return false
	}
	ev, err := store.GetEvent(room.EventID)
	if err != nil {
		return false
	}
	if SuperFromCookies(store, cookies, ev) {
		return true
	}
	return roomTokenOK(store.SessionSecret(), cookies[roomCookieName(room.Code)], ev, room)
}

func (d *Deps) isSuper(c *gin.Context, ev timerpi.Event) bool {
	return SuperFromCookies(d.Store, cookieMap(c.Request), ev)
}

func (d *Deps) canModerate(c *gin.Context, showID int64) bool {
	return ModerateFromCookies(d.Store, cookieMap(c.Request), showID)
}

// hasSuperSession: the request holds at least one valid Event Technician
// (supervisor) session. The waiting room is theirs: setting up screens is
// never a room moderator's job (STATUS U25).
func (d *Deps) hasSuperSession(c *gin.Context) bool {
	if d.Store == nil {
		return false
	}
	cookies := cookieMap(c.Request)
	seen := 0
	for name, val := range cookies {
		if !strings.HasPrefix(name, "tp_ev_") {
			continue
		}
		if !tokenLooksLive(val) {
			continue
		}
		if seen++; seen > maxSessionCookies {
			break
		}
		if ev, ok := d.Store.ResolveEvent(strings.TrimPrefix(name, "tp_ev_")); ok && SuperFromCookies(d.Store, cookies, ev) {
			return true
		}
	}
	return false
}

// isBoxAdmin: box settings need the box password (box.go). Until one is
// set nobody is box admin; /box offers the first-time setup.
func (d *Deps) isBoxAdmin(c *gin.Context) bool {
	return d.boxSigned(c)
}

// requireShowGated resolves :ident and demands moderator access to it.
// Every show-scoped REST endpoint that reads operator data or mutates
// goes through here.
func (d *Deps) requireShowGated(c *gin.Context) (int64, bool) {
	id, ok := d.requireShow(c)
	if !ok {
		return 0, false
	}
	if !d.canModerate(c, id) {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "moderator access required — join the room from the home page with the event code"})
		return 0, false
	}
	return id, true
}

// requireBoxAdmin guards box-level settings endpoints.
func (d *Deps) requireBoxAdmin(c *gin.Context) bool {
	if d.isBoxAdmin(c) {
		return true
	}
	if wantsHTML(c) {
		c.Redirect(http.StatusSeeOther, "/box?next="+url.QueryEscape(c.Request.URL.RequestURI()))
		c.Abort()
		return false
	}
	c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "box password required — sign in at /box"})
	c.Abort()
	return false
}

// requireSuperSession guards the waiting room.
func (d *Deps) requireSuperSession(c *gin.Context) bool {
	if d.hasSuperSession(c) {
		return true
	}
	c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "Event Technician sign-in required"})
	c.Abort()
	return false
}

// moderatorPageGate is the page arm (/c/, /screens/): a browser without
// access gets a friendly page pointing home (never the event code — a
// room code alone, e.g. from an audience QR, must reveal nothing).
func (d *Deps) moderatorPageGate(c *gin.Context, showID int64) bool {
	if d.canModerate(c, showID) {
		return true
	}
	d.renderAccessDenied(c, "Moderator access needed", "Go to the home page, type your event code and pick your room.")
	return false
}

func wantsHTML(c *gin.Context) bool {
	return c.Request.Method == http.MethodGet && strings.Contains(c.GetHeader("Accept"), "text/html")
}

func (d *Deps) renderAccessDenied(c *gin.Context, title, msg string) {
	c.Status(http.StatusUnauthorized)
	if d.Tmpl == nil {
		c.String(http.StatusUnauthorized, title+": "+msg)
		c.Abort()
		return
	}
	d.render(c, "denied", gin.H{"Title": title, "Message": msg, "DefaultTheme": config.DefaultTheme()})
	c.Abort()
}

// setSuperSession / setRoomSession issue the cookies after a successful
// sign-in.
func (d *Deps) setSuperSession(c *gin.Context, ev timerpi.Event) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(superCookieName(ev.Code), superToken(d.Store.SessionSecret(), ev), sessionMaxAge, "/", "", secureCookie(c), true)
}

func (d *Deps) setRoomSession(c *gin.Context, ev timerpi.Event, room timerpi.Show) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(roomCookieName(room.Code), roomToken(d.Store.SessionSecret(), ev, room), sessionMaxAge, "/", "", secureCookie(c), true)
}

// clearSessions drops every TimerPi session cookie on this browser.
func clearSessions(c *gin.Context) {
	for _, ck := range c.Request.Cookies() {
		if strings.HasPrefix(ck.Name, "tp_ev_") || strings.HasPrefix(ck.Name, "tp_rm_") ||
			strings.HasPrefix(ck.Name, "tp_show_") || ck.Name == "tp_auth" || ck.Name == boxCookieName {
			c.SetCookie(ck.Name, "", -1, "/", "", secureCookie(c), true)
		}
	}
}

// accessGate is the global middleware for box-level and shared operator
// endpoints that are not tied to one room or event (those check access in
// their handlers: requireShowGated / requireSuper).
func (d *Deps) accessGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		m := c.Request.Method
		switch {
		case p == "/settings" || strings.HasPrefix(p, "/api/network") || p == "/frag/network" ||
			strings.HasPrefix(p, "/api/osc") || (p == "/api/theme" && m != http.MethodGet):
			if !d.requireBoxAdmin(c) {
				return
			}
		case p == "/api/waiting/register" || p == "/api/waiting/mine":
			// Screens register themselves without signing in.
		case strings.HasPrefix(p, "/api/waiting"):
			// /api/assets checks its event scope in the handlers (assets.go).
			if !d.requireSuperSession(c) {
				return
			}
		}
		c.Next()
	}
}
