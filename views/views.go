// Package views builds the template-facing view data (CONTRACT-UI.md field
// names) from timerpi domain objects and renders the parsed template set.
// Both the page routes and the WS hub's oob fragment pushes use:
//
//	PageData        — the dot for every template (all optional fields)
//	New()           — parse templates/ once (or -dev per-request reparse)
//	Render          — ExecuteTemplate on the registry
//	ShowData(snap)  — map an engine snapshot to the dashboard shape
package views

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"timerpi/config"
	"timerpi/timerpi"
)

// ---------------------------------------------------------------------------
// View structs — field names are load-bearing: they match CONTRACT-UI.md §2.

// ShowRef lands on .Show. Code is the public share code (Agent L): public
// links ALWAYS use it — ID is internal bookkeeping only.
type ShowRef struct {
	ID    int64
	Title string
	Code  string
	// Blanked mirrors Snapshot.Show.Blanked (E3 blackout) for display
	// bodies: data-blanked + the .tp-blanked overlay.
	Blanked bool
}

// CurrentCue is the active cue view (.Current; may be zero).
type CurrentCue struct {
	Pos     int64
	Label   string
	Speaker string
	DurFmt  string
	IsBreak bool
}

// NextCue is the next-up view (.Next; may be zero).
type NextCue struct {
	Pos      int64
	Label    string
	DurFmt   string
	StartFmt string
}

// RuntimeVM backs the rate slider and the day-bar anchor (.Runtime).
type RuntimeVM struct {
	ActivePos int64
	RatePct   int // rate × 100 (e.g. 100 = ×1.00)
}

// SegmentVM is one day-bar block (.Schedule.Segments).
type SegmentVM struct {
	Pos      int64
	Label    string
	Title    string
	Left     string // "12.34%"
	Width    string // "8.26%"
	StartMS  int64
	EndMS    int64
	IsBreak  bool
	IsDone   bool
	IsActive bool
}

// ScheduleVM backs frag-daybar (.Schedule).
type ScheduleVM struct {
	DayStartTS  int64
	DayStartFmt string
	DayEndFmt   string
	TotalMS     int64
	TotalFmt    string
	Segments    []SegmentVM
}

// CueVM is one running-order row (.Cues entries; frag-cuelist).
type CueVM struct {
	Pos         int64
	Label       string
	DurFmt      string
	StartFmt    string
	EndFmt      string
	StartHM     string // planned start "15:04" (24 h); "" until the day has a start (walk-in schedule, U3)
	EndHM       string // planned end "15:04" (running order shows HH:MM; StartFmt/EndFmt keep seconds)
	Kind        string
	IsBreak     bool
	Tags        []string
	Speaker     string
	Location    string // a break's place ("Great Hall", U14)
	Notes       string
	Color       string
	TimerKind   string
	EndAction   string
	Alert1Fmt   string
	Alert2Fmt   string
	AlertColor1 string
	AlertColor2 string
	IsNext      bool
	ID          int64 // row identity (reorder ops)
}

// MessageVM is a stage overlay line (.Messages entries; frag-messages).
type MessageVM struct {
	ID      int64
	Text    string
	Color   string
	IsShown bool
}

// ShareVM backs frag-share (.Share). Code is the bare 8-char share code,
// CodeFmt its 4-4 rendering ("K7QP-M3XB").
type ShareVM struct {
	Code    string // "K7QPM3XB" (bare)
	CodeFmt string // "K7QP-M3XB"
	// AudienceBase is where phones join: the cloud on a box that has one
	// (VENUE-CLOUD §1, phones only reach TimerPi through the cloud); ""
	// means this server (client JS uses location.origin).
	AudienceBase string
}

// PageData is THE template dot: every field may be zero-valued; templates
// tolerate it. Page is REQUIRED on every render ("home"|"dashboard").
type PageData struct {
	Page   string
	Title  string
	Nav    string
	Role   string
	ShowID int64
	Show   ShowRef
	Peers  int

	// Appliance default theme (B7): the ftl theme name surfaces fall back
	// to when the browser has nothing stored. "" = bundled xbmc.
	DefaultTheme string

	// DayStartHHMM carries the show's scheduled day start (B4; "" = unset)
	// so the dashboard's "Day begins at" field prefills from truth.
	DayStartHHMM string

	// Dashboard shape:
	Current  CurrentCue
	Next     NextCue
	Runtime  RuntimeVM
	Schedule ScheduleVM
	Cues     []CueVM
	Messages []MessageVM
	Share    ShareVM
	Notes    string // per-show day memo (A7; dashboard + daysheet surfaces)

	// Display page only:
	Hostname string

	// Event context for room pages (the room's parent event).
	Event EventRef

	// Screen pages: this screen's rotation (0/90/180/270).
	Rotation int

	// RoomPrefix is "Room: " when the room's event has several rooms, so
	// screens read "Room: Stark"; "" for a single-room event (STATUS U6).
	RoomPrefix string
}

// EventRef is the parent-event context on room pages (app bar, nav).
type EventRef struct {
	Code    string
	Name    string
	IsSuper bool // this browser is the event's SuperOperator
}

// CodeFmt is the Event ID as people read it (XXXX-XXXX).
func (e EventRef) CodeFmt() string { return timerpi.FmtCode(e.Code) }

// ---------------------------------------------------------------------------
// Formatting (client parity: public/src/engine.js fmt*).

// FmtDur renders a duration as m:ss below an hour, h:mm:ss from 1 h.
func FmtDur(ms int64) string {
	total := (ms + 500) / 1000
	if total < 0 {
		total = 0
	}
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// FmtTimeOfDay renders an epoch-ms instant as the local wall clock HH:MM:SS
// (client Date parity; planned times of day per CONTRACT-UI).
func FmtTimeOfDay(ms int64) string {
	return time.UnixMilli(ms).Format("15:04:05")
}

// FmtAgo humanizes an epoch-ms instant ("just now", "5m ago").
func FmtAgo(ms int64) string {
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < 15*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// ParseDuration accepts "30" (minutes), "30s" (seconds), "1:30"
// (h:mm) or "1:00:05" (h:mm:ss) into milliseconds — dashboard quick-adds
// and the inspector duration field (owner decision 2026-10-05: a bare
// number is minutes, two-part is hours:minutes). File imports keep their
// own seconds-based rule (importdocs.ParseDurationMS) — spreadsheet
// "1:00" still means one minute there.
func ParseDuration(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if strings.HasSuffix(s, "s") || strings.HasSuffix(s, "S") {
		sec, err := strconv.Atoi(strings.TrimSpace(s[:len(s)-1]))
		if err != nil || sec < 0 {
			return 0
		}
		return int64(sec) * 1000
	}
	parts := strings.Split(s, ":")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || v < 0 { // empty parts fail Atoi: "1::30" is garbage,
			return 0 // not "1:30" (JS parseDur parity)
		}
		nums = append(nums, v)
	}
	switch len(nums) {
	case 1:
		// bare number = minutes (operator shorthand)
		return int64(nums[0]) * 60 * 1000
	case 2:
		return int64(nums[0]*60+nums[1]) * 60 * 1000 // h:mm
	case 3:
		return int64((nums[0]*60+nums[1])*60+nums[2]) * 1000 // h:mm:ss
	default:
		return 0
	}
}

// ---------------------------------------------------------------------------
// Mapping: engine snapshot → view data.

// ShowData builds the dashboard/display dot from a snapshot. nowMS anchors
// the "done" day-bar flags; hostname lands only on the display page.
func ShowData(snap timerpi.Snapshot, nowMS int64, hostname string) *PageData {
	d := &PageData{
		Page:         "dashboard",
		DefaultTheme: config.DefaultTheme(),
		ShowID:       snap.Show.ID,
		Show:         ShowRef{ID: snap.Show.ID, Title: snap.Show.Title, Code: snap.Show.Code, Blanked: snap.Show.Blanked},
		Role:         "controls",
		Title:        snap.Show.Title,
		Nav:          "cues",
		Hostname:     hostname,
	}
	rt := snap.Runtime
	d.Runtime = RuntimeVM{ActivePos: rt.ActivePos, RatePct: int(rt.Rate * 100)}
	// The share panel is CODE-ONLY (Agent L): the snapshot's show code is
	// the whole public address. Code-less shows (pre-backfill rows would
	// be the only shape) render empty; the fragment placeholders hold the
	// panel open rather than print a dead link.
	code := timerpi.NormalizeCode(snap.Show.Code)
	d.Share = ShareVM{Code: code, CodeFmt: timerpi.FmtCode(code)}
	if !config.IsCloud() {
		d.Share.AudienceBase = config.CloudURL()
	}
	d.Notes = snap.Show.Notes
	d.DayStartHHMM = snap.Show.DayStart

	sched := timerpi.ComputeScheduleRuntime(snap.Cues, runtimeOf(snap), nowMS)
	d.Schedule = scheduleVM(sched, rt, nowMS)
	d.Cues = cueVMs(snap.Cues, sched, rt)

	// Active + next cue views. Idle-show polish: with nothing armed yet,
	// "next" is the DAY'S FIRST cue (the client mirror — engine.js
	// findNextArmed — picks exactly that), so server pre-render and the
	// first WS repaint agree instead of flashing "Next up —".
	var active, next *timerpi.Cue
	for i := range snap.Cues {
		if snap.Cues[i].Pos == rt.ActivePos {
			active = &snap.Cues[i]
		}
		if snap.Cues[i].Pos == rt.NextPos {
			next = &snap.Cues[i]
		}
	}
	if next == nil && len(snap.Cues) > 0 {
		next = &snap.Cues[0]
	}
	if active != nil {
		d.Current = CurrentCue{
			Pos:     active.Pos,
			Label:   active.Label,
			Speaker: active.Speaker,
			DurFmt:  FmtDur(active.DurationMS),
			IsBreak: active.Kind == timerpi.KindBreak,
		}
	}
	if next != nil {
		d.Next = NextCue{Pos: next.Pos, Label: next.Label, DurFmt: FmtDur(next.DurationMS)}
		if row := schedRowOf(sched, next.Pos); row != nil {
			d.Next.StartFmt = FmtTimeOfDay(row.StartTS)
		}
	}

	for _, m := range snap.Messages {
		d.Messages = append(d.Messages, MessageVM{ID: m.ID, Text: m.Text, Color: m.Color, IsShown: m.ShownAt > 0})
	}
	// Hidden (queued) messages are not in the snapshot (shown only);
	// callers with DB access pour the full list via SetMessages.
	return d
}

// SetMessages replaces .Messages with the full DB listing (shown + queued),
// for the dashboard page and the messages oob: the panel manages queued
// rows (Show/Hide buttons) the snapshot can't carry.
func (d *PageData) SetMessages(msgs []timerpi.Message) {
	out := make([]MessageVM, 0, len(msgs))
	for _, m := range msgs { // queued first, shown after (Create order)
		if m.ShownAt <= 0 {
			out = append(out, MessageVM{ID: m.ID, Text: m.Text, Color: m.Color})
		}
	}
	for _, m := range msgs {
		if m.ShownAt > 0 {
			out = append(out, MessageVM{ID: m.ID, Text: m.Text, Color: m.Color, IsShown: true})
		}
	}
	d.Messages = out
}

// runtimeOf extracts the stored Runtime half of a RuntimeView (schedule
// math takes the struct, not the view).
func runtimeOf(snap timerpi.Snapshot) timerpi.Runtime {
	rtv := snap.Runtime
	return timerpi.Runtime{
		ShowID:          snap.Show.ID,
		ActivePos:       rtv.ActivePos,
		PrevPos:         rtv.PrevPos,
		NextPos:         rtv.NextPos,
		Paused:          rtv.Paused,
		Running:         rtv.Running,
		EndAction:       rtv.EndAction,
		AnchorTS:        rtv.AnchorTS,
		Rate:            rtv.Rate,
		PausedElapsedMS: rtv.PausedElapsedMS,
		DayStartTS:      rtv.DayStartTS,
	}
}

func schedRowOf(s timerpi.Schedule, pos int64) *timerpi.ScheduleRow {
	for i := range s.Rows {
		if s.Rows[i].Pos == pos {
			return &s.Rows[i]
		}
	}
	return nil
}

// scheduleVM maps the computed schedule onto the day-bar fields.
func scheduleVM(s timerpi.Schedule, rt timerpi.RuntimeView, nowMS int64) ScheduleVM {
	v := ScheduleVM{
		DayStartTS:  s.DayStartTS,
		DayStartFmt: FmtTimeOfDay(s.DayStartTS),
		DayEndFmt:   FmtTimeOfDay(s.EndTS),
		TotalMS:     s.TotalMS,
		TotalFmt:    FmtDur(s.TotalMS),
	}
	if v.DayStartTS == 0 {
		// No day anchored yet: show offsets instead of a misleading
		// midnight wall clock.
		v.DayStartFmt = "0:00"
		v.DayEndFmt = "0:00"
	} else {
		v.DayStartFmt = FmtTimeOfDay(s.DayStartTS)
		v.DayEndFmt = FmtTimeOfDay(s.EndTS)
	}
	for _, r := range s.Rows {
		seg := SegmentVM{
			Pos:     r.Pos,
			Label:   rowLabel(r),
			Title:   rowTitle(r),
			IsBreak: r.Break,
		}
		if s.TotalMS > 0 {
			seg.Left = fmt.Sprintf("%.2f%%", float64(r.StartMS)/float64(s.TotalMS)*100)
			seg.Width = fmt.Sprintf("%.2f%%", float64(r.EndMS-r.StartMS)/float64(s.TotalMS)*100)
		} else {
			seg.Left, seg.Width = "0%", "0%"
		}
		seg.StartMS, seg.EndMS = r.StartMS, r.EndMS
		seg.IsDone = nowMS > r.EndTS && !(rt.Running && rt.ActivePos == r.Pos)
		seg.IsActive = rt.ActivePos == r.Pos && rt.Running
		v.Segments = append(v.Segments, seg)
	}
	return v
}

// rowLabel picks the day-bar text: break rows announce themselves.
func rowLabel(r timerpi.ScheduleRow) string {
	if r.Break {
		if r.Label == "" {
			return "BREAK"
		}
		return r.Label
	}
	return r.Label
}

// rowTitle is the day-bar hover title.
func rowTitle(r timerpi.ScheduleRow) string {
	t := rowLabel(r)
	if r.Speaker != "" {
		t += " · " + r.Speaker
	}
	return t + " · " + FmtDur(r.DurationMS)
}

// cueVMs maps the cue list with per-row computed start/end from the
// schedule (CONTRACT-UI: StartFmt/EndFmt come from ComputeSchedule).
func cueVMs(cues []timerpi.Cue, sched timerpi.Schedule, rt timerpi.RuntimeView) []CueVM {
	out := make([]CueVM, 0, len(cues))
	for _, c := range cues {
		vm := CueVM{
			Pos:         c.Pos,
			ID:          c.ID,
			Label:       c.Label,
			DurFmt:      FmtDur(c.DurationMS),
			Kind:        c.Kind,
			IsBreak:     c.Kind == timerpi.KindBreak,
			TimerKind:   c.TimerKind,
			EndAction:   c.EndAction,
			AlertColor1: c.AlertColor1,
			AlertColor2: c.AlertColor2,
			Speaker:     c.Speaker,
			Location:    c.Location,
			Notes:       c.Notes,
			Color:       c.Color,
			Alert1Fmt:   fmtAlert(c.Alert1MS),
			Alert2Fmt:   fmtAlert(c.Alert2MS),
			IsNext:      rt.NextPos == c.Pos,
		}
		vm.Tags = tagsOf(c.Tags)
		if row := schedRowOf(sched, c.Pos); row != nil {
			if sched.DayStartTS != 0 {
				vm.StartFmt = FmtTimeOfDay(row.StartTS)
				vm.EndFmt = FmtTimeOfDay(row.EndTS)
				vm.StartHM = time.UnixMilli(row.StartTS).Format("15:04")
				vm.EndHM = time.UnixMilli(row.EndTS).Format("15:04")
			} else {
				// No day anchored yet: show relative offsets instead of a
				// misleading midnight wall clock.
				vm.StartFmt = offset(row.StartMS)
				vm.EndFmt = offset(row.EndMS)
			}
		}
		out = append(out, vm)
	}
	return out
}

func fmtAlert(alertMS int64) string {
	if alertMS <= 0 {
		return ""
	}
	return FmtDur(alertMS)
}

func offset(ms int64) string {
	return "+" + FmtDur(ms)
}

// tagsOf splits the space-separated tag field ("VT GFX") into badges.
func tagsOf(s string) []string {
	return strings.Fields(s)
}

// ---------------------------------------------------------------------------
// Template registry.

// Set is the parsed template set (base.html + pages + fragments).
type Set struct {
	tmpl *template.Template
}

// New parses templates from dir: base/pages at the top level, fragments
// one level down (CONTRACT-UI §1). Dev workflows may parse a fresh set per
// request via a delegate Set; hot reload is not otherwise supported.
func New(dir string) (*Set, error) {
	SetPublicFS(os.DirFS(filepath.Join(filepath.Dir(dir), "public")))
	return parse(os.DirFS(dir), dir)
}

// NewFS parses templates from the embedded web tree (STATUS C9): web holds
// templates/ and public/, so the binary always serves the pages and
// scripts it was built with.
func NewFS(web fs.FS) (*Set, error) {
	tmpl, err := fs.Sub(web, "templates")
	if err != nil {
		return nil, err
	}
	pub, err := fs.Sub(web, "public")
	if err != nil {
		return nil, err
	}
	SetPublicFS(pub)
	return parse(tmpl, "embedded templates")
}

func parse(root fs.FS, label string) (*Set, error) {
	t, err := template.New("").Funcs(template.FuncMap{"asset": Asset}).ParseFS(root, "*.html", "fragments/*.html")
	if err != nil {
		return nil, fmt.Errorf("views: parsing templates in %s: %w", label, err)
	}
	return &Set{tmpl: t}, nil
}

// Render executes the named template (usually "base" or "display").
func (s *Set) Render(w io.Writer, name string, data any) error {
	return s.tmpl.ExecuteTemplate(w, name, data)
}

// ---------------------------------------------------------------------------
// Display board (Agent N, ADDITIVE — existing structs above untouched).
//
// BoardPage is the dot for templates/display_board.html + fragments/b-*.
// It embeds *PageData so every CONTRACT-UI field (.Show, .Current, .Next,
// .Runtime, .Schedule, .Cues, .Messages, .Share, .Hostname) keeps working
// inside board templates; the Board* fields carry the composed screen.

// BoardWidgetVM is one tile's server view: grid geometry + precomputed
// grid-area style + raw opts for the client editor.
type BoardWidgetVM struct {
	ID    string
	Type  string
	X, Y  int
	W, H  int
	Style template.CSS // grid-area (built from validated ints — no user text)
	Opts  map[string]string
}

// BoardWidgetStyle renders the grid-area inline style (1-based lines).
func BoardWidgetStyle(x, y, w, h int) template.CSS {
	return template.CSS(fmt.Sprintf("grid-column: %d / span %d; grid-row: %d / span %d;", x+1, w, y+1, h))
}

// BoardVM is the rendered board (geometry only; live digits are JS-owned).
type BoardVM struct {
	ID          int64
	Name        string
	Rows        int    // canvas rows (the grid stretches them to fill the screen)
	Orientation string // landscape | portrait
	Widgets     []BoardWidgetVM
}

// BoardInfo is one entry of the board switcher.
type BoardInfo struct {
	ID   int64
	Name string
}

// BoardJoinVM backs the board's share affordance (QR join card pattern
// from d-chrome: mini QR of THIS board URL + control-room link + host).
type BoardJoinVM struct {
	Self    string // absolute URL of this board (view + board id preserved)
	QR      string // /api/shows/:code/qr?data=<urlencoded Self>&size=132
	Control string // /c/<code> — operator control room
	Host    string // "<hostname>.local" (empty when unknown)
}

// BoardPage is the display_board dot.
type BoardPage struct {
	*PageData
	Board   BoardVM
	Boards  []BoardInfo
	Join    BoardJoinVM
	BoardID int64
	// Server-rendered initials (readable with zero JS; board.js repaints):
	NowFmt         string      // wall clock HH:MM:SS at render
	CountdownFmt   string      // active-cue remaining (m:ss / h:mm:ss)
	CountdownState string      // idle|armed|running|paused|held|overtime|alert1|alert2|blank
	ProgressPct    string      // active-cue progress "42.50%"
	DayPct         string      // whole-day progress "12.00%"
	RateFmt        string      // "×1.00"
	Editable       bool        // ?edit=1 — operator may compose (Edit/Lock chrome)
	Rotation       int         // this screen's rotation (0/90/180/270)
	ScreenKind     string      // this screen's display type (audience/walkin/presenter)
	LayoutJSON     template.JS // current layout document (editor pristine copy; validated JSON)
}

// WidgetDot pairs one tile with its page for fragment dispatch:
// {{template "frag-b-countdown" ($.WidgetDot .)}} inside the range.
type WidgetDot struct {
	W BoardWidgetVM
	P *BoardPage
}

// WidgetDot builds the fragment dot for one tile.
func (p *BoardPage) WidgetDot(w BoardWidgetVM) WidgetDot {
	return WidgetDot{W: w, P: p}
}

// Fragment renders one named fragment to a string — the WS hub's oob swapper
// and htmx endpoints build on this.
func (s *Set) Fragment(name string, data any) (string, error) {
	var b strings.Builder
	if err := s.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		return "", fmt.Errorf("views: rendering fragment %s: %w", name, err)
	}
	return b.String(), nil
}
