package routes_test

import (
	"net/http"
	"testing"
)

// BUGLOG RS31: on a fresh box (no layout request yet), giving a screen a
// template works; the layouts table used to be created only lazily by
// the board API, so this failed with "no such table: display_boards".
func TestTemplateOnFreshBox(t *testing.T) {
	ts := newAPITest(t)
	code, body := ts.call("POST", "/api/shows/"+ts.showCode+"/screens/template", []byte(`{"name":"Door","template":"room"}`), "application/json")
	if code != http.StatusOK {
		t.Fatalf("template on a fresh box: %d %s", code, body)
	}
}
