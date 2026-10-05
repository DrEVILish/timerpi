package boards_test

import (
	"testing"

	"timerpi/boards"
)

// Board.Parsed degrades corrupt/empty stored rows to the factory layout
// (never an error page); persistence tests only round-tripped valid rows.
func TestParsedCorruptRowFallsBackToFactory(t *testing.T) {
	factory := boards.DefaultLayout()
	if len(factory.Widgets) == 0 {
		t.Fatal("factory layout is empty")
	}
	for name, raw := range map[string]string{
		"garbage":       "not json",
		"empty object":  "{}",
		"empty widgets": `{"v":1,"widgets":[]}`,
		"null":          "null",
	} {
		if got := (boards.Board{Layout: raw}).Parsed(); len(got.Widgets) != len(factory.Widgets) {
			t.Errorf("%s: Parsed() = %d widgets, want factory %d", name, len(got.Widgets), len(factory.Widgets))
		}
	}

	// A valid stored doc parses as-is (no factory substitution).
	raw := boards.DefaultLayoutJSON()
	if got := (boards.Board{Layout: raw}).Parsed(); len(got.Widgets) != len(factory.Widgets) {
		t.Errorf("valid row: Parsed() = %d widgets, want %d", len(got.Widgets), len(factory.Widgets))
	}
}

// E2 notice widget: registered type, validates, and normalizes like the
// other eleven (unknown-type rejection is unchanged).
func TestNoticeWidgetRegistered(t *testing.T) {
	if !boards.ValidType("notice") {
		t.Fatal("notice not a registered widget type")
	}
	l, err := boards.ValidateLayout(`{"v":1,"widgets":[{"id":"n","type":"notice","x":0,"y":0,"w":6,"h":2,"opts":{"text":"Welcome"}}]}`)
	if err != nil {
		t.Fatalf("notice layout rejected: %v", err)
	}
	if l.Widgets[0].Opts["text"] != "Welcome" {
		t.Fatalf("notice text lost: %+v", l.Widgets[0])
	}
}
