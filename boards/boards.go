// Package boards owns the customizable display-board persistence + layout
// model (Agent N).
//
// Boards belong to a show: display_boards(id, show_id, name, layout_json,
// updated_at). The table is created by Migrate against the SAME sqlite file
// the timerpi package owns — Migrate takes the live *sqlx.DB handle (the
// embedded handle of timerpi.DB), so there is exactly one opener/DSN (see
// timerpi/db.go; that file is untouched).
//
// Layout model: a 12-column grid of widgets {id, type, x,y,w,h, opts{}}.
// Validation: NormalizeLayout clamps every widget into the grid;
// ValidateLayout additionally rejects unknown widget types and overlapping
// rectangles. Widget types are the WidgetTypes registry below.
package boards

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"timerpi/timerpi"
)

// Grid geometry.
const (
	GridCols   = 12
	MaxWidgets = 48
	MaxY       = MaxRows - 1 // a tile's top row; Y+H never passes MaxRows (BUGLOG RW33)
	MaxH       = 12
)

// Widget is one board tile. Opts carries per-type settings (all strings):
//
//	countdown/wallclock: tenths "0"|"1"
//	cuelabel:            source "label"|"speaker"
//	schedule:            count "3"|"5"|"8"
//	notice:              text "…" (free text, ≤256 chars — sanitizeOpts truncates)
type Widget struct {
	ID   string            `json:"id"`
	Type string            `json:"type"`
	X    int               `json:"x"`
	Y    int               `json:"y"`
	W    int               `json:"w"`
	H    int               `json:"h"`
	Opts map[string]string `json:"opts,omitempty"`
}

// Layout is the persisted board geometry (layout_json shape). A layout is
// a fixed canvas: GridCols columns × Rows rows that stretch to fill the
// screen. Orientation says which way up the canvas is designed
// (landscape 16:9 or portrait 9:16 poster screens).
type Layout struct {
	V           int    `json:"v"`
	Rows        int    `json:"rows,omitempty"`
	Orientation string `json:"orientation,omitempty"`
	// Anim / AnimMS: the layout's default appear/disappear animation
	// (fade | slide | pop | none; 120–3000 ms). Tiles may override it
	// with opts.anim / opts.animMS.
	Anim    string   `json:"anim,omitempty"`
	AnimMS  int      `json:"animMS,omitempty"`
	Widgets []Widget `json:"widgets"`
}

// Canvas defaults and limits.
const (
	DefaultRowsLandscape = 8
	DefaultRowsPortrait  = 16
	MaxRows              = 48
)

// Extent is the lowest occupied row + 1.
func (l Layout) Extent() int {
	n := 0
	for _, w := range l.Widgets {
		if w.Y+w.H > n {
			n = w.Y + w.H
		}
	}
	return n
}

// WidgetDef describes one registered widget type (palette entry).
type WidgetDef struct {
	Type     string // registry key (widget.type)
	Title    string // palette label
	Desc     string // one-line operator help
	DefaultW int
	DefaultH int
}

// Widget types (v1). Every board.js renderer + every server fragment keys
// off this registry; unknown types are rejected at the REST boundary.
var WidgetTypes = []WidgetDef{
	{Type: "countdown", Title: "Countdown", Desc: "Giant active-cue timer, alert colors + overtime", DefaultW: 8, DefaultH: 3},
	{Type: "cuelabel", Title: "Cue label", Desc: "Active cue label (or speaker via settings)", DefaultW: 5, DefaultH: 1},
	{Type: "speaker", Title: "Speaker", Desc: "Active cue speaker", DefaultW: 3, DefaultH: 1},
	{Type: "nextup", Title: "Next up", Desc: "Next cue label + start + in", DefaultW: 4, DefaultH: 2},
	{Type: "nownext", Title: "Current & next", Desc: "Walk-in: current and next session with start time, duration and speaker", DefaultW: 6, DefaultH: 4},
	{Type: "wallclock", Title: "Wall clock", Desc: "Time of day (lobby filler)", DefaultW: 4, DefaultH: 1},
	{Type: "progress", Title: "Cue progress", Desc: "Active-cue progress bar", DefaultW: 8, DefaultH: 1},
	{Type: "dayprogress", Title: "Day progress", Desc: "Whole-day progress bar", DefaultW: 8, DefaultH: 1},
	{Type: "messages", Title: "Messages", Desc: "Shown stage-message overlay lines", DefaultW: 4, DefaultH: 3},
	{Type: "showtitle", Title: "Show title", Desc: "Show title + share code", DefaultW: 6, DefaultH: 1},
	{Type: "rate", Title: "Rate chip", Desc: "Countdown rate multiplier", DefaultW: 2, DefaultH: 1},
	{Type: "schedule", Title: "Schedule", Desc: "Mini running-order list", DefaultW: 4, DefaultH: 4},
	{Type: "notice", Title: "Notice", Desc: "Static free text (welcome, sponsors, Wi-Fi…)", DefaultW: 6, DefaultH: 2},
	// PLAN §11.2 (Rooms v2, phase 2): the audience/venue slots.
	{Type: "poll", Title: "Audience item", Desc: "Whatever is shown to its target: poll/quiz results bars, Q&A wall + spotlight, word cloud, ideas", DefaultW: 6, DefaultH: 4},
	{Type: "qa", Title: "Q&A wall", Desc: "Approved questions by upvotes, plus the spotlight (Q&A and ideas only)", DefaultW: 6, DefaultH: 4},
	{Type: "wordcloud", Title: "Word cloud", Desc: "Approved words sized by how many people sent them", DefaultW: 6, DefaultH: 4},
	{Type: "map", Title: "Map", Desc: "Venue map (the event map unless another image is picked)", DefaultW: 6, DefaultH: 4},
	{Type: "rooms", Title: "All rooms now", Desc: "Every room of the event: what is on now and what is next", DefaultW: 8, DefaultH: 4},
	{Type: "eventschedule", Title: "Event schedule", Desc: "Every room's full-day schedule", DefaultW: 8, DefaultH: 4},
	{Type: "joinqr", Title: "Join QR", Desc: "Audience join QR for this room", DefaultW: 3, DefaultH: 4},
}

// TemplateInfo is one built-in starting layout, grouped by display type
// (PRODUCT §3.2): audience | walkin | presenter. Go is the single source of
// truth (served at GET /api/board-templates; boards_test checks overlap).
type TemplateInfo struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Desc   string `json:"desc"`
	Layout Layout `json:"layout"`
}

// Templates returns the catalog in display order.
func Templates() []TemplateInfo {
	w := func(id, typ string, x, y, hw, hh int, opts map[string]string) Widget {
		return Widget{ID: id, Type: typ, X: x, Y: y, W: hw, H: hh, Opts: opts}
	}
	land := func(rows int, ws ...Widget) Layout {
		return Layout{V: 1, Rows: rows, Orientation: "landscape", Widgets: ws}
	}
	port := func(rows int, ws ...Widget) Layout {
		return Layout{V: 1, Rows: rows, Orientation: "portrait", Widgets: ws}
	}
	clock := map[string]string{"tenths": "0"}
	return []TemplateInfo{
		// --- Audience displays (what the room looks at) ---
		{Key: "main", Name: "Audience main", Kind: "audience", Desc: "Polls, results, Q&A and word clouds when shown to the audience; join QR alongside",
			Layout: land(8,
				w("title", "showtitle", 0, 0, 12, 1, nil),
				w("item", "poll", 0, 1, 9, 7, map[string]string{"target": "audience"}),
				// The QR's own label is the one "Scan to take part" on screen.
				w("join", "joinqr", 9, 1, 3, 7, nil))},
		{Key: "main-portrait", Name: "Audience main (portrait)", Kind: "audience", Desc: "Poster version of the audience main: the shown item above, join QR below",
			Layout: port(16,
				w("title", "showtitle", 0, 0, 12, 2, nil),
				w("item", "poll", 0, 2, 12, 9, map[string]string{"target": "audience"}),
				w("join", "joinqr", 3, 11, 6, 5, nil))},
		{Key: "countdown", Name: "Audience countdown", Kind: "audience", Desc: "A timer the room can see (breaks, competitions, timed tasks): big countdown, session and what's next",
			Layout: land(8,
				w("countdown", "countdown", 0, 0, 12, 4, map[string]string{"tenths": "0"}),
				w("label", "cuelabel", 0, 4, 12, 1, map[string]string{"source": "label"}),
				w("progress", "progress", 0, 5, 12, 1, nil),
				w("next", "nextup", 0, 6, 12, 2, nil))},
		{Key: "countdown-portrait", Name: "Audience countdown (portrait)", Kind: "audience", Desc: "Poster version of the audience countdown",
			Layout: port(16,
				w("label", "cuelabel", 0, 0, 12, 2, map[string]string{"source": "label"}),
				w("countdown", "countdown", 0, 2, 12, 7, map[string]string{"tenths": "0"}),
				w("progress", "progress", 0, 9, 12, 1, nil),
				w("next", "nextup", 0, 10, 12, 3, nil),
				w("clock", "wallclock", 0, 13, 12, 3, clock))},
		{Key: "qawall", Name: "Q&A wall", Kind: "audience", Desc: "The approved questions and the spotlight, full screen",
			Layout: land(8,
				w("wall", "qa", 0, 0, 9, 8, map[string]string{"target": "audience"}),
				w("join", "joinqr", 9, 0, 3, 8, map[string]string{"label": "Questions? Scan and ask."}))},
		{Key: "holding", Name: "Holding slide", Kind: "audience", Desc: "Session title, speaker, what's next and the join QR between items",
			Layout: land(8,
				w("title", "showtitle", 0, 0, 12, 1, nil),
				w("label", "cuelabel", 0, 1, 9, 2, map[string]string{"source": "label"}),
				w("speaker", "speaker", 0, 3, 9, 1, nil),
				w("next", "nextup", 0, 4, 9, 2, nil),
				w("clock", "wallclock", 0, 6, 9, 2, clock),
				w("join", "joinqr", 9, 1, 3, 7, nil))},
		// --- Walk-in displays (posters outside rooms, foyer) ---
		{Key: "event", Name: "Event walk-in", Kind: "walkin", Desc: "Every room now and next, the venue map and the full-day schedule",
			Layout: land(8,
				w("rooms", "rooms", 0, 0, 9, 5, nil),
				w("clock", "wallclock", 9, 0, 3, 2, clock),
				w("map", "map", 9, 2, 3, 6, nil),
				w("sched", "eventschedule", 0, 5, 9, 3, nil))},
		{Key: "event-portrait", Name: "Event walk-in (portrait)", Kind: "walkin", Desc: "Poster version of the event walk-in",
			Layout: port(16,
				w("clock", "wallclock", 0, 0, 12, 2, clock),
				w("rooms", "rooms", 0, 2, 12, 7, nil),
				w("map", "map", 0, 9, 12, 5, nil),
				w("note", "notice", 0, 14, 12, 2, map[string]string{"text": "Welcome"}))},
		{Key: "room", Name: "Room walk-in", Kind: "walkin", Desc: "This room: clock, current and next session (start, duration, speaker), full-day schedule",
			Layout: land(8,
				w("title", "showtitle", 0, 0, 8, 1, nil),
				w("clock", "wallclock", 8, 0, 4, 1, clock),
				w("nownext", "nownext", 0, 1, 6, 7, nil),
				w("sched", "schedule", 6, 1, 6, 7, map[string]string{"count": "all"}))},
		{Key: "room-portrait", Name: "Room walk-in (portrait)", Kind: "walkin", Desc: "Poster version of the room walk-in",
			Layout: port(16,
				w("title", "showtitle", 0, 0, 12, 2, nil),
				w("clock", "wallclock", 0, 2, 12, 2, clock),
				w("nownext", "nownext", 0, 4, 12, 5, nil),
				w("sched", "schedule", 0, 9, 12, 7, map[string]string{"count": "all"}))},
		{Key: "lobby", Name: "Room lobby", Kind: "walkin", Desc: "Room title, clock, stage messages and the next sessions",
			Layout: land(8,
				w("title", "showtitle", 0, 0, 12, 1, nil),
				w("clock", "wallclock", 0, 1, 5, 3, clock),
				w("msgs", "messages", 5, 1, 7, 3, nil),
				w("sched", "schedule", 0, 4, 12, 4, map[string]string{"count": "6"}))},
		// --- Presenter displays (face the speaker) ---
		{Key: "lobby-portrait", Name: "Room lobby (portrait)", Kind: "walkin", Desc: "Poster version of the room lobby",
			Layout: port(16,
				w("title", "showtitle", 0, 0, 12, 2, nil),
				w("clock", "wallclock", 0, 2, 12, 3, clock),
				w("msgs", "messages", 0, 5, 12, 4, nil),
				w("sched", "schedule", 0, 9, 12, 7, map[string]string{"count": "6"}))},
		{Key: "dsm", Name: "Presenter (DSM)", Kind: "presenter", Desc: "Big countdown, stage messages, next session and items shown to the presenter",
			Layout: land(8,
				w("countdown", "countdown", 0, 0, 8, 4, map[string]string{"tenths": "1"}),
				w("item", "poll", 8, 0, 4, 4, map[string]string{"target": "presenter"}),
				w("label", "cuelabel", 0, 4, 8, 1, map[string]string{"source": "label"}),
				w("msgs", "messages", 8, 4, 4, 2, nil),
				w("progress", "progress", 0, 5, 8, 1, nil),
				w("next", "nextup", 0, 6, 8, 2, nil),
				w("clock", "wallclock", 8, 6, 4, 2, clock))},
		{Key: "stage", Name: "Full timer", Kind: "presenter", Desc: "The classic timer wall: giant countdown, messages, next, progress",
			Layout: land(7,
				w("countdown", "countdown", 0, 0, 8, 3, map[string]string{"tenths": "1"}),
				w("messages", "messages", 8, 0, 4, 3, nil),
				w("cuelabel", "cuelabel", 0, 3, 8, 1, map[string]string{"source": "label"}),
				w("nextup", "nextup", 8, 3, 4, 2, nil),
				w("progress", "progress", 0, 4, 8, 1, nil),
				w("dayprogress", "dayprogress", 0, 5, 8, 1, nil),
				w("wallclock", "wallclock", 8, 5, 4, 2, clock),
				w("showtitle", "showtitle", 0, 6, 8, 1, nil))},
		{Key: "stage-portrait", Name: "Full timer (portrait)", Kind: "presenter", Desc: "Portrait confidence monitor: giant countdown, messages, session and next",
			Layout: port(16,
				w("cuelabel", "cuelabel", 0, 0, 12, 2, map[string]string{"source": "label"}),
				w("countdown", "countdown", 0, 2, 12, 6, map[string]string{"tenths": "1"}),
				w("progress", "progress", 0, 8, 12, 1, nil),
				w("messages", "messages", 0, 9, 12, 3, nil),
				w("nextup", "nextup", 0, 12, 12, 2, nil),
				w("wallclock", "wallclock", 0, 14, 12, 2, clock))},
		{Key: "timer", Name: "Countdown only", Kind: "presenter", Desc: "Just the countdown and stage messages: the cleanest confidence monitor",
			Layout: land(8,
				w("countdown", "countdown", 0, 0, 12, 6, map[string]string{"tenths": "1"}),
				w("messages", "messages", 0, 6, 12, 2, nil))},
		{Key: "timer-portrait", Name: "Countdown only (portrait)", Kind: "presenter", Desc: "Portrait version of countdown only",
			Layout: port(16,
				w("countdown", "countdown", 0, 0, 12, 11, map[string]string{"tenths": "1"}),
				w("messages", "messages", 0, 11, 12, 5, nil))},
		{Key: "speaker", Name: "Speaker support", Kind: "presenter", Desc: "Who is on, the session, progress and what comes next",
			Layout: land(7,
				w("speaker", "speaker", 0, 0, 12, 2, nil),
				w("cuelabel", "cuelabel", 0, 2, 12, 2, map[string]string{"source": "label"}),
				w("progress", "progress", 0, 4, 12, 1, nil),
				w("nextup", "nextup", 0, 5, 8, 2, nil),
				w("wallclock", "wallclock", 8, 5, 4, 2, clock))},
		// --- Fillers (any display) ---
		{Key: "clockroom", Name: "Clock", Kind: "walkin", Desc: "Big clock with the next few sessions",
			Layout: land(6,
				w("wallclock", "wallclock", 0, 0, 12, 2, clock),
				w("schedule", "schedule", 0, 2, 12, 4, map[string]string{"count": "4"}))},
		{Key: "clockroom-portrait", Name: "Clock (portrait)", Kind: "walkin", Desc: "Poster version of the clock",
			Layout: port(16,
				w("wallclock", "wallclock", 0, 0, 12, 5, clock),
				w("schedule", "schedule", 0, 5, 12, 11, map[string]string{"count": "6"}))},
		{Key: "break", Name: "Break", Kind: "audience", Desc: "Between sessions: clock, stage messages and a notice",
			Layout: land(8,
				w("wallclock", "wallclock", 0, 0, 12, 3, clock),
				w("messages", "messages", 0, 3, 12, 3, nil),
				w("notice", "notice", 0, 6, 12, 2, map[string]string{"text": "Back shortly — enjoy the break"}))},
		{Key: "break-portrait", Name: "Break (portrait)", Kind: "audience", Desc: "Poster version of the break screen",
			Layout: port(16,
				w("wallclock", "wallclock", 0, 0, 12, 5, clock),
				w("messages", "messages", 0, 5, 12, 6, nil),
				w("notice", "notice", 0, 11, 12, 5, map[string]string{"text": "Back shortly — enjoy the break"}))},
	}
}

// FitTemplate keeps a screen's built-in layout in step with its display
// type and mounting: the template itself when it already fits, else its
// portrait/landscape twin ("x" ↔ "x-portrait"), else the type's first
// template of the right shape. kind "" keeps the template's own type.
func FitTemplate(key, kind string, portrait bool) string {
	ts := Templates()
	byKey := map[string]TemplateInfo{}
	for _, t := range ts {
		byKey[t.Key] = t
	}
	if kind == "" {
		kind = byKey[key].Kind
	}
	fits := func(k string) bool {
		t, ok := byKey[k]
		return ok && (kind == "" || t.Kind == kind) && (t.Layout.Orientation == "portrait") == portrait
	}
	base := strings.TrimSuffix(key, "-portrait")
	for _, k := range []string{key, base, base + "-portrait"} {
		if fits(k) {
			return k
		}
	}
	for _, t := range ts {
		if fits(t.Key) {
			return t.Key
		}
	}
	return key
}

// TemplateLayouts maps template key → layout (capture + apply paths).
func TemplateLayouts() map[string]Layout {
	out := map[string]Layout{}
	for _, t := range Templates() {
		out[t.Key] = t.Layout
	}
	return out
}

// DefOf looks a type up in the registry (nil when unknown).
func DefOf(t string) *WidgetDef {
	if i := slices.IndexFunc(WidgetTypes, func(d WidgetDef) bool { return d.Type == t }); i >= 0 {
		return &WidgetTypes[i]
	}
	return nil
}

// ValidType reports whether t is a registered widget type.
func ValidType(t string) bool { return DefOf(t) != nil }

// DefaultLayout is the factory board: countdown + messages on top, cue
// meta + next-up below, progress bars, wall clock, rate, title, schedule.
// Hand-checked overlap-free on the 12-column grid.
func DefaultLayout() Layout {
	w := func(id, typ string, x, y, hw, hh int, opts map[string]string) Widget {
		return Widget{ID: id, Type: typ, X: x, Y: y, W: hw, H: hh, Opts: opts}
	}
	return Layout{V: 1, Widgets: []Widget{
		w("countdown", "countdown", 0, 0, 8, 3, map[string]string{"tenths": "1"}),
		w("messages", "messages", 8, 0, 4, 3, nil),
		w("cuelabel", "cuelabel", 0, 3, 5, 1, map[string]string{"source": "label"}),
		w("speaker", "speaker", 5, 3, 3, 1, nil),
		w("nextup", "nextup", 8, 3, 4, 2, nil),
		w("progress", "progress", 0, 4, 8, 1, nil),
		w("wallclock", "wallclock", 8, 5, 4, 1, map[string]string{"tenths": "0"}),
		w("dayprogress", "dayprogress", 0, 5, 8, 1, nil),
		w("rate", "rate", 0, 6, 2, 1, nil),
		w("showtitle", "showtitle", 2, 6, 6, 1, nil),
		w("schedule", "schedule", 8, 6, 4, 4, map[string]string{"count": "5"}),
		w("notice", "notice", 0, 10, 12, 2, map[string]string{"text": "Welcome"}),
	}}
}

// DefaultLayoutJSON is the factory layout serialized ( persistence + the
// board.js Reset mirror — that file documents the duplication).
func DefaultLayoutJSON() string {
	raw, _ := json.Marshal(DefaultLayout())
	return string(raw)
}

// NormalizeLayout clamps every widget into the grid in place order:
// X∈[0,11], W∈[1,12] with X+W≤12, Y∈[0,MaxY], H∈[1,MaxH] with Y+H≤MaxRows
// (the canvas never holds more rows, so a lower tile would be hidden); empty ids are
// filled (w1…), opts maps sanitized (length caps). Unknown types and
// overlaps are NOT fixed here — ValidateLayout rejects those.
func NormalizeLayout(in Layout) Layout {
	out := Layout{V: 1, Rows: in.Rows, Orientation: in.Orientation, Anim: in.Anim, AnimMS: in.AnimMS, Widgets: make([]Widget, 0, len(in.Widgets))}
	if out.Orientation != "portrait" {
		out.Orientation = "landscape"
	}
	switch out.Anim {
	case "fade", "slide", "pop", "none":
	default:
		out.Anim = "fade"
	}
	if out.AnimMS < 120 || out.AnimMS > 3000 {
		out.AnimMS = 400
	}
	seen := map[string]bool{}
	for i, w := range in.Widgets {
		w.ID = strings.TrimSpace(w.ID)
		if w.ID == "" {
			w.ID = fmt.Sprintf("w%d", i+1)
		}
		if len(w.ID) > 32 {
			w.ID = w.ID[:32]
		}
		// Duplicate ids get a numeric suffix (stays addressable client-side).
		base := w.ID
		for n := 2; seen[w.ID]; n++ {
			w.ID = fmt.Sprintf("%s-%d", base, n)
		}
		seen[w.ID] = true

		w.Type = strings.TrimSpace(w.Type)
		if w.X < 0 {
			w.X = 0
		}
		if w.X > GridCols-1 {
			w.X = GridCols - 1
		}
		if w.W < 1 {
			w.W = 1
		}
		if w.W > GridCols {
			w.W = GridCols
		}
		if w.X+w.W > GridCols {
			w.W = GridCols - w.X
		}
		if w.Y < 0 {
			w.Y = 0
		}
		if w.Y > MaxY {
			w.Y = MaxY
		}
		if w.H < 1 {
			w.H = 1
		}
		if w.H > MaxH {
			w.H = MaxH
		}
		if w.Y+w.H > MaxRows {
			w.H = MaxRows - w.Y
		}
		w.Opts = sanitizeOpts(w.Opts)
		out.Widgets = append(out.Widgets, w)
	}
	// Stable paint order: top-to-bottom, left-to-right.
	slices.SortStableFunc(out.Widgets, func(a, b Widget) int {
		return cmp.Or(cmp.Compare(a.Y, b.Y), cmp.Compare(a.X, b.X))
	})
	// The canvas always holds every tile.
	if out.Rows <= 0 {
		out.Rows = DefaultRowsLandscape
		if out.Orientation == "portrait" {
			out.Rows = DefaultRowsPortrait
		}
	}
	if ext := out.Extent(); out.Rows < ext {
		out.Rows = ext
	}
	if out.Rows > MaxRows {
		out.Rows = MaxRows
	}
	return out
}

// sanitizeOpts caps opts size/shape (client-supplied JSON must stay small).
func sanitizeOpts(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if len(out) >= 16 {
			break
		}
		k = strings.TrimSpace(k)
		if k == "" || len(k) > 32 {
			continue
		}
		v = timerpi.ClipUTF8(v, 256)
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// overlap reports whether a and b share grid cells.
func overlap(a, b Widget) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// Overlaps returns human-readable "id×id" pairs for every intersecting
// widget pair (empty = overlap-free).
func Overlaps(l Layout) []string {
	var pairs []string
	for i := range l.Widgets {
		for j := i + 1; j < len(l.Widgets); j++ {
			if overlap(l.Widgets[i], l.Widgets[j]) {
				pairs = append(pairs, l.Widgets[i].ID+"×"+l.Widgets[j].ID)
			}
		}
	}
	return pairs
}

// ParseLayout decodes layout_json (object {"v":…,"widgets":[…]} or a bare
// […] array for forward tolerance) and returns the NORMALIZED layout.
// Validation (unknown types, overlaps, size caps) is ValidateLayout's job.
func ParseLayout(raw string) (Layout, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Layout{}, fmt.Errorf("boards: empty layout")
	}
	var l Layout
	if strings.HasPrefix(s, "[") {
		var ws []Widget
		dec := json.NewDecoder(strings.NewReader(s))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&ws); err != nil {
			return Layout{}, fmt.Errorf("boards: layout array: %w", err)
		}
		l = Layout{V: 1, Widgets: ws}
	} else {
		dec := json.NewDecoder(strings.NewReader(s))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&l); err != nil {
			return Layout{}, fmt.Errorf("boards: layout: %w", err)
		}
		if l.Widgets == nil {
			l.Widgets = []Widget{}
		}
	}
	return NormalizeLayout(l), nil
}

// ValidateLayout enforces the registry + geometry contract on raw
// layout_json: known widget types, ≤MaxWidgets tiles, overlap-free after
// normalization. Returns the normalized layout for storage.
func ValidateLayout(raw string) (Layout, error) {
	l, err := ParseLayout(raw)
	if err != nil {
		return Layout{}, err
	}
	if len(l.Widgets) == 0 {
		return Layout{}, fmt.Errorf("boards: layout needs at least one widget")
	}
	if len(l.Widgets) > MaxWidgets {
		return Layout{}, fmt.Errorf("boards: %d widgets (max %d)", len(l.Widgets), MaxWidgets)
	}
	for _, w := range l.Widgets {
		if !ValidType(w.Type) {
			return Layout{}, fmt.Errorf("boards: unknown widget type %q", w.Type)
		}
	}
	if pairs := Overlaps(l); len(pairs) > 0 {
		return Layout{}, fmt.Errorf("boards: overlapping widgets: %s", strings.Join(pairs, ", "))
	}
	return l, nil
}

// MarshalLayout serializes a (normalized) layout for storage.
func MarshalLayout(l Layout) string {
	raw, _ := json.Marshal(NormalizeLayout(l))
	return string(raw)
}

// ---------------------------------------------------------------------------
// Persistence — same sqlite file, additive table only.
// ---------------------------------------------------------------------------

// Board is one display_boards row.
type Board struct {
	ID        int64  `db:"id" json:"id"`
	ShowID    int64  `db:"show_id" json:"showId"`
	Name      string `db:"name" json:"name"`
	Layout    string `db:"layout_json" json:"-"`
	UpdatedAt int64  `db:"updated_at" json:"updatedAt"`
}

// LayoutJSON returns the raw layout document.
func (b Board) LayoutJSON() string { return b.Layout }

// Parsed decodes + normalizes the stored layout (stored docs are already
// valid; a corrupt row degrades to the factory layout, never an error page).
func (b Board) Parsed() Layout {
	if l, err := ParseLayout(b.Layout); err == nil && len(l.Widgets) > 0 {
		return NormalizeLayout(l)
	}
	return NormalizeLayout(DefaultLayout())
}

// Migrate creates the additive display_boards table (+index). Safe to run
// on every boot; CREATE IF NOT EXISTS only.
func Migrate(db *sqlx.DB) error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS display_boards (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			show_id    INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
			name       TEXT NOT NULL DEFAULT '',
			layout_json TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_boards_show ON display_boards (show_id, id);`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("boards: migrate: %w", err)
		}
	}
	return nil
}

func nowMS() int64 { return time.Now().UnixMilli() }

// Layouts belong to the EVENT (PRODUCT §7, 2026-10-06): every room of an
// event sees and can use the same layouts. Rows keep the room that made
// them (show_id, for the cascade on event delete); every lookup below is
// widened to "any room of showID's event". A room outside any event
// (event_id 0, never after adoption) only sees its own.
const sameEvent = `show_id IN (SELECT s2.id FROM shows s2 WHERE s2.event_id =
	(SELECT s1.event_id FROM shows s1 WHERE s1.id = ?) AND s2.event_id != 0) OR show_id = ?`

// ListBoards returns the event's layouts, oldest first (first = default).
func ListBoards(db *sqlx.DB, showID int64) ([]Board, error) {
	var out []Board
	err := db.Select(&out, `SELECT * FROM display_boards WHERE (`+sameEvent+`) ORDER BY id ASC`, showID, showID)
	return out, err
}

// GetBoard fetches one layout of showID's event; sql.ErrNoRows when
// missing (callers 404).
func GetBoard(db *sqlx.DB, showID, bid int64) (Board, error) {
	var b Board
	err := db.Get(&b, `SELECT * FROM display_boards WHERE (`+sameEvent+`) AND id = ?`, showID, showID, bid)
	if err != nil && err != sql.ErrNoRows {
		err = fmt.Errorf("boards: get %d: %w", bid, err)
	}
	return b, err
}

// CreateBoard inserts a board; an empty layout seeds the factory default.
// Names are trimmed, capped at 64 chars, and must be non-empty.
func CreateBoard(db *sqlx.DB, showID int64, name, layoutRaw string) (Board, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Board{}, fmt.Errorf("boards: name must not be empty")
	}
	if len(name) > 64 {
		name = timerpi.ClipUTF8(name, 64)
	}
	layout := strings.TrimSpace(layoutRaw)
	if layout == "" {
		layout = DefaultLayoutJSON()
	} else {
		l, err := ValidateLayout(layout)
		if err != nil {
			return Board{}, err
		}
		layout = MarshalLayout(l)
	}
	res, err := db.Exec(`INSERT INTO display_boards (show_id, name, layout_json, updated_at)
		VALUES (?, ?, ?, ?)`, showID, name, layout, nowMS())
	if err != nil {
		return Board{}, fmt.Errorf("boards: create: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Board{}, err
	}
	return GetBoard(db, showID, id)
}

// EnsureDefaultBoard returns the show's default board (first by id),
// creating a factory "Main" board when the show has none. The board view
// renders this when ?board= is absent.
func EnsureDefaultBoard(db *sqlx.DB, showID int64) (Board, error) {
	boards, err := ListBoards(db, showID)
	if err != nil {
		return Board{}, err
	}
	if len(boards) > 0 {
		return boards[0], nil
	}
	return CreateBoard(db, showID, "Main", "")
}

// RenameBoard updates the board name (non-empty, capped).
func RenameBoard(db *sqlx.DB, showID, bid int64, name string) (Board, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Board{}, fmt.Errorf("boards: name must not be empty")
	}
	if len(name) > 64 {
		name = timerpi.ClipUTF8(name, 64)
	}
	if _, err := db.Exec(`UPDATE display_boards SET name = ?, updated_at = ? WHERE (`+sameEvent+`) AND id = ?`,
		name, nowMS(), showID, showID, bid); err != nil {
		return Board{}, fmt.Errorf("boards: rename: %w", err)
	}
	return GetBoard(db, showID, bid)
}

// StoreLayout validates + stores a new layout document.
func StoreLayout(db *sqlx.DB, showID, bid int64, layoutRaw string) (Board, error) {
	l, err := ValidateLayout(layoutRaw)
	if err != nil {
		return Board{}, err
	}
	if _, err := db.Exec(`UPDATE display_boards SET layout_json = ?, updated_at = ? WHERE (`+sameEvent+`) AND id = ?`,
		MarshalLayout(l), nowMS(), showID, showID, bid); err != nil {
		return Board{}, fmt.Errorf("boards: store layout: %w", err)
	}
	return GetBoard(db, showID, bid)
}

// DeleteBoard removes a layout of showID's event (a room with none left
// re-seeds on next view via EnsureDefaultBoard).
func DeleteBoard(db *sqlx.DB, showID, bid int64) error {
	_, err := db.Exec(`DELETE FROM display_boards WHERE (`+sameEvent+`) AND id = ?`, showID, showID, bid)
	return err
}

// RehomeRoomLayouts hands the layouts a room made to another room of its
// event before the room is deleted (the row cascade would otherwise delete
// layouts other rooms' screens use). With no other room (the event is
// going too) nothing moves and the cascade removes them.
func RehomeRoomLayouts(db *sqlx.DB, showID int64) error {
	_, err := db.Exec(`UPDATE display_boards SET show_id = (
			SELECT s2.id FROM shows s2 WHERE s2.event_id = (SELECT event_id FROM shows WHERE id = ?)
			AND s2.event_id != 0 AND s2.id != ? ORDER BY s2.room_pos, s2.id LIMIT 1)
		WHERE show_id = ? AND EXISTS (
			SELECT 1 FROM shows s2 WHERE s2.event_id = (SELECT event_id FROM shows WHERE id = ?)
			AND s2.event_id != 0 AND s2.id != ?)`, showID, showID, showID, showID, showID)
	if err != nil {
		return fmt.Errorf("boards: rehome: %w", err)
	}
	return nil
}
