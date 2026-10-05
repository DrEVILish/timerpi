// Package views builds the template-facing view data (CONTRACT-UI.md field
// names) from timerpi domain objects and renders the parsed template set.
// Both the page routes and the WS hub's oob fragment pushes use:
//
//	PageData        — the dot for every template (all optional fields)
//	New()           — parse templates/ once (or -dev per-request reparse)
//	Render          — ExecuteTemplate on the registry
//	ShowData(snap)  — map an engine snapshot to the dashboard shape
//	HomeData(...)   — homepage list shape
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
	Kind    string
	// Over/under vs the computed schedule (A2): DeltaMS is only meaningful
	// for the row on the clock; DeltaFmt renders it signed (+late/−early).
	DeltaMS  int64
	DeltaFmt string
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
	Running   bool
	Paused    bool
	ActivePos int64
	PrevPos   int64
	NextPos   int64
	RatePct   int   // rate × 100 (e.g. 100 = ×1.00)
	DayStart  int64 `json:"-"` // DayStartTS
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
	Pos          int64
	Label        string
	DurFmt       string
	StartFmt     string
	EndFmt       string
	HoldFmt      string
	Kind         string
	IsBreak      bool
	Tags         []string
	Speaker      string
	Notes        string
	Color        string
	TimerKind    string
	Alert1Fmt    string
	Alert2Fmt    string
	AlertColor1  string
	AlertColor2  string
	AutoContinue bool
	IsNext       bool
	ID           int64 // row identity (reorder ops)
	// StartAt mirrors the cue's wall-clock auto-start (E5, "" = off).
	StartAt string
}

// MessageVM is a stage overlay line (.Messages entries; frag-messages).
type MessageVM struct {
	ID       int64
	Text     string
	Color    string
	IsShown  bool
	ShownFmt string
}

// ShareVM backs frag-share (.Share). Code is the bare 8-char share code,
// CodeFmt its 4-4 rendering ("K7QP-M3XB"); DisplayPath/DisplayURL build
// from the CODE ONLY (Agent L scope change: numeric /d/<id> links are
// dead routes).
type ShareVM struct {
	Code        string // "K7QPM3XB" (bare)
	CodeFmt     string // "K7QP-M3XB"
	DisplayPath string // "/d/K7QP-M3XB" ("" when the show is code-less)
	DisplayURL  string // absolute; may be "" — client JS fills from location
}

// ShowVM is one homepage list row (.Shows entries; frag-shows).
type ShowVM struct {
	ID          int64
	Code        string // share code ("" pre-backfill rows never render links)
	Title       string
	CueCount    int
	TotalFmt    string
	UpdatedFmt  string
	ControlPath string // "/c/<code>"
	DisplayPath string // "/d/<code>"
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

	// Homepage shape:
	Shows []ShowVM

	// Display page only:
	Hostname string

	// Event context for room pages (the room's parent event).
	Event EventRef

	// Screen pages: this screen's rotation (0/90/180/270).
	Rotation int
}

// EventRef is the parent-event context on room pages (app bar, nav).
type EventRef struct {
	Code    string
	Name    string
	IsSuper bool // this browser is the event's SuperOperator
}

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

// FmtDurSigned renders a signed delta (+0:35 late, −1:02 early; U+2212
// reads better than ASCII hyphen). A delta that rounds to zero renders "0".
func FmtDurSigned(ms int64) string {
	rounded := (ms + 500) / 1000 // seconds, to-nearest (FmtDur's rule)
	if rounded == 0 {
		return "0"
	}
	sign := "+"
	if ms < 0 {
		sign = "−"
		ms = -ms
	}
	return sign + FmtDur(ms)
}

// FmtTimeOfDay renders an epoch-ms instant as the local wall clock HH:MM:SS
// (client Date parity; planned times of day per CONTRACT-UI).
func FmtTimeOfDay(ms int64) string {
	return time.UnixMilli(ms).Format("15:04:05")
}

// FmtCode renders a bare 8-char share code as 4-4 ("K7QP-M3XB"); numeric
// ids are NOT codes here — the rulebook is timerpi/gen.go (Agent L).
func FmtCode(code string) string { return timerpi.FmtCode(code) }

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
	d.Runtime = RuntimeVM{
		Running:   rt.Running,
		Paused:    rt.Paused,
		ActivePos: rt.ActivePos,
		PrevPos:   rt.PrevPos,
		NextPos:   rt.NextPos,
		RatePct:   int(rt.Rate * 100),
		DayStart:  rt.DayStartTS,
	}
	// The share panel is CODE-ONLY (Agent L): the snapshot's show code is
	// the whole public address. Code-less shows (pre-backfill rows would
	// be the only shape) render empty paths; the fragment placeholders
	// hold the panel open rather than print a dead /d/ link.
	code := timerpi.NormalizeCode(snap.Show.Code)
	displayPath := ""
	if code != "" {
		displayPath = "/d/" + code
	}
	d.Share = ShareVM{
		Code:        code,
		CodeFmt:     FmtCode(code),
		DisplayPath: displayPath,
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
			Kind:    active.Kind,
		}
		// A2: the runtime schedule computes the on-clock row's projected
		// real end (over/under vs its scheduled end); the client keeps the
		// chip live between frag repaints (timerpi.js paint()).
		if row := schedRowOf(sched, active.Pos); row != nil {
			d.Current.DeltaMS = row.DeltaMS
			d.Current.DeltaFmt = FmtDurSigned(row.DeltaMS)
		}
	}
	if next != nil {
		d.Next = NextCue{Pos: next.Pos, Label: next.Label, DurFmt: FmtDur(next.DurationMS)}
		if row := schedRowOf(sched, next.Pos); row != nil {
			d.Next.StartFmt = FmtTimeOfDay(row.StartTS)
		}
	}

	for _, m := range snap.Messages {
		d.Messages = append(d.Messages, MessageVM{
			ID:       m.ID,
			Text:     m.Text,
			Color:    m.Color,
			IsShown:  m.ShownAt > 0,
			ShownFmt: FmtTimeOfDay(m.ShownAt),
		})
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
			out = append(out, MessageVM{ID: m.ID, Text: m.Text, Color: m.Color, IsShown: true, ShownFmt: FmtTimeOfDay(m.ShownAt)})
		}
	}
	d.Messages = out
}

// ActionVM is one E4 log row (kept for future debugging surfaces; the
// dashboard section was removed by owner decision 2026-10-05).
type ActionVM struct {
	TS     int64
	Ago    string // FmtAgo bucket at render time
	Actor  string
	Action string
	Detail string
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
			Pos:          c.Pos,
			ID:           c.ID,
			Label:        c.Label,
			DurFmt:       FmtDur(c.DurationMS),
			Kind:         c.Kind,
			IsBreak:      c.Kind == timerpi.KindBreak,
			TimerKind:    c.TimerKind,
			AlertColor1:  c.AlertColor1,
			AlertColor2:  c.AlertColor2,
			AutoContinue: c.AutoContinue,
			Speaker:      c.Speaker,
			Notes:        c.Notes,
			Color:        c.Color,
			StartAt:      c.StartAt,
			HoldFmt:      fmtHold(c.HoldMS),
			Alert1Fmt:    fmtAlert(c.Alert1MS),
			Alert2Fmt:    fmtAlert(c.Alert2MS),
			IsNext:       rt.NextPos == c.Pos,
		}
		vm.Tags = tagsOf(c.Tags)
		if row := schedRowOf(sched, c.Pos); row != nil {
			if sched.DayStartTS != 0 {
				vm.StartFmt = FmtTimeOfDay(row.StartTS)
				vm.EndFmt = FmtTimeOfDay(row.EndTS)
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

func fmtHold(holdMS int64) string {
	if holdMS <= 0 {
		return ""
	}
	return FmtDur(holdMS)
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
// Homepage data.

// HomeData builds the homepage dot from the show rows.
func HomeData(shows []ShowVM) *PageData {
	return &PageData{
		Page:         "home",
		Nav:          "home",
		Role:         "controls",
		Shows:        shows,
		Title:        "",
		DefaultTheme: config.DefaultTheme(),
	}
}

// BuildShowRows converts shows + their cue lists to homepage rows
// (cue counts / totals / humanized stamps per CONTRACT-UI §2). Public
// paths are CODE-ONLY (Agent L): a row without a code (impossible after
// the backfill migration) yields empty paths rather than a dead numeric
// link.
func BuildShowRows(metas ShowMetas) []ShowVM {
	rows := make([]ShowVM, 0, len(metas))
	for _, s := range metas {
		code := timerpi.NormalizeCode(s.Show.Code)
		var control, display string
		if code != "" {
			control, display = "/c/"+code, "/d/"+code
		}
		rows = append(rows, ShowVM{
			ID:          s.Show.ID,
			Code:        code,
			Title:       s.Show.Title,
			CueCount:    s.CueCount,
			TotalFmt:    FmtDur(s.TotalMS),
			UpdatedFmt:  FmtAgo(s.Show.UpdatedAt),
			ControlPath: control,
			DisplayPath: display,
		})
	}
	return rows
}

// ShowMeta is the per-show metadata the homepage needs (DB reader output).
type ShowMeta struct {
	Show     timerpi.Show
	CueCount int
	TotalMS  int64 // whole day incl. holds/breaks
}

// ShowMetas is the full homepage listing.
type ShowMetas []ShowMeta

// ---------------------------------------------------------------------------
// Template registry.

// Set is the parsed template set (base.html + pages + fragments).
type Set struct {
	tmpl *template.Template
	fs   fs.FS
}

// New parses templates from dir: base/pages at the top level, fragments
// one level down (CONTRACT-UI §1). Dev workflows may parse a fresh set per
// request via a delegate Set; hot reload is not otherwise supported.
func New(dir string) (*Set, error) {
	s := &Set{}
	root := os.DirFS(dir)
	SetPublicDir(filepath.Join(filepath.Dir(dir), "public"))
	t, err := template.New("").Funcs(template.FuncMap{"asset": Asset}).ParseFS(root, "*.html", "fragments/*.html")
	if err != nil {
		return nil, fmt.Errorf("views: parsing templates in %s: %w", dir, err)
	}
	s.fs = root
	s.tmpl = t
	return s, nil
}

// SubDir is unused by production code; kept for diagnostics.
func (s *Set) SubDir() string {
	return "templates"
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
