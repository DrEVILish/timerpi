// Phase 2 tests (PLAN §11.2): the Rooms display templates are overlap-free
// and registry-valid; assets upload/serve/delete with sniffed mimes; the
// board PUT accepts the new widget types; the wordcloud children ride
// ActivePoll; the zone map pointer round-trips and reaches the walk-in page.
package boards_test

import (
	"encoding/json"
	"testing"

	"timerpi/boards"
)

func mustJSON(t *testing.T, l boards.Layout) string {
	t.Helper()
	raw, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// Every Rooms template must pass the same validation a client PUT would —
// a template that the REST layer would reject is a broken product.
func TestTemplateLayoutsValid(t *testing.T) {
	names := []string{"stage", "lobby", "event", "room", "main", "dsm", "speaker", "qawall", "clockroom", "break"}
	tl := boards.TemplateLayouts()
	for _, name := range names {
		l, ok := tl[name]
		if !ok {
			t.Fatalf("template %q missing", name)
		}
		if _, err := boards.ValidateLayout(mustJSON(t, l)); err != nil {
			t.Errorf("template %q invalid: %v", name, err)
		}
		if len(l.Widgets) == 0 {
			t.Errorf("template %q has no widgets", name)
		}
		for _, w := range l.Widgets {
			if !boards.ValidType(w.Type) {
				t.Errorf("template %q uses unregistered type %q", name, w.Type)
			}
		}
	}
}

// The new audience/venue widget types are registered (palette + PUT
// validation) and carry sane defaults.
func TestAudienceWidgetTypes(t *testing.T) {
	for _, typ := range []string{"poll", "qa", "wordcloud", "map", "joinqr"} {
		if !boards.ValidType(typ) {
			t.Errorf("type %q not registered", typ)
			continue
		}
		def := boards.DefOf(typ)
		if def.DefaultW <= 0 || def.DefaultH <= 0 {
			t.Errorf("type %q has degenerate defaults %dx%d", typ, def.DefaultW, def.DefaultH)
		}
	}
	if boards.ValidType("subway") {
		t.Error("garbage type accepted")
	}
}
