package routes

import (
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"timerpi/config"
)

// OriginGuard is the cross-site guard for the whole HTTP surface.
//
//   - DNS rebinding: see hostAllowed — with the default empty
//     allowed_hosts the server answers to ANY Host (custom domains and
//     reverse-proxy names just work); a non-empty allowed_hosts switches
//     to strict mode where unknown public names are refused with HTTP 421.
//   - CSRF: a state-changing request (anything but GET/HEAD/OPTIONS) whose
//     Origin (or, lacking one, Referer) names a different host is refused.
//     Browsers attach cached Basic-auth credentials to cross-site form
//     posts, so the operator password alone does not stop a hostile page
//     from POSTing /api/... Requests with neither header (curl, scripts,
//     devices on the mesh) are not browser-driven and pass.
func OriginGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !hostAllowed(c.Request.Host) {
			log.Printf("origin: refused Host %q: not in config allowed_hosts (strict mode)", c.Request.Host)
			c.String(http.StatusMisdirectedRequest,
				"Host %q is not allowed. In strict mode, add it to allowed_hosts in %s and restart the service.",
				c.Request.Host, config.ConfigFilePath())
			c.Abort()
			return
		}
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if !sameOriginRequest(c.Request) {
			log.Printf("origin: refused cross-origin %s %s from %s", c.Request.Method, c.Request.URL.Path, c.ClientIP())
			c.String(http.StatusForbidden, "cross-origin request refused")
			c.Abort()
			return
		}
		c.Next()
	}
}

// sameOriginRequest reports whether r's Origin/Referer (when present) names
// r's own Host. Also used by the WebSocket upgrader's CheckOrigin (ws hub
// agent: call routes.SameOriginRequest).
func sameOriginRequest(r *http.Request) bool {
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
		if src == "" {
			return true // not a browser-initiated cross-site request
		}
	}
	if src == "null" {
		return false // sandboxed iframe / opaque origin
	}
	u, err := url.Parse(src)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// SameOriginRequest is exported for the WebSocket upgrader's CheckOrigin.
func SameOriginRequest(r *http.Request) bool {
	return sameOriginRequest(r)
}

// localSuffixes are reserved or non-delegated name suffixes that never
// resolve through public DNS.
var localSuffixes = []string{".local", ".lan", ".home.arpa", ".internal", ".localhost"}

// hostAllowed is the DNS-rebinding allow-list for the Host header.
//
// Semantics (reverse proxies and custom domains are supported):
//
//   - config allowed_hosts EMPTY (the default): open mode — ANY Host
//     passes, so a TimerPi behind Nginx Proxy Manager with arbitrary
//     custom domains works out of the box. The always-local checks below
//     still run first, but nothing is excluded.
//   - config allowed_hosts NON-EMPTY: strict mode — only the listed names
//     (plus the always-local set: IP literals, localhost, dotless names,
//     local-only suffixes, the machine hostname) pass; every other public
//     name is refused with HTTP 421.
func hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if host == "" {
		return true // HTTP/1.0 without Host: not a browser
	}
	// Always-local set, valid in both modes: an attacker's public DNS
	// cannot resolve to these, so they cannot be rebinding vectors. This
	// keeps direct LAN access, mDNS conflict renames (timerpi-2.local) and
	// router names (timerpi.lan) working.
	if net.ParseIP(host) != nil || host == "localhost" {
		return true
	}
	if !strings.Contains(host, ".") {
		return true // dotless names resolve only on the LAN
	}
	for _, suffix := range localSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	if name, err := os.Hostname(); err == nil && name != "" {
		name = strings.ToLower(name)
		if host == name || host == name+".local" {
			return true
		}
	}
	// Strict vs open mode, decided by whether the operator listed anything.
	hosts := config.AllowedHosts()
	if len(hosts) == 0 {
		return true // open mode: custom domains / proxied names allowed
	}
	for _, h := range hosts {
		if strings.EqualFold(strings.TrimSpace(h), host) {
			return true
		}
	}
	return false
}
