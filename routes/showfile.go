// Package routes — showfile.go: the whole-room show file (.timerpi.json).
//
//	GET  /api/shows/:ident/file          export one room (moderator)
//	POST /api/events/:code/rooms/import  import a room into an event (events.go)
//
// Also the LAN-address probe used for absolute share/QR URLs.
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
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/boards"
	"timerpi/timerpi"
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
	// Whole-room show file export (moderator); import lives on the event
	// (POST /api/events/:code/rooms/import, SuperOperator).
	r.GET("/api/shows/:ident/file", d.apiShowFile)
}

// ------------------------------------------------------------------- types --

// -------------------------------------------------------- wizard page root --

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

// --------------------------------------------------------------------------
// Step b — content: create a new show by title, create-by-import (one
// multipart POST does show + cue application server-side), or start empty.

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

// ---------------------------------------------------------------- fragments --

// setupDBOk guards mutation endpoints on missing wiring.
func (d *Deps) setupDBOk() bool { return d.Store != nil && d.Engines != nil }

// ------------------------------------------------------ printable sheet --

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
const (
	showFileVersionV1 = 1                 // cues+messages+schedule (importing stays supported)
	showFileVersionV2 = 2                 // §11.9 full fidelity (polls/votes/screens/boards/presets/zone+map)
	showFileVersion   = showFileVersionV2 // what we EXPORT today
)

// appVersion is the product version string (PLAN v2 "Rooms"; §11.3 phase 7).
func appVersion() string { return "2.0" }

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
	// §11.9 full fidelity (v2, ZIP): the whole event rides the bundle.
	// MapIndex is the event's venue map (→ Assets[i-1]). Zone and
	// ZoneMapIndex are read from pre-event bundles only, never written
	// (STATUS C10): their map becomes the event map when it has none.
	MapIndex     int64              `json:"mapIndex,omitempty"`
	Zone         string             `json:"zone,omitempty"`
	ZoneMapIndex int64              `json:"zoneMapIndex,omitempty"`
	Polls        []showFilePoll     `json:"polls,omitempty"`
	Votes        []showFileVote     `json:"votes,omitempty"`
	Screens      []showFileScreen   `json:"screens,omitempty"`
	Boards       []showFileBoard    `json:"boards,omitempty"`
	Presets      []showFilePreset   `json:"presets,omitempty"`
	Assets       []showFileAssetRef `json:"assets,omitempty"`
	Version      string             `json:"generator,omitempty"`
}

// showFilePoll — one interaction item, wire-shaped for the bundle
// (Poll's db tags hide Options/Parent/Author from JSON on purpose).
type showFilePoll struct {
	ID       int64  `json:"id"` // export-local 1..N
	Kind     string `json:"kind"`
	Question string `json:"question"`
	Options  string `json:"options,omitempty"`
	Correct  int64  `json:"correct"`
	State    string `json:"state"`
	Parent   int64  `json:"parent"` // export-local
	Author   string `json:"author,omitempty"`
	Ts       int64  `json:"ts,omitempty"`
	// Push targets, Q&A spotlight (export-local child id) and auto-approve.
	ToAudience  bool  `json:"toAudience,omitempty"`
	ToPresenter bool  `json:"toPresenter,omitempty"`
	Spot        int64 `json:"spot,omitempty"`
	AutoApprove bool  `json:"autoApprove,omitempty"`
}

// showFileVote — one vote row keyed by poll export id (§11.9).
type showFileVote struct {
	PollID int64  `json:"pollId"`
	Peer   string `json:"peer"`
	Choice string `json:"choice"`
	Ts     int64  `json:"ts"`
}

// showFileScreen / Board / Preset / AssetRef — registry + layout payloads.
type showFileScreen struct {
	Name    string `json:"name"`
	Theme   string `json:"theme"`
	Room    string `json:"room"`
	BoardID int64  `json:"boardId"`
}

type showFileBoard struct {
	ID     int64           `json:"id"` // export-local id (screens reference it)
	Name   string          `json:"name"`
	Layout json.RawMessage `json:"layout"`
}

type showFilePreset struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

type showFileAssetRef struct {
	ID   int64  `json:"id"` // export-local 1..N
	Name string `json:"name"`
	Mime string `json:"mime"`
	Data string `json:"data"` // data URL (mime;base64) — ≤4 MiB by upload cap
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
// (an old handoff note, not kept)).
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
	// §11.9: the whole event rides the bundle — polls (with moderation
	// state), their votes, the screens registry, boards, presets, the zone
	// label and its map asset (data-URL'd images; ≤4 MiB each by the
	// upload cap, so JSON stays honest) plus the generator version.
	sf := showFile{
		ManifestVersion: showFileVersionV2,
		ExportedAt:      time.Now().UnixMilli(),
		Show:            showFileTitle{Title: show.Title},
		Cues:            out,
		Messages:        msgOut,
		Schedule:        sched,
		Version:         appVersion(),
	}
	d.fillBundleExtras(id, &sf)
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`,
		showFileFilename(show.ID, show.Title)))
	c.JSON(http.StatusOK, sf)
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

// importShowFile parses a bundle and creates the show (shared by
// /api/shows/import-file and the wizard's file drop). Exported store funcs
// ONLY: CreateShow / ReplaceCues / CreateMessage / ShowMessage / SaveRuntime
// / TouchShow.
func (d *Deps) importShowFile(raw []byte, fallbackTitle string, eventID int64) (timerpi.Show, int, error) {
	var sf showFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return timerpi.Show{}, 0, fmt.Errorf("not a TimerPi show file: %v", err)
	}
	if sf.ManifestVersion != showFileVersionV1 && sf.ManifestVersion != showFileVersionV2 {
		return timerpi.Show{}, 0, fmt.Errorf(
			"unsupported show file version %d (want %d or %d)", sf.ManifestVersion, showFileVersionV1, showFileVersionV2)
	}

	title := strings.TrimSpace(sf.Show.Title)
	if title == "" {
		title = strings.TrimSpace(fallbackTitle)
	}
	if title == "" {
		title = "Imported show"
	}
	show, err := d.Store.CreateRoom(eventID, title)
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

	// §11.9: the event's other halves ride v2 bundles — zone + map asset,
	// interaction items with moderation state, votes, screens (re-keyed to
	// imported boards), presets. v1 bundles simply lack the sections.
	if sf.ManifestVersion == showFileVersionV2 {
		boardX := map[int64]int64{}
		for _, b := range sf.Boards {
			layout := string(b.Layout)
			if layout == "" || layout == "null" {
				continue
			}
			if nb, berr := boards.CreateBoard(d.Store.DB, show.ID, b.Name, layout); berr == nil {
				boardX[b.ID] = nb.ID
			}
		}
		assetX := map[int64]int64{}
		for _, a := range sf.Assets {
			data, derr := dataURLBytes(a.Data)
			if derr != nil || len(data) == 0 {
				continue
			}
			mime, ok := sniffImage(data) // the bundle's declared mime is never trusted
			if !ok {
				continue
			}
			if na, aerr := d.Store.CreateAsset(eventID, a.Name, mime, data); aerr == nil {
				assetX[a.ID] = na.ID
			}
		}
		// Polls: export ids are the bundle's list positions (1..N, parents
		// before children), so each child's parent re-keys inline as it
		// lands; votes ride the same mapping afterwards.
		pollX := map[int64]int64{}
		for slot, p := range sf.Polls {
			parent := int64(0)
			if p.Parent != 0 {
				parent = pollX[p.Parent] // parents precede children
			}
			np, cerr := d.Store.CreatePollRaw(timerpi.Poll{
				ShowID: show.ID, Kind: p.Kind, Question: p.Question,
				Options: p.Options, Correct: p.Correct, State: p.State,
				Parent: parent, Author: p.Author, Ts: p.Ts,
				ToAudience: p.ToAudience, ToPresenter: p.ToPresenter, AutoApprove: p.AutoApprove,
			})
			if cerr == nil {
				pollX[int64(slot+1)] = np.ID
			}
		}
		for slot, p := range sf.Polls {
			if p.Spot > 0 && pollX[p.Spot] > 0 {
				_ = d.Store.SetPollSpotRaw(pollX[int64(slot+1)], pollX[p.Spot])
			}
		}
		if d.Hub != nil {
			d.Hub.BroadcastPoll(show.ID)
		}
		d.restoreBundleVotes(pollX, sf.Votes)
		for _, r := range sf.Screens {
			_ = d.Store.SetScreenConfig(show.ID, r.Name, r.Theme, boardX[r.BoardID], r.Room)
		}
		for _, pr := range sf.Presets {
			if len(pr.Data) > 0 && string(pr.Data) != "null" {
				_, _ = d.Store.SavePreset(show.ID, pr.Name, string(pr.Data))
			}
		}
		// The venue map belongs to the event: a bundle's map (or a
		// pre-event bundle's zone map) fills it only when it has none.
		idx := sf.MapIndex
		if idx == 0 {
			idx = sf.ZoneMapIndex
		}
		if aid := assetX[idx]; idx > 0 && aid > 0 {
			if ev, eerr := d.Store.GetEvent(show.EventID); eerr == nil && ev.MapAsset == 0 {
				_ = d.Store.SetEventMap(ev.ID, aid)
			}
		}
	}

	d.warmShow(show.ID)
	return show, len(sf.Cues), nil
}
