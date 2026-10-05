// Package routes — setup.go: the first-run wizard + printable connect sheet
// + whole-day show-file export/import (Agent I).
//
// Goal: make turn-on and day-swap trivial. Boot a TimerPi, open
// http://<hostname>.local → the operator lands on the wizard (see the
// GET / welcome-branch handoff in reviews/NOTES-setup.md), names the device,
// creates the day's show (typed OR imported OR empty), and is dropped
// straight into the control room with a printable QR connect sheet. Moving a
// whole day between TimerPis is GET /api/shows/:id/file → POST
// /api/shows/import-file.
//
// Parallel-agent ownership: this file owns its endpoints and is mounted by
// the ONE registration line for the consolidation pass into routes.go (see
// reviews/NOTES-setup.md for the exact diff). routes.go / pages.go /
// index.html stay untouched here.
//
// Endpoints brought up here:
//
//	GET  /setup                     wizard page (redirects to / when done)
//	GET  /setup/sheet?show=<code>   printable QR connect sheet (dark card)
//	POST /api/setup/identity        {name} → device name (H route w/ fallback)
//	POST /api/setup/create          {title?} → new show → finish step
//	POST /api/setup/import          multipart {file, title?} → show + cues
//	GET  /api/shows/:ident/file     whole-day JSON bundle (.timerpi.json)
//	POST /api/shows/import-file     accepts that bundle → new show
package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/importdocs"
	"timerpi/timerpi"
	"timerpi/views"
)

// ------------------------------------------------------------------ wiring --

// RegisterSetup mounts the wizard + QR-sheet + show-file endpoints. The
// consolidation agent calls this once from routes.New, right after
// registerAPI:
//
//	registerAPI(r, d)
//	RegisterSetup(r, d) // ← the one line
func RegisterSetup(r gin.IRouter, d *Deps) {
	if d == nil {
		return
	}
	r.GET("/setup", d.setupPage)
	r.GET("/setup/sheet", d.setupSheet)
	r.POST("/api/setup/identity", d.setupIdentity)
	r.POST("/api/setup/create", d.setupCreate)
	r.POST("/api/setup/import", d.setupImport)

	// Whole-day show file (export/import for day-swap between TimerPis).
	// :ident — Agent L scope change: same code-only param name as the rest
	// of /api/shows/… (a differing wildcard name here would panic gin's
	// route tree at Register time).
	r.GET("/api/shows/:ident/file", d.apiShowFile)
	r.POST("/api/shows/import-file", d.apiImportShowFile)
}

// NeedsSetup is the middleware-free first-run probe: the wizard only has
// work when the shows table is empty. Safe on nil wiring (returns false —
// "done" degrades to no-loop on /).
func (d *Deps) NeedsSetup() bool {
	if d.Store == nil {
		return false
	}
	rows, err := d.Store.ListShows()
	if err != nil {
		return false
	}
	return len(rows) == 0
}

// HandleFirstRun is the one-line welcome branch for routes/pages.go's
// homePage (documented handoff in reviews/NOTES-setup.md): consumed the
// request with a redirect to the wizard → true, else false.
// ?skip=1 renders the normal homepage anyway.
func (d *Deps) HandleFirstRun(c *gin.Context) bool {
	if !d.NeedsSetup() || c.Query("skip") != "" {
		return false
	}
	c.Redirect(http.StatusTemporaryRedirect, "/setup")
	return true
}

// ------------------------------------------------------------------- types --

// setupData is the dot for templates/setup.html + fragments/setup-*.html
// (a self-contained shape, not views.PageData — the wizard root is a
// standalone document like display.html, and its steps are fragments).
type setupData struct {
	Step  string // "identity" | "content" | "finish"
	Title string // <title> prefix
	Err   string // step error, human-phrased (html-escaped by the template)

	// Appliance default theme (B7): blue-future unless configured.
	DefaultTheme string

	Device string // friendly device name (config → OS)
	Host   string // "<device>.local" mDNS hint
	Port   int
	Base   string // "http://<lan-ip>:<port>" for absolute links + QR payloads

	ShowID    int64
	ShowCode  string // public share code (Agent L): public links use ONLY this
	ShowTitle string
	CueCount  int
	CtrlURL   string // absolute control-room URL (CODE-based)
	DispURL   string // absolute display URL (CODE-based)
	SheetURL  string // absolute printable-sheet URL (?show=<code>)
}

// wizardStepsValid is the allowed step query value set on GET /setup.
var wizardStepsValid = map[string]bool{"identity": true, "content": true, "finish": true}

// -------------------------------------------------------- wizard page root --

// setupPage is GET /setup: the first-run wizard. When setup is done the
// wizard hands the operator to the homepage (redirect) — reopening
// deliberately is ?force=1.
func (d *Deps) setupPage(c *gin.Context) {
	if !d.NeedsSetup() && c.Query("force") == "" {
		c.Redirect(http.StatusTemporaryRedirect, "/")
		return
	}
	data := d.wizardData()
	data.Step = c.DefaultQuery("step", "identity")
	if !wizardStepsValid[data.Step] {
		data.Step = "identity"
	}
	d.render(c, "setup", data)
}

// wizardData builds the common dot (device/hostname/base URLs) shared by the
// wizard steps and the connect sheet.
func (d *Deps) wizardData() setupData {
	port := config.HTTPPort()
	base := fmt.Sprintf("http://%s:%d", localAddress(), port)
	dev := hostname()
	host := strings.TrimSpace(dev) + ".local"
	if dev == "" {
		host = ""
	}
	return setupData{
		Device:       dev,
		Host:         host,
		Port:         port,
		Base:         base,
		DefaultTheme: config.DefaultTheme(),
	}
}

// --------------------------------------------------------------------------
// Step a — identity: device name + hostname note.
//
// The system hostname + mDNS re-announce route (Agent H's
// POST /api/network/hostname) is attempted first so the wizard gains full
// behavior the moment it lands; until then the call falls back to the
// config device name (config.SetDeviceName) so the wizard always works.

type identityBody struct {
	Name string `json:"name"`
}

// setupIdentity is POST /api/setup/identity {name} — read from the form
// (htmx) or JSON. Answers the NEXT step's fragment for the htmx swap;
// errors ride in .Err, never a bare error page (CONTRACT-UI rule).
func (d *Deps) setupIdentity(c *gin.Context) {
	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		var b identityBody
		_ = c.ShouldBindJSON(&b)
		name = strings.TrimSpace(b.Name)
	}
	data := d.wizardData()
	data.Step = "content"
	data.Title = "Set up TimerPi"
	if name != "" {
		if err := d.applyIdentity(name); err != nil {
			log.Printf("setup: identity save: %v", err)
			data.Err = "Device name couldn't be saved — continuing without it."
		} else {
			data.Device = name
			data.Host = name + ".local"
		}
	}
	d.renderSetupFrag(c, "frag-setup-content", data)
}

// applyIdentity renames the appliance: H's route when registered (OS
// hostname + mDNS re-announce), the config device-name as the always-works
// fallback.
func (d *Deps) applyIdentity(name string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	// mirror the field H is expected to take; the extra "name" key is harmless.
	payload := fmt.Sprintf(`{"hostname":%q,"name":%q}`, name, name)
	res, err := client.Post(
		fmt.Sprintf("http://127.0.0.1:%d/api/network/hostname", config.HTTPPort()),
		"application/json", strings.NewReader(payload))
	if err == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode/100 == 2 {
			return nil // H's route lives: it owns the OS hostname + mDNS
		}
		// 404 (route not landed yet) or an H-side error → config fallback.
	}
	return config.SetDeviceName(name)
}

// --------------------------------------------------------------------------
// Step b — content: create a new show by title, create-by-import (one
// multipart POST does show + cue application server-side), or start empty.

// setupCreate is POST /api/setup/create — start a fresh day.
// title (form OR JSON); "" → "Untitled show". Answers the finish fragment.
func (d *Deps) setupCreate(c *gin.Context) {
	if !d.setupDBOk() {
		c.String(http.StatusServiceUnavailable, "db wiring missing")
		return
	}
	title := strings.TrimSpace(c.PostForm("title"))
	if title == "" {
		var b identityBody
		_ = c.ShouldBindJSON(&b)
		title = strings.TrimSpace(b.Name) // accept {name:"…"} too — same intent
	}
	if title == "" {
		title = "Untitled show"
	}

	data := d.wizardData()
	data.Title = "Set up TimerPi"
	show, err := d.freshShow(title, nil)
	if err != nil {
		data.Step = "content"
		data.Err = "The show couldn't be created: " + err.Error()
		d.renderSetupFrag(c, "frag-setup-content", data)
		return
	}
	data.Step = "finish"
	data.fillFinish(show, 0)
	d.renderSetupFrag(c, "frag-setup-finish", data)
}

// setupImport is POST /api/setup/import — ONE multipart POST creates the
// show AND applies the document (xlsx|xls|csv|json auto-detected by
// extension, same importdocs path as the dashboard import; a .timerpi.json
// whole-day bundle is auto-detected and routed to the show-file importer).
// Fields: file (required), title (optional — defaults to the file name).
func (d *Deps) setupImport(c *gin.Context) {
	if !d.setupDBOk() {
		c.String(http.StatusServiceUnavailable, "db wiring missing")
		return
	}
	data := d.wizardData()
	data.Title = "Set up TimerPi"
	data.Step = "content"

	raw, fname, terr := setupUpload(c)
	if terr != "" {
		data.Err = terr
		d.renderSetupFrag(c, "frag-setup-content", data)
		return
	}

	// Whole-day bundle? It carries title + cues + messages + schedule.
	if isShowFileBundle(raw) {
		show, n, merr := d.importShowFile(raw, titleFromFormOrName(c, fname))
		if merr != nil {
			data.Err = merr.Error()
			d.renderSetupFrag(c, "frag-setup-content", data)
			return
		}
		data.Step = "finish"
		data.fillFinish(show, n)
		d.renderSetupFrag(c, "frag-setup-finish", data)
		return
	}

	// Ordinary cue document: importdocs parse → domain cues; verbatim
	// row errors (importdocs numbering) surface to the operator.
	tcs, perr := parseCueDoc(raw, fname)
	if perr != nil {
		data.Err = perr.Error()
		d.renderSetupFrag(c, "frag-setup-content", data)
		return
	}
	title := titleFromFormOrName(c, fname)
	if title == "" {
		title = "Imported show"
	}
	show, err := d.freshShow(title, tcs)
	if err != nil {
		data.Err = "The show was created but the cues couldn't be applied: " + err.Error()
		d.renderSetupFrag(c, "frag-setup-content", data)
		return
	}
	data.Step = "finish"
	data.fillFinish(show, len(tcs))
	d.renderSetupFrag(c, "frag-setup-finish", data)
}

// setupUpload reads the multipart "file" field (wizard ergonomics: errors
// come back as human copy inside the fragment, not 4xx pages).
func setupUpload(c *gin.Context) (raw []byte, filename string, errText string) {
	fh, ferr := c.FormFile("file")
	if ferr != nil {
		return nil, "", `Choose a cue document (field "file") — or pick "start with an empty show" instead.`
	}
	f, oerr := fh.Open()
	if oerr != nil {
		return nil, "", "The upload is unreadable — please try again."
	}
	defer f.Close()
	raw, rerr := io.ReadAll(f)
	if rerr != nil {
		return nil, "", "The upload is unreadable — please try again."
	}
	return raw, fh.Filename, ""
}

func titleFromFormOrName(c *gin.Context, fname string) string {
	if t := strings.TrimSpace(c.PostForm("title")); t != "" {
		return t
	}
	if base := strings.TrimSuffix(fname, filepath.Ext(fname)); base != "" {
		if base != "untitled" {
			return base
		}
	}
	return ""
}

// parseCueDoc is the shared importdocs call path (parse → domain cues), the
// same normalization the dashboard import uses (routes/import.go).
func parseCueDoc(raw []byte, name string) ([]timerpi.Cue, error) {
	cues, perr := importdocs.ParseFilename(raw, name)
	var tcs []timerpi.Cue
	if len(cues) > 0 {
		tcs, perr = importdocs.ToTimerpiCues(cues)
	}
	if perr == nil && len(tcs) == 0 {
		perr = importError{msg: "no cues found in the document"}
	}
	return tcs, perr
}

// freshShow creates the show, applies the (optional) opening cue list, and
// warms the engine so the ticker owns it from creation. Returns the row
// (Agent L: its Code feeds the finish-step / sheet links — code-only
// public addressing).
func (d *Deps) freshShow(title string, cues []timerpi.Cue) (timerpi.Show, error) {
	show, err := d.Store.CreateShow(title)
	if err != nil {
		return timerpi.Show{}, err
	}
	if len(cues) > 0 {
		if cerr := d.Store.ReplaceCues(show.ID, cues); cerr != nil {
			return show, fmt.Errorf("cues: %w", cerr)
		}
	}
	d.warmShow(show.ID)
	return show, nil
}

// warmShow builds the engine (ticker seeder picks it up) and re-broadcasts.
func (d *Deps) warmShow(id int64) {
	if d.Engines == nil {
		return
	}
	eng, gerr := d.Engines.Get(id)
	if gerr != nil {
		log.Printf("setup: engine warmup for show %d: %v", id, gerr)
		return
	}
	if nerr := eng.Notify(); nerr != nil {
		log.Printf("setup: notify show %d: %v", id, nerr)
	}
}

// fillFinish populates the finish-step dot. Agent L (scope change):
// CtrlURL/DispURL/SheetURL are built from the show's CODE — the numeric
// id is bookkeeping only.
func (data *setupData) fillFinish(show timerpi.Show, cueCount int) {
	data.ShowID = show.ID
	data.ShowCode = timerpi.NormalizeCode(show.Code)
	data.ShowTitle = show.Title
	data.CueCount = cueCount
	if data.ShowCode == "" {
		return // code-less row: the fragment placeholders hold the panel
	}
	data.CtrlURL = fmt.Sprintf("%s/c/%s", data.Base, data.ShowCode)
	data.DispURL = fmt.Sprintf("%s/d/%s", data.Base, data.ShowCode)
	data.SheetURL = fmt.Sprintf("%s/setup/sheet?show=%s", data.Base, data.ShowCode)
}

// ---------------------------------------------------------------- fragments --

// renderSetupFrag answers a wizard fragment for the htmx swap; -dev keeps
// pages.go's reparse behavior.
func (d *Deps) renderSetupFrag(c *gin.Context, name string, data setupData) {
	tmpl := d.Tmpl
	if tmpl == nil {
		c.String(http.StatusNotImplemented, "template registry missing (mismatched build)")
		return
	}
	if d.ReloadTmpl {
		if t, err := views.New(findDir("templates")); err == nil {
			tmpl = t
		}
	}
	html, err := tmpl.Fragment(name, data)
	if err != nil {
		log.Printf("setup: template %s: %v", name, err)
		c.String(http.StatusInternalServerError, "fragment rendering failed")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// setupDBOk guards mutation endpoints on missing wiring.
func (d *Deps) setupDBOk() bool { return d.Store != nil && d.Engines != nil }

// ------------------------------------------------------ printable sheet --

// setupSheet is GET /setup/sheet?show=<code> — the print-optimized A5 card:
// operator (control) + TV (display) QR codes, the .local hint, an ftl-themed
// dark card with a white print stylesheet (scoped inside templates/setup.html).
// Agent L (scope change): ?show= takes the share CODE only; a numeric id
// is refused (it is no longer an address).
func (d *Deps) setupSheet(c *gin.Context) {
	ident := strings.TrimSpace(c.Query("show"))
	if ident == "" {
		c.String(http.StatusBadRequest, "sheet needs ?show=<code>")
		return
	}
	if !d.setupDBOk() {
		c.String(http.StatusServiceUnavailable, "db/engine wiring missing")
		return
	}
	id, ok := d.resolveIdent(ident)
	if !ok {
		c.String(http.StatusNotFound, "Unknown session code")
		return
	}
	if d.Engines == nil {
		c.String(http.StatusServiceUnavailable, "engine wiring missing")
		return
	}
	eng, gerr := d.Engines.Get(id)
	if gerr != nil {
		c.String(http.StatusNotFound, "Unknown session code")
		return
	}
	snap, serr := eng.Snapshot()
	if serr != nil {
		c.String(http.StatusNotFound, "Unknown session code")
		return
	}
	data := d.wizardData()
	data.fillFinish(snap.Show, len(snap.Cues))
	data.Title = "Connect sheet · " + snap.Show.Title
	d.render(c, "setup-sheet", data)
}

// ------------------------------------------------------ LAN address probe --

// localAddress finds the LAN address the appliance serves on (QR-sheet
// targets). Default-route probe first (a UDP "Dial" connects in the kernel
// only — no packet is sent — and picks the egress interface), then an
// RFC1918/private interface sweep, then the OS hostname, then localhost.
// TIMERPI_LAN_ADDR overrides (exotic routers / tests).
func localAddress() string {
	if a := strings.TrimSpace(os.Getenv("TIMERPI_LAN_ADDR")); a != "" {
		return a
	}
	if ip, ok := defaultRouteIP(); ok {
		return ip
	}
	if ip := firstLANIP(); ip != "" {
		return ip
	}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		return hn // best-effort: named hosts usually also answer on .local
	}
	return "localhost"
}

// defaultRouteIP probes the default route without emitting traffic.
func defaultRouteIP() (string, bool) {
	c, err := net.Dial("udp", "9.9.9.9:53") // DNS root anycast; no packets sent
	if err != nil {
		return "", false
	}
	defer c.Close()
	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || !usefulIP(addr.IP) {
		return "", false
	}
	return addr.IP.String(), true
}

// firstLANIP sweeps interfaces: first UP non-loopback IPv4 (private first).
func firstLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var pub string
	for _, it := range ifaces {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, aerr := it.Addrs()
		if aerr != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			v4 := ip.To4()
			if v4 == nil || !usefulIP(v4) {
				continue
			}
			if v4.IsPrivate() || v4.IsLinkLocalUnicast() {
				return v4.String()
			}
			if pub == "" {
				pub = v4.String()
			}
		}
	}
	return pub
}

// usefulIP filters loopback/unspec/link-local v4 addresses.
func usefulIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsUnspecified() &&
		!ip.IsLinkLocalUnicast()
}

// NOTE on link-local: appliance LANs are DHCP here (PLAN.md), so link-local
// addresses (169.254/16, zeroconf timerpi-named) beat routing drawers; the
// default-route probe wins over both anyway when a route exists.

// ------------------------------------------------------ whole-day show file --

// showFileVersion is the .timerpi.json bundle digest version.
const showFileVersion = 1

// showFile is the whole-day export/import bundle — portable fields ONLY.
// Cue/message DB ids and show ids are dropped on the wire (re-created on
// import; stable Pos is the run order). The playhead/held state is
// intentionally non-portable: a day moving between TimerPis lands ARMED.
// Rate + DayStartTS travel in schedule (the operator's rehearsal pacing and
// day-bar anchor carry over; shownAt survives for messages).
type showFile struct {
	ManifestVersion int               `json:"manifestVersion"`
	ExportedAt      int64             `json:"exportedAt"`
	Show            showFileTitle     `json:"show"`
	Cues            []timerpi.Cue     `json:"cues"`
	Messages        []timerpi.Message `json:"messages"`
	Schedule        showFileSchedule  `json:"schedule"`
}

type showFileTitle struct {
	Title string `json:"title"`
}

type showFileSchedule struct {
	DayStartTS int64   `json:"dayStartTS"`
	Rate       float64 `json:"rate,omitempty"`
}

// apiShowFile is GET /api/shows/:ident/file — dump the whole day as
// .timerpi.json. READS ONLY: exported store funcs (GetShow/ListCues/
// ListMessages/LoadRuntime) + the engine snapshot's show title — no db.go
// edits (gap note: cues/messages written straight from the store are the
// full rows; the PROTOCOL snapshot shape lacks message shownAt for hidden
// messages, so the store listing is what round-trips them — see
// NOTES-setup.md).
func (d *Deps) apiShowFile(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	show, gerr := d.Store.GetShow(id)
	if gerr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such show"})
		return
	}
	cues, cerr := d.Store.ListCues(id)
	if cerr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": cerr.Error()})
		return
	}
	msgs, _ := d.Store.ListMessages(id)
	rt, _, _ := d.Store.LoadRuntime(id)

	out := make([]timerpi.Cue, 0, len(cues))
	for _, cue := range cues {
		cue.ID = 0     // re-numbered on import
		cue.ShowID = 0 // re-bound to the new show
		out = append(out, cue)
	}
	msgOut := make([]timerpi.Message, 0, len(msgs))
	for _, m := range msgs {
		m.ShowID = 0
		msgOut = append(msgOut, m)
	}
	sched := showFileSchedule{DayStartTS: rt.DayStartTS}
	if rt.Rate > 0 {
		sched.Rate = rt.Rate
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`,
		showFileFilename(show.ID, show.Title)))
	c.JSON(http.StatusOK, showFile{
		ManifestVersion: showFileVersion,
		ExportedAt:      time.Now().UnixMilli(),
		Show:            showFileTitle{Title: show.Title},
		Cues:            out,
		Messages:        msgOut,
		Schedule:        sched,
	})
}

// showFileSafeRe sanitizes the download name.
var showFileSafeRe = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

func showFileFilename(id int64, title string) string {
	base := showFileSafeRe.ReplaceAllString(strings.TrimSpace(title), "_")
	base = strings.Trim(base, " ._")
	if base == "" {
		base = fmt.Sprintf("show-%d", id)
	}
	if len(base) > 60 {
		base = base[:60]
	}
	return base + ".timerpi.json"
}

// apiImportShowFile is POST /api/shows/import-file — accept a .timerpi.json
// bundle (multipart field "file", or a raw application/json body) and
// CREATE a new show containing all cues + messages + schedule anchor.
// Day-swap is then: export there, upload here, open /c/:id.
func (d *Deps) apiImportShowFile(c *gin.Context) {
	if !d.setupDBOk() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db wiring missing"})
		return
	}
	raw, ferr := bundleBody(c)
	if ferr != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": ferr})
		return
	}
	show, count, ierr := d.importShowFile(raw, "")
	if ierr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ierr.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":       show.ID,
		"code":     timerpi.NormalizeCode(show.Code),
		"title":    show.Title,
		"cueCount": count,
		"control":  "/c/" + timerpi.NormalizeCode(show.Code),
		"display":  "/d/" + timerpi.NormalizeCode(show.Code),
	})
}

// bundleBody reads the bundle bytes (multipart "file" preferred, JSON body
// fallback for scripts).
func bundleBody(c *gin.Context) ([]byte, string) {
	if fh, ferr := c.FormFile("file"); ferr == nil {
		f, oerr := fh.Open()
		if oerr != nil {
			return nil, "file unreadable"
		}
		defer f.Close()
		raw, rerr := io.ReadAll(f)
		if rerr != nil {
			return nil, "file unreadable"
		}
		return raw, ""
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil, `send the bundle as multipart field "file" (or an application/json body)`
	}
	return raw, ""
}

// isShowFileBundle sniffs whether an uploaded file is a whole-day bundle
// (the wizard's file field takes any format; bundles are routed to the
// show-file importer instead of the cue parser): a `{"manifestVersion"` head,
// optionally BOM-prefixed.
func isShowFileBundle(raw []byte) bool {
	head := bytes.TrimLeft(bytes.TrimLeft(raw, "\xef\xbb\xbf"), " \t\r\n")
	return bytes.HasPrefix(head, []byte(`{"manifestVersion"`))
}

// importShowFile parses a bundle and creates the show (shared by
// /api/shows/import-file and the wizard's file drop). Exported store funcs
// ONLY: CreateShow / ReplaceCues / CreateMessage / ShowMessage / SaveRuntime
// / TouchShow.
func (d *Deps) importShowFile(raw []byte, fallbackTitle string) (timerpi.Show, int, error) {
	var sf showFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return timerpi.Show{}, 0, fmt.Errorf("not a TimerPi show file: %v", err)
	}
	if sf.ManifestVersion != showFileVersion {
		return timerpi.Show{}, 0, fmt.Errorf(
			"unsupported show file version %d (want %d)", sf.ManifestVersion, showFileVersion)
	}

	title := strings.TrimSpace(sf.Show.Title)
	if title == "" {
		title = strings.TrimSpace(fallbackTitle)
	}
	if title == "" {
		title = "Imported show"
	}
	show, err := d.Store.CreateShow(title)
	if err != nil {
		return timerpi.Show{}, 0, err
	}

	for i := range sf.Cues {
		sf.Cues[i].ID = 0
		sf.Cues[i].ShowID = 0
	}
	if err := d.Store.ReplaceCues(show.ID, sf.Cues); err != nil {
		return show, 0, fmt.Errorf("cues: %w", err)
	}
	for _, m := range sf.Messages {
		m.ShowID = 0
		nm, cerr := d.Store.CreateMessage(show.ID, m.Text, m.Color)
		if cerr != nil {
			continue
		}
		if m.ShownAt > 0 {
			_ = d.Store.ShowMessage(show.ID, nm.ID, m.ShownAt)
		}
	}
	// Schedule anchor + rate restore as an ARMED day: no playhead travels.
	rt := timerpi.Runtime{
		ShowID:     show.ID,
		Rate:       sf.Schedule.Rate,
		DayStartTS: sf.Schedule.DayStartTS,
	}
	if rt.Rate <= 0 {
		rt.Rate = timerpi.DefaultRate
	}
	if err := d.Store.SaveRuntime(rt); err != nil {
		return show, len(sf.Cues), fmt.Errorf("runtime: %w", err)
	}
	_ = d.Store.TouchShow(show.ID)
	d.warmShow(show.ID)
	return show, len(sf.Cues), nil
}
