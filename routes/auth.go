// auth.go — the operator password gate (ladder item A1).
//
// config.AuthPassword existed since v1 but nothing checked it; this file
// wires it honestly:
//
//   - When the password is SET, every route except the MVList below needs
//     proof of identity: the `tp_auth` cookie (set by POST /api/login) or
//     HTTP Basic credentials for scripts/curl/mesh tools.
//   - /d/<code> (stage TVs), /health and static assets stay OPEN — a display
//     must never re-login after a power cut mid-show.
//   - The /ws upgrade is exempt at the HTTP layer (a fresh display TV has no
//     cookie), but ws/session.go re-gates at join time: role "controls"
//     joins must present the auth token; the join frame carries it from
//     localStorage. Commands from non-controls sessions are refused in
//     ws/commands.go so a spoofed display join cannot mutate either.
//   - GET+text/html browsers get a 302 to /login; API/htmx callers get a
//     401 JSON. OriginGuard already blocks cross-site form posts, so the
//     cookie is not a CSRF vector on its own.
package routes

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"timerpi/config"
)

// authExempt reports whether a path may pass without proof of identity.
// Display TVs, health probes, static assets and the login round-trip itself
// are exempt; everything operator-shaped is not.
func authExempt(path string) bool {
	if path == "/health" || path == "/login" || path == "/api/login" ||
		path == "/api/waiting/register" || path == "/api/waiting/mine" {
		// The last two: a waiting display has no show to unlock against
		// and a stage TV never logs in (registry holds only screen names).
		return true
	}
	for _, pre := range []string{"/d/", "/ftl/", "/css/", "/src/", "/img/", "/ws"} {
		if strings.HasPrefix(path, pre) {
			return true
		}
	}
	// Stage TVs never log in (PROTOCOL §auth), yet their pages embed two
	// API calls as plain same-origin requests: the join-card <img> QR and
	// the F4 client-error POST. Both stay show-passphrase-gated inside
	// their handlers; only the OPERATOR-password layer exempts them —
	// otherwise a password set flips every TV into broken images and
	// silently dropped error reports.
	if segs := strings.Split(strings.Trim(path, "/"), "/"); len(segs) == 4 &&
		segs[0] == "api" && segs[1] == "shows" &&
		(segs[3] == "qr" || segs[3] == "client-log") {
		return true
	}
	return false
}

// AuthGate is the per-request password check (chained right after
// OriginGuard; order matters: origin first, identity second).
func AuthGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !config.HasAuth() {
			c.Next()
			return
		}
		if authExempt(c.Request.URL.Path) {
			c.Next()
			return
		}
		if requestAuthed(c) {
			c.Next()
			return
		}
		if c.Request.Method == http.MethodGet && strings.Contains(c.GetHeader("Accept"), "text/html") {
			// Human in a browser: land on the login page.
			c.Redirect(http.StatusFound, "/login?next="+url.QueryEscape(c.Request.URL.Path))
			c.Abort()
			return
		}
		// Everything else (API, htmx frags, scripts without credentials).
		c.Header("WWW-Authenticate", `Basic realm="TimerPi operator"`)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "operator password required (POST {pw} to /api/login, or send Basic auth)",
		})
		c.Abort()
	}
}

// requestAuthed checks the login cookie or an Authorization: Basic header
// against the configured password (constant-time compares).
func requestAuthed(c *gin.Context) bool {
	if tok, err := c.Cookie("tp_auth"); err == nil && tok != "" && config.CheckToken(tok) {
		return true
	}
	user, pw, ok := c.Request.BasicAuth()
	return ok && config.CheckPassword(pw) && strings.EqualFold(user, "operator")
}

// assetsRev is the current PATH revision for static assets: templates emit
// /css/<rev>/…, /src/<rev>/… and the NoRoute rewriter maps it back to the
// public/ tree. Bump on every CSS/JS change (one string + the template sed);
// older rev paths keep resolving so cached pages never 404.
const assetsRev = "v51"

// registerAuth mounts the login round-trip and the password setter.// Routes registered here are intentionally NOT in any tests' page lists —
// they are the first lines of defense, not page furniture.
func registerAuth(r *gin.Engine) {
	r.GET("/login", loginPage)
	r.POST("/api/login", loginAPI)
	r.POST("/api/auth/password", authPasswordAPI)
}

// ---- B7: appliance default theme (which ftl bundle fresh browsers use) ---

// installedThemes lists the vendored ftl dist bundles by filename
// (authoritative — no hard-coded list to rot).
func installedThemes() []string {
	dir := findDir(filepath.Join("third_party", "ftl-themes", "dist"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"blue-future"}
	}
	out := []string{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".css") {
			out = append(out, strings.TrimSuffix(name, ".css"))
		}
	}
	return out
}

// registerTheme wires the appliance default theme into /settings surfaces:
// GET answers current + available bundles, POST rewrites the fallback used
// by every fresh operator browser (showFolder puts it where the person who
// just renamed a hostname sets it — device identity, not per-show).
func registerTheme(r *gin.Engine) {
	r.GET("/api/theme", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"current":  config.DefaultTheme(),
			"fallback": "blue-future",
			"themes":   installedThemes(),
		})
	})
	r.POST("/api/theme", func(c *gin.Context) {
		var body struct {
			Theme string `json:"theme"`
		}
		if strings.HasPrefix(c.ContentType(), "application/json") {
			if err := c.ShouldBindJSON(&body); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
				return
			}
		} else {
			body.Theme = c.PostForm("theme")
		}
		if err := config.SetDefaultTheme(body.Theme); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "theme": config.DefaultTheme()})
	})
}

// loginAPI answers {pw} (JSON) or pw= (form). Sets the HttpOnly cookie and
// returns the join token for localStorage (the WS join frame presents it;
// HttpOnly keeps it out of the page's JS by design).
func loginAPI(c *gin.Context) {
	var body struct {
		PW string `json:"pw"`
	}
	if strings.HasPrefix(c.ContentType(), "application/json") {
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
			return
		}
	} else {
		body.PW = c.PostForm("pw")
	}
	if !config.CheckPassword(body.PW) {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "wrong password"})
		return
	}
	tok := config.AuthToken()
	c.SetCookie("tp_auth", tok, 30*24*3600, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"ok": true, "token": tok})
}

// authPasswordAPI sets or clears the operator password. Gate semantics are
// request-time: while set, the middleware already demanded identity; while
// unset this is the first-set path (open, like the rest of the appliance).
func authPasswordAPI(c *gin.Context) {
	var body struct {
		PW string `json:"pw"`
	}
	if strings.HasPrefix(c.ContentType(), "application/json") {
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
			return
		}
	} else {
		body.PW = c.PostForm("pw")
	}
	if err := config.SetAuthPassword(body.PW); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if body.PW == "" {
		c.JSON(http.StatusOK, gin.H{"ok": true, "enabled": false}) // cleared: cookies start failing naturally
		return
	}
	c.SetCookie("tp_auth", config.AuthToken(), 30*24*3600, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"ok": true, "enabled": true, "token": config.AuthToken()})
}

// loginPageHTML — standalone login micro-page (same shape as the degraded
// /settings stub: themed minimal markup, no template registry needed so a
// broken template set can never lock the operator out).
func loginPage(c *gin.Context) {
	next := sanitizeNext(c.Query("next"))
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(loginPageHTML(next)))
}

// sanitizeNext allows only same-site absolute paths ("/c/K7QP-M3XB").
func sanitizeNext(n string) string {
	if n == "" || !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") || strings.Contains(n, "://") {
		return "/"
	}
	return n
}

func loginPageHTML(next string) string {
	return `<!DOCTYPE html><html lang="en" data-theme="blue-future"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>TimerPi — operator login</title>` +
		`<link rel="stylesheet" href="/ftl/dist/blue-future.css">` +
		`<link rel="stylesheet" href="/css/` + assetsRev + `/timerpi.v51.css"></head>` +
		`<body class="app"><main class="main" style="max-width:26rem;margin:8vh auto;padding:0 4vw">` +
		`<h1>OPERATOR LOGIN</h1>` +
		`<p class="text-muted">This appliance has an operator password. The stage display (` +
		`<code>/d/…</code>) needs no login.</p>` +
		`<form id="f">` +
		`<input type="password" id="pw" class="input" autocomplete="current-password" aria-label="Operator password" required autofocus>` +
		`<button type="submit" class="btn btn-primary" style="margin-top:.75rem">Unlock</button>` +
		`</form>` +
		`<p id="err" class="field-error" role="alert"></p>` +
		`<script>` +
		`var next = ` + jsStr(next) + `;` +
		`document.getElementById('f').addEventListener('submit', function (e) {` +
		` e.preventDefault();` +
		` fetch('/api/login', { method: 'POST', headers: {'content-type': 'application/json'},` +
		`   body: JSON.stringify({ pw: document.getElementById('pw').value }) })` +
		` .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })` +
		` .then(function (out) {` +
		`  if (out.ok) {` +
		`   try { localStorage.setItem('tp.atoken', out.j.token); } catch (e) { /* private mode */ }` +
		`   location.href = next;` +
		`  } else {` +
		`   document.getElementById('err').textContent = out.j.error || 'login failed';` +
		`  }` +
		` })` +
		` .catch(function () { document.getElementById('err').textContent = 'network error'; });` +
		`});` +
		`</script></main></body></html>`
}

// jsStr quotes s for inline JS (login pages only ever carry sanitized paths).
func jsStr(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}
