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
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// Grid geometry.
const (
	GridCols   = 12
	MaxWidgets = 48
	MaxY       = 99
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

// Layout is the persisted board geometry (layout_json shape).
type Layout struct {
	V       int      `json:"v"`
	Widgets []Widget `json:"widgets"`
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
	{Type: "wallclock", Title: "Wall clock", Desc: "Time of day (lobby filler)", DefaultW: 4, DefaultH: 1},
	{Type: "progress", Title: "Cue progress", Desc: "Active-cue progress bar", DefaultW: 8, DefaultH: 1},
	{Type: "dayprogress", Title: "Day progress", Desc: "Whole-day progress bar", DefaultW: 8, DefaultH: 1},
	{Type: "messages", Title: "Messages", Desc: "Shown stage-message overlay lines", DefaultW: 4, DefaultH: 3},
	{Type: "showtitle", Title: "Show title", Desc: "Show title + share code", DefaultW: 6, DefaultH: 1},
	{Type: "rate", Title: "Rate chip", Desc: "Countdown rate multiplier", DefaultW: 2, DefaultH: 1},
	{Type: "schedule", Title: "Schedule", Desc: "Mini running-order list", DefaultW: 4, DefaultH: 4},
	{Type: "notice", Title: "Notice", Desc: "Static free text (welcome, sponsors, Wi-Fi…)", DefaultW: 6, DefaultH: 2},
	// PLAN §11.2 (Rooms v2, phase 2): the audience/venue slots.
	{Type: "poll", Title: "Poll", Desc: "On-air poll: question, live counts, results bars", DefaultW: 6, DefaultH: 4},
	{Type: "qa", Title: "Q&A", Desc: "Open audience question + likes", DefaultW: 6, DefaultH: 2},
	{Type: "wordcloud", Title: "Word cloud", Desc: "Approved audience words as tiles", DefaultW: 6, DefaultH: 4},
	{Type: "map", Title: "Map", Desc: "Venue map image (upload under /api/assets)", DefaultW: 6, DefaultH: 4},
	{Type: "joinqr", Title: "Join QR", Desc: "Audience join QR for this room", DefaultW: 3, DefaultH: 4},
}

// TemplateLayouts are the Rooms display templates (PLAN §11.2): named
// starting layouts for the walk-in / main / DSM surfaces, applied to a
// board in one click from the board chrome (fetched from
// GET /api/board-templates — Go is the single source of truth so the
// overlap test in boards_test.go covers every shape).
func TemplateLayouts() map[string]Layout {
	w := func(id, typ string, x, y, hw, hh int, opts map[string]string) Widget {
		return Widget{ID: id, Type: typ, X: x, Y: y, W: hw, H: hh, Opts: opts}
	}
	return map[string]Layout{
		// 1. SHOW — the classic cue wall: giant timer + messages + next.
		"stage": {V: 1, Widgets: []Widget{
			w("countdown", "countdown", 0, 0, 8, 3, map[string]string{"tenths": "1"}),
			w("messages", "messages", 8, 0, 4, 3, nil),
			w("cuelabel", "cuelabel", 0, 3, 8, 1, map[string]string{"source": "label"}),
			w("nextup", "nextup", 8, 3, 4, 2, nil),
			w("progress", "progress", 0, 4, 8, 1, nil),
			w("dayprogress", "dayprogress", 0, 5, 8, 1, nil),
			w("wallclock", "wallclock", 8, 5, 4, 1, map[string]string{"tenths": "0"}),
			w("rate", "rate", 0, 6, 2, 1, nil),
			w("showtitle", "showtitle", 2, 6, 6, 1, nil),
		}},
		// 2. LOBBY — clock + messages + the running order.
		"lobby": {V: 1, Widgets: []Widget{
			w("showtitle", "showtitle", 0, 0, 12, 1, nil),
			w("wallclock", "wallclock", 0, 1, 5, 2, map[string]string{"tenths": "0"}),
			w("messages", "messages", 5, 1, 7, 2, nil),
			w("schedule", "schedule", 0, 3, 12, 4, map[string]string{"count": "6"}),
		}},
		// 3. EVENT LOBBY — space map + full-day spine (walkthrough #1).
		"event": {V: 1, Widgets: []Widget{
			w("showtitle", "showtitle", 0, 0, 12, 1, nil),
			w("map", "map", 0, 1, 5, 5, nil),
			w("schedule", "schedule", 5, 1, 7, 5, map[string]string{"count": "all"}),
			w("wallclock", "wallclock", 0, 6, 4, 1, map[string]string{"tenths": "0"}),
			w("notice", "notice", 4, 6, 8, 1, map[string]string{"text": "Welcome"}),
		}},
		// 4. ROOM WALK-IN — what's on + next-in-room + schedule + join QR (#2/#3).
		"room": {V: 1, Widgets: []Widget{
			w("showtitle", "showtitle", 0, 0, 12, 1, nil),
			w("wallclock", "wallclock", 0, 1, 4, 2, map[string]string{"tenths": "0"}),
			w("cuelabel", "cuelabel", 4, 1, 8, 1, map[string]string{"source": "label"}),
			w("speaker", "speaker", 4, 2, 8, 1, nil),
			w("nextup", "nextup", 0, 3, 4, 2, nil),
			w("schedule", "schedule", 4, 3, 8, 4, map[string]string{"count": "8"}),
			w("joinqr", "joinqr", 0, 6, 4, 3, nil),
			w("notice", "notice", 4, 7, 8, 2, map[string]string{"text": "Scan to take part"}),
		}},
		// 5. ROOM MAIN — the audience interaction surface (#4/#5).
		"main": {V: 1, Widgets: []Widget{
			w("showtitle", "showtitle", 0, 0, 12, 1, nil),
			w("poll", "poll", 0, 1, 7, 5, nil),
			w("qa", "qa", 7, 1, 5, 2, nil),
			w("wordcloud", "wordcloud", 7, 3, 5, 3, nil),
			w("joinqr", "joinqr", 0, 6, 3, 3, nil),
			w("notice", "notice", 3, 6, 9, 2, map[string]string{"text": "Scan to take part"}),
		}},
		// 6. DSM — the room's progress timer + its poll wedge (#6/#7).
		"dsm": {V: 1, Widgets: []Widget{
			w("countdown", "countdown", 0, 0, 8, 3, map[string]string{"tenths": "1"}),
			w("poll", "poll", 8, 0, 4, 3, nil),
			w("progress", "progress", 0, 3, 8, 1, nil),
			w("cuelabel", "cuelabel", 0, 4, 8, 1, map[string]string{"source": "label"}),
			w("speaker", "speaker", 0, 5, 8, 1, nil),
			w("nextup", "nextup", 8, 3, 4, 2, nil),
			w("wallclock", "wallclock", 0, 6, 4, 1, map[string]string{"tenths": "0"}),
			w("joinqr", "joinqr", 8, 5, 4, 2, nil),
			w("dayprogress", "dayprogress", 4, 6, 4, 1, nil),
		}},
		// 7. SPEAKER TAG — presenter support shot: who + what + the clock.
		"speaker": {V: 1, Widgets: []Widget{
			w("speaker", "speaker", 0, 0, 12, 2, nil),
			w("cuelabel", "cuelabel", 0, 2, 12, 2, map[string]string{"source": "label"}),
			w("progress", "progress", 0, 4, 12, 1, nil),
			w("nextup", "nextup", 0, 5, 8, 2, nil),
			w("wallclock", "wallclock", 8, 5, 4, 2, map[string]string{"tenths": "0"}),
		}},
		// 8. Q&A WALL — questions front-and-center + join.
		"qawall": {V: 1, Widgets: []Widget{
			w("qa", "qa", 0, 0, 8, 5, nil),
			w("joinqr", "joinqr", 8, 0, 4, 3, nil),
			w("wordcloud", "wordcloud", 8, 3, 4, 2, nil),
			w("notice", "notice", 0, 5, 12, 2, map[string]string{"text": "Questions? Scan and ask."}),
		}},
		// 9. CLOCK ROOM — near-idle room filler: big clock + shallow schedule.
		"clockroom": {V: 1, Widgets: []Widget{
			w("wallclock", "wallclock", 0, 0, 12, 2, map[string]string{"tenths": "0"}),
			w("schedule", "schedule", 0, 2, 12, 4, map[string]string{"count": "4"}),
		}},
		// 10. BREAK — between-sessions filler: clock + stage messages.
		"break": {V: 1, Widgets: []Widget{
			w("wallclock", "wallclock", 0, 0, 12, 3, map[string]string{"tenths": "0"}),
			w("messages", "messages", 0, 3, 12, 3, nil),
			w("notice", "notice", 0, 6, 12, 2, map[string]string{"text": "Back shortly — enjoy the break"}),
		}},
	}
}

// widgetDefOf looks a type up in the registry (nil when unknown).
func widgetDefOf(t string) *WidgetDef {
	for i := range WidgetTypes {
		if WidgetTypes[i].Type == t {
			return &WidgetTypes[i]
		}
	}
	return nil
}

// ValidType reports whether t is a registered widget type.
func ValidType(t string) bool { return widgetDefOf(t) != nil }

// DefOf exposes one registry entry (nil when unknown) — tests and the
// palette renderer read defaults from here.
func DefOf(t string) *WidgetDef { return widgetDefOf(t) }

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
// X∈[0,11], W∈[1,12] with X+W≤12, Y∈[0,MaxY], H∈[1,MaxH]; empty ids are
// filled (w1…), opts maps sanitized (length caps). Unknown types and
// overlaps are NOT fixed here — ValidateLayout rejects those.
func NormalizeLayout(in Layout) Layout {
	out := Layout{V: 1, Widgets: make([]Widget, 0, len(in.Widgets))}
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
		w.Opts = sanitizeOpts(w.Opts)
		out.Widgets = append(out.Widgets, w)
	}
	// Stable paint order: top-to-bottom, left-to-right.
	sort.SliceStable(out.Widgets, func(a, b int) bool {
		if out.Widgets[a].Y != out.Widgets[b].Y {
			return out.Widgets[a].Y < out.Widgets[b].Y
		}
		return out.Widgets[a].X < out.Widgets[b].X
	})
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
		if len(v) > 256 {
			v = v[:256]
		}
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
		return l
	}
	return DefaultLayout()
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

// ListBoards returns the show's boards, oldest first (first = default).
func ListBoards(db *sqlx.DB, showID int64) ([]Board, error) {
	var out []Board
	err := db.Select(&out, `SELECT * FROM display_boards WHERE show_id = ? ORDER BY id ASC`, showID)
	return out, err
}

// GetBoard fetches one board; sql.ErrNoRows when missing (callers 404).
func GetBoard(db *sqlx.DB, showID, bid int64) (Board, error) {
	var b Board
	err := db.Get(&b, `SELECT * FROM display_boards WHERE show_id = ? AND id = ?`, showID, bid)
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
		name = name[:64]
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
		name = name[:64]
	}
	if _, err := db.Exec(`UPDATE display_boards SET name = ?, updated_at = ? WHERE show_id = ? AND id = ?`,
		name, nowMS(), showID, bid); err != nil {
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
	if _, err := db.Exec(`UPDATE display_boards SET layout_json = ?, updated_at = ? WHERE show_id = ? AND id = ?`,
		MarshalLayout(l), nowMS(), showID, bid); err != nil {
		return Board{}, fmt.Errorf("boards: store layout: %w", err)
	}
	return GetBoard(db, showID, bid)
}

// DeleteBoard removes a board (a show with none left re-seeds on next view
// via EnsureDefaultBoard).
// UpsertLayoutByName creates or replaces the show board with this name —
// the capture round's template picker (one board per captured screen keeps
// its customizations isolated from every other screen on the template).
func UpsertLayoutByName(db *sqlx.DB, showID int64, name, layoutRaw string) (Board, error) {
	if list, err := ListBoards(db, showID); err == nil {
		for _, b := range list {
			if b.Name != name {
				continue
			}
			return StoreLayout(db, showID, b.ID, layoutRaw)
		}
	}
	return CreateBoard(db, showID, name, layoutRaw)
}

func DeleteBoard(db *sqlx.DB, showID, bid int64) error {
	_, err := db.Exec(`DELETE FROM display_boards WHERE show_id = ? AND id = ?`, showID, bid)
	return err
}
