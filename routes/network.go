// network.go — device-mesh HTTP surface (Agent H, 2026-10-03).
//
// DELIBERATELY NOT AUTO-REGISTERED: routes.go is owned by the in-flight
// fix wave, so the mount point is a tiny package seam instead. Integration
// adds ONE line in routes.go (or main.go) AFTER the fix wave lands:
//
//	routes.RegisterNetwork(g)   // inside routes.New, before registerStatic
//
// and main.go installs the device through routes.InstallNetwork (&mesh deps).
// Details, including the ws-side subscriptions, live in (an old handoff note, not kept).
// Until both happen the handlers answer 503 and pages 503 — no nil panics.
//
// Global CSRF/origin middleware (r.Use(OriginGuard())) applies to every
// route mounted here — nothing bypasses it.
package routes

import (
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/mesh"
	"timerpi/meshradio"
	"timerpi/update"
	"timerpi/views"
)

// NetworkDeps carries what the mesh handlers need; installed once at boot.
type NetworkDeps struct {
	Device     *mesh.Device
	Updater    *update.Checker // the boot-time updater; the UPDATE button reuses it
	Tmpl       *views.Set
	ReloadTmpl bool // reparses templates per request (matches Deps.ReloadTmpl)
}

// networkDeps is the seam; atomic so a reload never races handlers.
var networkDeps atomic.Pointer[NetworkDeps]

// InstallNetwork wires the mesh device + template set (main.go calls this
// after building routes.Deps; tests may install nil-degraded deps).
func InstallNetwork(nd *NetworkDeps) {
	networkDeps.Store(nd)
}

// RegisterNetwork mounts the mesh endpoints on the router (did NOT run at
// build time — integration owns the call site; see (an old handoff note, not kept)):
//
//	GET  /api/network                 — self identity + detected peers
//	GET  /api/network/example-detect  — peers seen in the last 30 s
//	POST /api/network/hostname        — {name} RFC1123 label; rename + re-announce
//	POST /api/network/role            — {force: auto|primary|member} override
//	GET  /settings                    — device identity page (settings root)
//	GET  /frag/network                — the live mesh panel fragment (htmx poll)
func RegisterNetwork(grp gin.IRouter) {
	nd := networkDeps.Load()
	if nd == nil {
		log.Printf("routes: RegisterNetwork before InstallNetwork — installing degraded handlers (503s)")
	}
	grp.GET("/api/network", networkSelf)
	grp.GET("/api/network/example-detect", networkExampleDetect)
	grp.POST("/api/network/hostname", networkSetHostname)
	grp.POST("/api/network/role", networkSetRole)
	grp.GET("/settings", networkSettingsPage)
	grp.GET("/frag/network", networkFrag)
	grp.POST("/api/network/mesh", networkSetMesh)
	grp.GET("/api/network/update", networkUpdateCheck)
	grp.POST("/api/network/update", networkUpdateApply)
}

// ---------------------------------------------------------------------------
// Handlers

// GET /api/network — our identity (hostname/device name/role/state/epoch/TXT)
// plus the freshly detected peers. 503 until mesh is wired.
func networkSelf(c *gin.Context) {
	nd := networkDeps.Load()
	if nd == nil || nd.Device == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mesh not wired (integration pending)"})
		return
	}
	id, peers := nd.Device.Status()
	c.JSON(http.StatusOK, gin.H{
		"hostname":      id.Hostname,
		"deviceName":    id.DeviceName,
		"port":          id.Port,
		"version":       id.Version,
		"state":         string(id.State),
		"role":          id.Role,
		"override":      id.Override,
		"epoch":         id.Epoch,
		"txt":           id.TXT,
		"primaryHost":   id.PrimaryHost,
		"primaryAddr":   id.PrimaryAddr,
		"primaryEpoch":  id.PrimaryEpoch,
		"lastPrimaryAt": id.LastPrimaryAt,
		"peers":         peers,
	})
}

// GET /api/network/example-detect — peers detected within the last 30 s
// (the settings page's live-list data source; the page itself servers it as
// /frag/network HTML, this stays JSON for scripts).
func networkExampleDetect(c *gin.Context) {
	nd := networkDeps.Load()
	if nd == nil || nd.Device == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mesh not wired (integration pending)"})
		return
	}
	_, peers := nd.Device.Status()
	c.JSON(http.StatusOK, gin.H{
		"window":   mesh.PeersFreshFor.String(),
		"count":    len(peers),
		"detected": peers,
	})
}

// POST /api/network/hostname {"name": …} — validates an RFC1123 label,
// applies it to the machine (hostnamectl → /etc/hostname fallback via
// mesh.Device.Rename) and re-announces mDNS, so <newname>.local works
// immediately. Also stores the display name in config.json. Returns the new
// TXT evidence. htmx callers get the refreshed fragment/validation note.
func networkSetHostname(c *gin.Context) {
	nd := networkDeps.Load()
	if nd == nil || nd.Device == nil {
		apiNetError(c, http.StatusServiceUnavailable, "mesh not wired (integration pending)")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apiNetError(c, http.StatusBadRequest, "body must be {\"name\": …}")
		return
	}
	name := strings.ToLower(strings.TrimSpace(body.Name))

	if err := nd.Device.Rename(name); err != nil {
		// Validation/system errors stay a 200 + error-note fragment for
		// htmx (import-error precedent: feedback box, no panel replaced,
		// no empty error page).
		apiNetError(c, http.StatusBadRequest, err.Error())
		return
	}
	// Keep the operator display name in sync (config.SetDeviceName).
	if serr := config.SetDeviceName(name); serr != nil {
		log.Printf("routes: device name persist after rename: %v", serr)
	}
	id, peers := nd.Device.Status()
	if isHtmx(c) {
		vm := buildNetVM(nd, id, peers)
		vm.OK = "hostname applied — announcing as " + id.Hostname
		renderNetFrag(c, nd, vm, http.StatusOK)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"hostname": id.Hostname,
		"role":     id.Role,
		"txt":      id.TXT, // new announce evidence
		"peers":    peers,
	})
}

// POST /api/network/role {"force": "auto"|"primary"|"member"} — persists and
// applies the override switch at once (mesh_state.role_override).
func networkSetRole(c *gin.Context) {
	nd := networkDeps.Load()
	if nd == nil || nd.Device == nil {
		apiNetError(c, http.StatusServiceUnavailable, "mesh not wired (integration pending)")
		return
	}
	var body struct {
		Force string `json:"force"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apiNetError(c, http.StatusBadRequest, "body must be {\"force\": auto|primary|member}")
		return
	}
	if err := nd.Device.SetOverride(body.Force); err != nil {
		apiNetError(c, http.StatusBadRequest, err.Error())
		return
	}
	id, peers := nd.Device.Status()
	if isHtmx(c) {
		vm := buildNetVM(nd, id, peers)
		vm.OK = "role override set to " + overrideLabel(id.Override)
		renderNetFrag(c, nd, vm, http.StatusOK)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "role": id.Role, "override": id.Override, "state": string(id.State)})
}

// GET /settings — the device identity page (standalone "settings" root; the
// base.html dispatch is home|dashboard and is owned by another agent).
func networkSettingsPage(c *gin.Context) {
	nd := networkDeps.Load()
	if nd == nil || nd.Device == nil || nd.Tmpl == nil {
		// Degraded mode: serve an honest, themed stub instead of a bare
		// 503 string — an operator landing here during a mesh failure must
		// see what happened and how to reach the rest of the appliance.
		c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8",
			[]byte(meshDownHTML(c.Request.Host, "mesh device is not running")))
		return
	}
	id, peers := nd.Device.Status()
	renderNetPage(c, nd, "settings", buildNetVM(nd, id, peers))
}

// meshDownHTML is the degraded-`/settings` page: minimal themed markup, no
// template registry needed (mesh failures may be the cause of their own
// absence). Purple-free (brand tokens unneeded), operator voice.
func meshDownHTML(host, reason string) string {
	return `<!DOCTYPE html><html lang="en" data-theme="xbmc"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>TimerPi — settings</title>` +
		`<link rel="stylesheet" href="/ftl/dist/xbmc.css"></head>` +
		`<body class="app"><main class="app-main"><section class="panel">` +
		`<div class="panel-header">Mesh unavailable</div>` +
		`<div class="empty-state is-offline"><p class="empty-state-title">The device mesh is not running</p>` +
		`<p>Reason: <code>` + htmlEsc(reason) + `</code>. Settings, identity and peer discovery live behind it; everything else keeps working.</p>` +
		`<p>Cue lists and displays are unaffected: ` +
		`<a href="/">show list</a> · <code>http://` + htmlEsc(host) + `/d/&lt;code&gt;</code></p>` +
		`<p class="empty-state-hint">The mesh starts automatically with the service; this page recovers on its own. ` +
		`Check the journal: <code>journalctl -u timerpi | grep mesh</code></p></div>` +
		`</section></main></body></html>`
}

// GET /frag/network — exactly the live mesh panel fragment (htmx polls it
// every 5 s from the settings page; CONTRACT-UI §3 frag-shows pattern).
func networkFrag(c *gin.Context) {
	nd := networkDeps.Load()
	if nd == nil || nd.Device == nil || nd.Tmpl == nil {
		// htmx callers get a 2xx inline note (the poll swaps it in);
		// scrapers get the honest 503.
		if isHtmx(c) {
			c.Data(http.StatusOK, "text/html; charset=utf-8",
				[]byte(netNoteHTML("err", "mesh unavailable — panel recovers when the device mesh restarts")))
			return
		}
		c.String(http.StatusNotImplemented, "mesh settings not wired (integration pending)")
		return
	}
	id, peers := nd.Device.Status()
	renderNetFrag(c, nd, buildNetVM(nd, id, peers), http.StatusOK)
}

// apiNetError answers JSON for API callers and an inline error-note
// fragment for htmx (status stays 2xx for htmx so the note renders).
func apiNetError(c *gin.Context, status int, msg string) {
	if isHtmx(c) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(netNoteHTML("err", msg)))
		return
	}
	c.JSON(status, gin.H{"error": msg})
}

// netNoteHTML builds the tiny status line swapped into #net-note. Hand-
// rolled here (not a template) so handlers can answer without the template
// registry; markup vocabulary identical to frag-net-note.
func netNoteHTML(kind, msg string) string {
	cls := "alert alert-success"
	if kind == "err" {
		cls = "field-error"
	}
	return `<div class="` + cls + `" id="net-note" role="alert">` + htmlEsc(msg) + `</div>`
}

// htmlEsc minimal-escapes into an HTML fragment (no full template needed).
func htmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;")
	return r.Replace(s)
}

// isHtmx detects the htmx request marker header.
func isHtmx(c *gin.Context) bool { return c.GetHeader("HX-Request") == "true" }

// renderNetFragment renders frag-network (-dev per-request reparse like the
// page renderer) as the htmx swap payload. [FIX-L: mid-flight arity fix —
// renderNetPage kept 4-arity (matches its only direct caller); the status
// rides on the Context before the render instead of being a param.]
func renderNetFrag(c *gin.Context, nd *NetworkDeps, vm netVM, status int) {
	c.Status(status)
	renderNetPage(c, nd, "frag-network", vm)
}

// renderNetPage executes a mesh-owned template with -dev reparse support
// (mirrors Deps.render / pages.go for the mesh surface).
func renderNetPage(c *gin.Context, nd *NetworkDeps, name string, vm netVM) {
	tmpl := nd.Tmpl
	if nd.ReloadTmpl {
		if t, err := views.New("templates"); err == nil {
			tmpl = t
		} else {
			log.Printf("routes: network page dev reparse: %v", err)
		}
	}
	if err := tmpl.Render(c.Writer, name, vm); err != nil {
		// Headers may already be out; a blank settings page is a bug.
		log.Printf("routes: network template %q: %v", name, err)
	}
}

// ---------------------------------------------------------------------------
// View models (settings.html + fragments/settings.html; CONTRACT-UI style)

// netPeerVM is one peer row of the settings live table.
type netPeerVM struct {
	Host      string
	Addr      string
	Role      string
	Epoch     string
	Age       string
	IsPrimary bool
	Ver       string
	Foreign   bool // another protocol major: not part of this mesh's election
}

// netVM is the dot for the settings page and the frag-network fragment.
type netVM struct {
	Page         string
	Title        string
	Hostname     string
	DeviceName   string
	DefaultTheme string // B7: appliance fallback (blue-future)
	Role         string
	State        string
	Override     string // "auto"|"primary"|"member" ("" normalized to auto)
	Epoch        string
	Version      string
	Port         int
	TXT          string // joined k=v line for display
	Primary      string // "host (addr)" or ""
	PrimaryEpoch string
	Peers        []netPeerVM
	OK           string
	Err          string
	// Venue mesh (VENUE-CLOUD §11): radio status from timerpi-mesh, the
	// radio settings, and whether a newer box needs this one to update.
	UpdateNeeded  bool
	Mesh          meshradio.Status
	MeshRan       bool
	Country       string
	Ch24, Ch5     int
	Mesh5Channels []int
	Ch24Options   []int
}

// buildNetVM maps the device identity + peer views into the template shape.
func buildNetVM(nd *NetworkDeps, id mesh.Identity, peers []mesh.PeerView) netVM {
	override := id.Override
	if strings.TrimSpace(override) == "" {
		override = mesh.OverrideAuto
	}
	vm := netVM{
		Page:         "settings",
		Title:        "Network",
		Hostname:     id.Hostname,
		DeviceName:   id.DeviceName,
		DefaultTheme: config.DefaultTheme(),
		Role:         id.Role,
		State:        string(id.State),
		Override:     override,
		Epoch:        fmtInt(id.Epoch),
		Version:      id.Version,
		Port:         id.Port,
		Primary:      id.PrimaryHost,
		PrimaryEpoch: fmtInt(id.PrimaryEpoch),
	}
	if id.PrimaryAddr != "" {
		vm.Primary = id.PrimaryHost + " (" + id.PrimaryAddr + ")"
	}
	if id.TXT != nil {
		var parts []string
		for _, k := range []string{"host", "role", "ver", "epoch"} {
			if v, ok := id.TXT[k]; ok {
				parts = append(parts, k+"="+v)
			}
		}
		vm.TXT = strings.Join(parts, " · ")
	}
	vm.UpdateNeeded = id.UpdateNeeded
	vm.Mesh, vm.MeshRan = meshradio.Load(meshStatusFile)
	vm.Country, vm.Ch24, vm.Ch5 = config.MeshRadio()
	vm.Mesh5Channels = config.Mesh5Channels
	for ch := 1; ch <= 13; ch++ {
		vm.Ch24Options = append(vm.Ch24Options, ch)
	}
	vm.Peers = make([]netPeerVM, 0, len(peers))
	for _, p := range peers {
		addr := ""
		if len(p.Addrs) > 0 {
			addr = p.Addrs[0]
		}
		vm.Peers = append(vm.Peers, netPeerVM{
			Host:      p.Host,
			Addr:      addr,
			Role:      p.Role,
			Epoch:     fmtInt(p.Epoch),
			Age:       ageFmt(p.AgeS),
			IsPrimary: p.Role == "primary" && !p.Foreign,
			Ver:       p.Ver,
			Foreign:   p.Foreign,
		})
	}
	return vm
}

// overrideLabel names the override for the UI note.
func overrideLabel(v string) string {
	if v == "" {
		return "auto"
	}
	return v
}

// fmtInt renders epoch-ms values for the mono column.
func fmtInt(v int64) string {
	if v == 0 {
		return "—"
	}
	return fmtNum(v)
}

// fmtNum is a plain int→string (no reflection tricks needed here).
func fmtNum(v int64) string {
	return strconvItoa(v)
}

// strconvItoa avoids importing strconv for a single call site.
func strconvItoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ageFmt humanizes last-seen ages for the peers table.
func ageFmt(sec int64) string {
	switch {
	case sec <= 0:
		return "live"
	case sec < 60:
		return strconvItoa(sec) + "s ago"
	default:
		return strconvItoa(sec/60) + "m ago"
	}
}

// netDepsTimeout is a compile-time anchor for the unused-import linter if
// the time package's role changes; handlers never block beyond mesh's own
// harvest deadline (mesh device owns its timeouts).
var _ = time.Now

// meshStatusFile is where timerpi-mesh writes the radio status (a var so
// tests can point it elsewhere).
var meshStatusFile = meshradio.StatusFile

// POST /api/network/mesh {country, ch24, ch5} — the venue mesh radio
// settings (box password). timerpi-mesh applies them at the next boot.
func networkSetMesh(c *gin.Context) {
	var body struct {
		Country string `json:"country"`
		Ch24    int    `json:"ch24"`
		Ch5     int    `json:"ch5"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		apiNetError(c, http.StatusBadRequest, "send {country, ch24, ch5}")
		return
	}
	if err := config.SetMeshRadio(body.Country, body.Ch24, body.Ch5); err != nil {
		apiNetError(c, http.StatusBadRequest, err.Error())
		return
	}
	country, ch24, ch5 := config.MeshRadio()
	c.JSON(http.StatusOK, gin.H{"ok": true, "country": country, "ch24": ch24, "ch5": ch5})
}
