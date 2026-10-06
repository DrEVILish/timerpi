package routes

import (
	"crypto/tls"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"timerpi/timerpi"
)

// BUGLOG RS1: a session token carries its issue time; one older than
// sessionMaxAge (or from the future, or tampered) is refused. RS4: only
// well-formed live tokens cost a lookup.
func TestSessionTokensExpire(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	ev := timerpi.Event{Code: "ABCD2345", SuperHash: "h"}
	now := int64(1_800_000_000)
	saved := tokenNow
	tokenNow = func() int64 { return now }
	defer func() { tokenNow = saved }()

	tok := superToken(secret, ev)
	if !superTokenOK(secret, tok, ev) || !tokenLooksLive(tok) {
		t.Fatal("a fresh token is refused")
	}
	other := timerpi.Event{Code: "ABCD2345", SuperHash: "changed"}
	if superTokenOK(secret, tok, other) {
		t.Error("a token survived a password change")
	}
	_, mac, _ := strings.Cut(tok, ".")
	if superTokenOK(secret, strconv.FormatInt(now-1000, 10)+"."+mac, ev) {
		t.Error("a re-dated token was accepted")
	}
	now += sessionMaxAge + 1
	if superTokenOK(secret, tok, ev) || tokenLooksLive(tok) {
		t.Error("a token older than sessionMaxAge was accepted")
	}
	if superTokenOK(secret, mac, ev) {
		t.Error("an old-format token (no issue time) was accepted")
	}
}

// BUGLOG RS2: cookies are Secure over HTTPS, directly or via a proxy.
func TestSecureCookieBehindHTTPS(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mk := func(tlsOn bool, proto string) bool {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		if tlsOn {
			c.Request.TLS = &tls.ConnectionState{}
		}
		if proto != "" {
			c.Request.Header.Set("X-Forwarded-Proto", proto)
		}
		return secureCookie(c)
	}
	if mk(false, "") || !mk(true, "") || !mk(false, "https") || mk(false, "http") {
		t.Error("Secure flag wrong for plain / TLS / proxied HTTPS / proxied HTTP")
	}
}
