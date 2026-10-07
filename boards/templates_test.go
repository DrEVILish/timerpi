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
	names := []string{"stage", "lobby", "event", "room", "main", "dsm", "speaker", "qawall", "clockroom", "break", "holding", "timer"}
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

// The catalog is four layouts per display type, each with a landscape
// and a portrait version (owner 2026-10-07: 24 built-in layouts), every
// version inside its canvas.
func TestTemplateCatalogCanvas(t *testing.T) {
	kinds := map[string]int{}
	versions := 0
	for _, tpl := range boards.Templates() {
		kinds[tpl.Kind]++
		if tpl.Layout.Alt == nil {
			t.Errorf("%s: no second version", tpl.Key)
			continue
		}
		for _, portrait := range []bool{false, true} {
			v := tpl.Layout.For(portrait)
			if (v.Orientation == "portrait") != portrait {
				t.Errorf("%s: For(%v) gave %s", tpl.Key, portrait, v.Orientation)
			}
			l := boards.NormalizeLayout(v)
			if l.Rows < l.Extent() || (v.Rows != 0 && v.Rows < v.Extent()) {
				t.Errorf("%s/%s: rows %d < extent %d", tpl.Key, v.Orientation, v.Rows, v.Extent())
			}
			if len(v.Widgets) == 0 {
				t.Errorf("%s/%s: empty", tpl.Key, v.Orientation)
			}
			versions++
		}
		if _, err := boards.ValidateLayout(mustJSON(t, tpl.Layout)); err != nil {
			t.Errorf("%s: %v", tpl.Key, err)
		}
	}
	for _, k := range []string{"audience", "walkin", "presenter"} {
		if kinds[k] != 4 {
			t.Errorf("%s: %d layouts, want 4", k, kinds[k])
		}
	}
	if versions != 24 {
		t.Errorf("%d built-in versions, want 24", versions)
	}
	// Rows default by orientation and never undercut the tiles.
	l := boards.NormalizeLayout(boards.Layout{Orientation: "portrait", Widgets: []boards.Widget{{ID: "a", Type: "notice", X: 0, Y: 30, W: 12, H: 2}}})
	if l.Rows != 32 {
		t.Errorf("rows should grow to the extent: %d", l.Rows)
	}
	if boards.NormalizeLayout(boards.Layout{}).Rows != boards.DefaultRowsLandscape {
		t.Error("landscape default rows")
	}
}

// A layout made before versions existed gets an automatic other version;
// a stored one wins; normalisation keeps the version opposite and drops
// an empty one.
func TestLayoutVersions(t *testing.T) {
	land := boards.Layout{V: 1, Orientation: "landscape", Rows: 8, Widgets: []boards.Widget{
		{ID: "a", Type: "countdown", X: 0, Y: 0, W: 8, H: 4}, {ID: "b", Type: "wallclock", X: 8, Y: 0, W: 4, H: 2}}}
	p := land.For(true)
	if p.Orientation != "portrait" || len(p.Widgets) != 2 || p.Widgets[0].W != 12 || len(boards.Overlaps(p)) > 0 {
		t.Fatalf("auto portrait: %+v", p)
	}
	back := p.For(false)
	if back.Orientation != "landscape" || len(back.Widgets) != 2 || len(boards.Overlaps(back)) > 0 {
		t.Fatalf("auto landscape: %+v", back)
	}
	own := boards.Layout{V: 1, Orientation: "landscape", Widgets: []boards.Widget{{ID: "x", Type: "notice", W: 12, H: 2}}}
	land.Alt = &own // wrong orientation on purpose: normalisation flips it
	n := boards.NormalizeLayout(land)
	if n.Alt == nil || n.Alt.Orientation != "portrait" || n.For(true).Widgets[0].ID != "x" {
		t.Fatalf("stored version: %+v", n.Alt)
	}
	land.Alt = &boards.Layout{}
	if boards.NormalizeLayout(land).Alt != nil {
		t.Error("an empty version should be dropped")
	}
}
