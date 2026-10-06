// Package boards tests: layout validation (overlap/clamp/normalize,
// widget-type registry) + sqlite persistence round-trip on :memory:.
package boards_test

import (
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"timerpi/boards"
)

func memDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE shows (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT, event_id INTEGER NOT NULL DEFAULT 0, room_pos INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatalf("stub shows: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO shows (title) VALUES ('Board Show')`); err != nil {
		t.Fatalf("stub show row: %v", err)
	}
	if err := boards.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// The factory layout must be valid: known types, in-grid, overlap-free.
func TestDefaultLayoutValid(t *testing.T) {
	raw := boards.DefaultLayoutJSON()
	l, err := boards.ValidateLayout(raw)
	if err != nil {
		t.Fatalf("factory layout invalid: %v", err)
	}
	// The factory board is the TIMER surface; the Rooms types (poll, qa,
	// wordcloud, map, joinqr — PLAN §11.2) are opt-in via templates, so
	// the factory must COVER its types from the registry, not equal it.
	if len(l.Widgets) > len(boards.WidgetTypes) {
		t.Errorf("factory has %d widgets, registry only %d types", len(l.Widgets), len(boards.WidgetTypes))
	}
	seen := map[string]bool{}
	for _, w := range l.Widgets {
		if seen[w.Type] {
			t.Errorf("factory duplicates type %q", w.Type)
		}
		seen[w.Type] = true
	}
}

// Unknown widget types are rejected (registry closed).
func TestValidateRejectsUnknownType(t *testing.T) {
	raw := `{"v":1,"widgets":[{"id":"a","type":"hologram","x":0,"y":0,"w":4,"h":2}]}`
	if _, err := boards.ValidateLayout(raw); err == nil || !strings.Contains(err.Error(), "hologram") {
		t.Fatalf("want unknown-type error, got %v", err)
	}
}

// Overlapping rectangles are rejected, naming the pair.
func TestValidateRejectsOverlap(t *testing.T) {
	raw := `{"v":1,"widgets":[` +
		`{"id":"a","type":"countdown","x":0,"y":0,"w":6,"h":2},` +
		`{"id":"b","type":"speaker","x":4,"y":1,"w":4,"h":2}]}` // shares cols 4-5, row 1
	_, err := boards.ValidateLayout(raw)
	if err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("want overlap error, got %v", err)
	}
	if !strings.Contains(err.Error(), "a×b") {
		t.Errorf("overlap error should name the pair, got %v", err)
	}
}

// Adjacent tiles (touching edges, no shared cells) are fine.
func TestValidateAllowsAdjacent(t *testing.T) {
	raw := `{"v":1,"widgets":[` +
		`{"id":"a","type":"countdown","x":0,"y":0,"w":6,"h":2},` +
		`{"id":"b","type":"speaker","x":6,"y":0,"w":6,"h":2}]}` // edge-touch at col 6
	if _, err := boards.ValidateLayout(raw); err != nil {
		t.Fatalf("adjacent tiles must pass: %v", err)
	}
}

// Clamp/normalize: off-grid geometry is pulled inside (not rejected).
func TestNormalizeClamps(t *testing.T) {
	raw := `{"v":1,"widgets":[` +
		`{"id":"","type":"countdown","x":-3,"y":0,"w":99,"h":0},` +
		`{"id":"b","type":"speaker","x":11,"y":1,"w":4,"h":2}]}` // x+w overflows → w=1
	l, err := boards.ParseLayout(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a := l.Widgets[0]
	if a.ID == "" {
		t.Error("empty id should be filled")
	}
	if a.X != 0 || a.W != 12 || a.H != 1 {
		t.Errorf("clamp a = %+v (want x0 w12 h1)", a)
	}
	b := l.Widgets[1]
	if b.W != 1 {
		t.Errorf("clamp b.w = %d (want 1, x+w≤12)", b.W)
	}
	if _, err := boards.ValidateLayout(raw); err != nil {
		t.Fatalf("clamped layout must validate: %v", err)
	}
}

// Duplicate ids are de-suffixed, never stored twice.
func TestNormalizeDedupesIDs(t *testing.T) {
	raw := `{"v":1,"widgets":[` +
		`{"id":"a","type":"countdown","x":0,"y":0,"w":6,"h":1},` +
		`{"id":"a","type":"speaker","x":6,"y":0,"w":6,"h":1}]}`
	l, err := boards.ParseLayout(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if l.Widgets[0].ID == l.Widgets[1].ID {
		t.Errorf("duplicate ids survive: %+v", l.Widgets)
	}
}

// Empty layouts and bare arrays: empty rejected, bare array accepted.
func TestLayoutShapes(t *testing.T) {
	if _, err := boards.ValidateLayout(`{"v":1,"widgets":[]}`); err == nil {
		t.Error("empty widget list must be rejected")
	}
	if _, err := boards.ValidateLayout(`[{"id":"a","type":"rate","x":0,"y":0,"w":2,"h":1}]`); err != nil {
		t.Errorf("bare array must be accepted: %v", err)
	}
	if _, err := boards.ValidateLayout(`not json`); err == nil {
		t.Error("garbage must be rejected")
	}
}

// Persistence round-trip: default seeding, rename, layout store, delete.
func TestBoardPersistence(t *testing.T) {
	db := memDB(t)

	def, err := boards.EnsureDefaultBoard(db, 1)
	if err != nil {
		t.Fatalf("ensure default: %v", err)
	}
	if def.Name != "Main" {
		t.Errorf("default name = %q (want Main)", def.Name)
	}
	if len(def.Parsed().Widgets) == 0 {
		t.Error("default board has no widgets")
	}
	// Second ensure returns the SAME board (no duplicates).
	again, err := boards.EnsureDefaultBoard(db, 1)
	if err != nil || again.ID != def.ID {
		t.Fatalf("ensure idempotent: %v %+v", err, again)
	}

	created, err := boards.CreateBoard(db, 1, "Lobby", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == def.ID {
		t.Error("create reused the default id")
	}
	list, err := boards.ListBoards(db, 1)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d, %v", len(list), err)
	}

	renamed, err := boards.RenameBoard(db, 1, created.ID, "  Foyer  ")
	if err != nil || renamed.Name != "Foyer" {
		t.Fatalf("rename: %v %+v", err, renamed)
	}
	if _, err := boards.RenameBoard(db, 1, created.ID, "   "); err == nil {
		t.Error("empty rename must fail")
	}

	// Unknown-type layout refused at the store boundary.
	if _, err := boards.StoreLayout(db, 1, created.ID,
		`{"v":1,"widgets":[{"id":"x","type":"nope","x":0,"y":0,"w":2,"h":1}]}`); err == nil {
		t.Error("bad-type store must fail")
	}
	good := `{"v":1,"widgets":[{"id":"x","type":"rate","x":0,"y":0,"w":2,"h":1}]}`
	stored, err := boards.StoreLayout(db, 1, created.ID, good)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if len(stored.Parsed().Widgets) != 1 || stored.Parsed().Widgets[0].Type != "rate" {
		t.Errorf("stored layout = %+v", stored.Parsed())
	}

	if err := boards.DeleteBoard(db, 1, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := boards.GetBoard(db, 1, created.ID); err == nil {
		t.Error("deleted board still readable")
	}
}

// Migrate is additive + idempotent (second run is a no-op).
func TestMigrateIdempotent(t *testing.T) {
	db := memDB(t)
	if err := boards.Migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// BUGLOG RW33: no tile may sit below the canvas (MaxRows). A tile at y=80
// used to validate while the CSS grid (48 rows) hid it.
func TestNormalizeKeepsTilesInsideCanvas(t *testing.T) {
	raw := `{"v":1,"widgets":[` +
		`{"id":"a","type":"countdown","x":0,"y":80,"w":4,"h":3},` +
		`{"id":"b","type":"wallclock","x":4,"y":46,"w":4,"h":6}]}`
	l, err := boards.ValidateLayout(raw)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, w := range l.Widgets {
		if w.Y+w.H > boards.MaxRows || w.H < 1 {
			t.Errorf("tile %s y=%d h=%d reaches past row %d", w.ID, w.Y, w.H, boards.MaxRows)
		}
		if w.Y+w.H > l.Rows {
			t.Errorf("tile %s y=%d h=%d outside the %d-row canvas", w.ID, w.Y, w.H, l.Rows)
		}
	}
}
