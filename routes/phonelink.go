// phonelink.go — sign an Event Technician in on their phone by scanning a
// QR code on the Event Technician page (owner 2026-10-07: walk the venue
// and check settings from the phone).
//
//	POST /api/events/:code/phone-link {base}  Event Technician → {url, qr, expiresAt}
//	GET  /e/:code/phone?t=<token>               the phone: session cookie → /e/:code/admin
//
// The token is "<expiry unix>.<nonce>.<HMAC>", keyed with the event's
// Event Technician password hash. That hash travels with every copy of the
// event (cloud and venue boxes), so a link minted at the venue also works
// on the cloud copy, and changing the password voids every link. Links
// live PhoneLinkTTL and are single-use (the redeeming server remembers
// spent nonces until they expire).
package routes

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"

	"timerpi/timerpi"
)

// PhoneLinkTTL is how long a phone sign-in code stays valid.
var PhoneLinkTTL = 2 * time.Minute

var phoneSpent = struct {
	sync.Mutex
	m map[string]int64 // nonce → expiry (unix)
}{m: map[string]int64{}}

func phoneMAC(ev timerpi.Event, exp, nonce string) string {
	m := hmac.New(sha256.New, []byte("timerpi-phone/1|"+ev.SuperHash))
	m.Write([]byte(ev.Code + "|" + exp + "|" + nonce))
	return hex.EncodeToString(m.Sum(nil))
}

func phoneToken(ev timerpi.Event, now time.Time) (string, time.Time) {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	nonce := hex.EncodeToString(b)
	expT := now.Add(PhoneLinkTTL)
	exp := strconv.FormatInt(expT.Unix(), 10)
	return exp + "." + nonce + "." + phoneMAC(ev, exp, nonce), expT
}

// phoneRedeem checks a token and spends it.
func phoneRedeem(ev timerpi.Event, tok string, now time.Time) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || ev.SuperHash == "" {
		return false
	}
	exp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || now.Unix() > exp || exp > now.Add(PhoneLinkTTL+time.Minute).Unix() {
		return false
	}
	if !hmac.Equal([]byte(parts[2]), []byte(phoneMAC(ev, parts[0], parts[1]))) {
		return false
	}
	phoneSpent.Lock()
	defer phoneSpent.Unlock()
	for n, e := range phoneSpent.m {
		if e < now.Unix() {
			delete(phoneSpent.m, n)
		}
	}
	if _, used := phoneSpent.m[parts[1]]; used {
		return false
	}
	phoneSpent.m[parts[1]] = exp
	return true
}

// POST /api/events/:code/phone-link {base} — base is the address the
// technician's browser uses (location.origin), so the phone opens the same
// server (cloud, timerpi.local or the box's IP).
func (d *Deps) apiPhoneLink(c *gin.Context) {
	ev, ok := d.requireSuper(c)
	if !ok {
		return
	}
	if !ev.HasSuperPassword() {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "Set an Event Technician Password first: the phone code is signed with it."})
		return
	}
	var body struct {
		Base string `json:"base"`
	}
	_ = c.ShouldBindJSON(&body)
	base, err := url.Parse(strings.TrimSpace(body.Base))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		base, _ = url.Parse(requestOrigin(c))
	}
	tok, exp := phoneToken(ev, time.Now())
	link := base.Scheme + "://" + base.Host + "/e/" + url.PathEscape(ev.Code) + "/phone?t=" + url.QueryEscape(tok)
	png, err := qrcode.Encode(link, qrcode.Medium, 320)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"ok": true, "url": link, "expiresAt": exp.UnixMilli(),
		"qr": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)})
}

// GET /e/:code/phone?t= — the phone's landing: a good code signs it in as
// Event Technician and opens the dashboard; anything else says to scan a
// fresh code. Failures count against the sign-in limiter.
func (d *Deps) phoneSignIn(c *gin.Context) {
	c.Header("Referrer-Policy", "no-referrer") // the code never leaves in a Referer
	c.Header("Cache-Control", "no-store")
	ev, ok := d.Store.ResolveEvent(c.Param("code"))
	if !ok {
		d.renderAccessDenied(c, "Unknown event", "This event code isn't on this server.")
		return
	}
	target := "phone:" + ev.Code
	if !loginAllowed(c, target) {
		return
	}
	good := phoneRedeem(ev, c.Query("t"), time.Now())
	loginResult(c, target, good)
	if !good {
		d.renderAccessDenied(c, "Code expired", "This phone code has expired or was already used. Show a fresh code on the Event Technician page and scan it again.")
		return
	}
	d.setSuperSession(c, ev)
	c.Redirect(http.StatusSeeOther, "/e/"+ev.Code+"/admin")
}
