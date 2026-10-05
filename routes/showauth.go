// showauth.go — the per-show passphrase gate (privacy tier 2).
//
// The 8-character share code is the ONLY public address, but an operator can
// optionally give ONE show an extra password. When set:
//
//   - /c/<code> and /d/<code> render a lock page until the passphrase is
//     entered (unlock sets the `tp_show_<CODE>` cookie).
//   - WS joins for the show must present the join token (session.go /
//     mesh.js carry it from localStorage, written by the unlock page) or the
//     unlock cookie on the upgrade request.
//   - Snapshot/WS payloads NEVER carry the passphrase (Show.Passphrase is
//     json:"-", and the join token is derived, not the plaintext).
package routes

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"timerpi/timerpi"
)

// showEntropy is the derivation domain (changing it invalidates every
// per-show session at once, like config.AuthPassword's entropy).
const showEntropy = "timerpi/showpass/v1"

// showCookieName is per-code so several locked shows can be open in one
// browser without clobbering each other.
func showCookieName(code string) string { return "tp_show_" + code }

// ShowPassToken derives the session token for one show's passphrase.
func ShowPassToken(code, pw string) string {
	sum := sha256.Sum256([]byte(showEntropy + "|" + strings.ToUpper(code) + "|" + pw))
	return hex.EncodeToString(sum[:])
}

// showUnlockedByCookie reports whether the request carries a valid
// per-show unlock cookie.
func showUnlockedByCookie(c *gin.Context, code, pw string) bool {
	tok, err := c.Cookie(showCookieName(code))
	return err == nil && tok != "" &&
		subtle.ConstantTimeCompare([]byte(tok), []byte(ShowPassToken(code, pw))) == 1
}

// requireShowGated is requireShow + the per-show passphrase check (API arm):
// every show-scoped REST endpoint that touches cue/state content refuses a
// locked show until the browser carries the unlock cookie. Passphrase,
// unlock, QR and import-example endpoints stay on plain requireShow.
func (d *Deps) requireShowGated(c *gin.Context) (int64, bool) {
	id, ok := d.requireShow(c)
	if !ok {
		return 0, false
	}
	if d.Store != nil {
		if sh, err := d.Store.GetShow(id); err == nil && sh.Passphrase != "" &&
			!showUnlockedByCookie(c, sh.Code, sh.Passphrase) {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "show password required (POST /api/shows/" + sh.Code + "/unlock)",
			})
			return 0, false
		}
	}
	return id, true
}

// showGate is the per-request check for resolved-show page handlers: true
// means proceed. Browsers get the unlock page; API callers get 401.
// Display AND operator pages gate — an extra password that TV pages skipped
// would be no extra security at all.
func showGate(c *gin.Context, sh timerpi.Show) bool {
	if sh.Passphrase == "" {
		return true
	}
	if showUnlockedByCookie(c, sh.Code, sh.Passphrase) {
		return true
	}
	if c.Request.Method == http.MethodGet && strings.Contains(c.GetHeader("Accept"), "text/html") {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(showLockHTML(sh)))
		c.Abort()
		return false
	}
	c.JSON(http.StatusUnauthorized, gin.H{"error": "show password required (POST /api/shows/" + sh.Code + "/unlock)"})
	c.Abort()
	return false
}

// registerShowAuth mounts the show-passphrase endpoints into the /api/shows
// group (called from registerAPI so no path-template conflicts).
func registerShowAuth(g *gin.RouterGroup, d *Deps) {
	g.POST("/shows/:ident/passphrase", showPassphraseAPI(d))
	g.POST("/shows/:ident/unlock", showUnlockAPI(d))
}

// showPassphraseAPI sets/clears the show's extra password. Operator-auth
// rules already apply at the middleware layer (gated when the operator
// password is set; open-appliance first-set path otherwise).
func showPassphraseAPI(d *Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := d.requireShow(c)
		if !ok {
			return
		}
		body, okb := readPwBody(c)
		if !okb {
			return
		}
		// The passphrase API is itself show-gated once a password exists:
		// only a browser that unlocked this show (cookie/token) — or an
		// operator login while the operator password is on — may change or
		// clear it. Otherwise a LAN passer-by could silently REMOVE the
		// gate or lock operators out.
		sh0, _ := d.Store.GetShow(id)
		if sh0.Passphrase != "" && !showUnlockedByCookie(c, sh0.Code, sh0.Passphrase) {
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "unlock this show first (wrong or missing show cookie)"})
			return
		}
		if err := d.Store.SetShowPassphrase(id, body); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		resp := gin.H{"ok": true, "enabled": body != ""}
		if body != "" {
			// The setter's own browser unlocks immediately (cookie + token);
			// every OTHER device needs the passphrase on the next open.
			sh, _ := d.Store.GetShow(id)
			tok := ShowPassToken(sh.Code, body)
			c.SetCookie(showCookieName(sh.Code), tok, 7*24*3600, "/", "", false, true)
			resp["token"] = tok
		}
		c.JSON(http.StatusOK, resp)
	}
}

// showUnlockAPI verifies the passphrase and issues the per-show cookie +
// token (stored in localStorage by the lock page for the WS join).
func showUnlockAPI(d *Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := d.requireShow(c)
		if !ok {
			return
		}
		body, okb := readPwBody(c)
		if !okb {
			return
		}
		want, err := d.Store.ShowPassphrase(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		if want != "" && subtle.ConstantTimeCompare([]byte(body), []byte(want)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "wrong show password"})
			return
		}
		sh, _ := d.Store.GetShow(id)
		tok := ShowPassToken(sh.Code, want)
		c.SetCookie(showCookieName(sh.Code), tok, 7*24*3600, "/", "", false, true)
		c.JSON(http.StatusOK, gin.H{"ok": true, "token": tok})
	}
}

// readPwBody reads {pw} JSON or pw= form; writes the error and returns
// false on syntax failure.
func readPwBody(c *gin.Context) (string, bool) {
	var body struct {
		PW string `json:"pw"`
	}
	if strings.HasPrefix(c.ContentType(), "application/json") {
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
			return "", false
		}
	} else {
		body.PW = c.PostForm("pw")
	}
	return body.PW, true
}

// ShowUnlockedJoin is the WS-arm of the per-show passphrase check (called
// from ws/session.go readJoin): the join must carry a valid show token OR
// the upgrade request must carry the unlock cookie for this show.
func ShowUnlockedJoin(cookies map[string]string, code, pw, joinToken string) bool {
	name := showCookieName(code)
	return (cookies != nil && subtle.ConstantTimeCompare([]byte(cookies[name]),
		[]byte(ShowPassToken(code, pw))) == 1) ||
		(joinToken != "" && subtle.ConstantTimeCompare([]byte(joinToken),
			[]byte(ShowPassToken(code, pw))) == 1)
}

// showLockHTML — standalone show-password page (same micro-page pattern as
// /login: no template registry dependency, themed, posts to the unlock API
// and stores the join token for the WS client).
func showLockHTML(sh timerpi.Show) string {
	return `<!DOCTYPE html><html lang="en" data-theme="blue-future"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>TimerPi — show password</title>` +
		`<link rel="stylesheet" href="/ftl/dist/blue-future.css">` +
		`<link rel="stylesheet" href="/css/` + assetsRev + `/timerpi.v58.css"></head>` +
		`<body class="app"><main class="main" style="max-width:26rem;margin:8vh auto;padding:0 4vw">` +
		`<h1>SHOW PASSWORD</h1>` +
		`<p class="text-muted">This show carries an extra password. Enter it once — this browser` +
		` (including its TV windows) stays unlocked for 7 days.</p>` +
		`<form id="f">` +
		`<input type="password" id="pw" class="input" autocomplete="current-password" aria-label="Show password" required autofocus>` +
		`<button type="submit" class="btn btn-primary" style="margin-top:.75rem">Unlock show</button>` +
		`</form>` +
		`<p id="err" class="field-error" role="alert" hidden></p>` +
		`<script>` +
		`var CODE = ` + jsStr(sh.Code) + `;` +
		`document.getElementById('f').addEventListener('submit', function (e) {` +
		` e.preventDefault();` +
		` fetch('/api/shows/' + CODE + '/unlock', { method: 'POST', headers: {'content-type': 'application/json'},` +
		`   body: JSON.stringify({ pw: document.getElementById('pw').value }) })` +
		` .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })` +
		` .then(function (out) {` +
		`  if (out.ok) {` +
		`   try { localStorage.setItem('tp.show.' + CODE, out.j.token); } catch (e) {}` +
		`   location.href = location.pathname; // back to the /c/ or /d/ page that asked` +
		`  } else {` +
		`   var el = document.getElementById('err');` +
		`   el.textContent = out.j.error || 'unlock failed'; el.hidden = false;` +
		`  }` +
		` })` +
		` .catch(function () { var el = document.getElementById('err'); el.textContent = 'network error'; el.hidden = false; });` +
		`});` +
		`</script></main></body></html>`
}
