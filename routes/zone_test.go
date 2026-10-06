package routes_test

import (
	"net/http"
	"testing"
)

// BUGLOG RW7 / STATUS C10: the zone walk-in is retired. It was public and
// listed room codes across every event on the box (/zone/%21 matched all
// rooms without a zone). Old links go home; the zone APIs are gone.
func TestZoneSurfaceRetired(t *testing.T) {
	ts := newAPITest(t)
	p := newPersona(ts)
	for _, path := range []string{"/zone/%21", "/zone/Hall%20A"} {
		if code, _ := p.do("GET", path, ""); code != http.StatusFound {
			t.Errorf("GET %s: %d, want 302 home", path, code)
		}
	}
	if code, _ := ts.call("POST", "/api/zone-map", []byte(`{"zone":"Hall A","assetId":1}`), "application/json"); code != http.StatusNotFound {
		t.Errorf("POST /api/zone-map: %d, want 404", code)
	}
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/zone", []byte(`{"zone":"Hall A"}`), "application/json"); code != http.StatusNotFound {
		t.Errorf("POST zone label: %d, want 404", code)
	}
}
