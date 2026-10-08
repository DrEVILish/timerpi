package routes_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// --- apiShowCmd edges (B5 covered go/jump-guard/rate-clamp/unknown) ---------

// cmd/rate with a missing body defaults to ×1.0; a below-floor value
// clamps to ×0.5 (the ceiling twin is covered by TestRemoteCmdB5).
func TestCmdRateDefaultAndFloor(t *testing.T) {
	ts := newAPITest(t)

	code, m := cmdBody(t, ts, "/cmd/rate", "")
	if code != http.StatusOK {
		t.Fatalf("rate missing body: %d %v", code, m)
	}
	if rt := m["snapshot"].(map[string]any)["runtime"].(map[string]any); rt["rate"] != 1.0 {
		t.Fatalf("rate default = %v, want 1.0", rt["rate"])
	}

	code, m = cmdBody(t, ts, "/cmd/rate", `{"rate":0.1}`)
	if code != http.StatusOK {
		t.Fatalf("rate 0.1: %d %v", code, m)
	}
	if rt := m["snapshot"].(map[string]any)["runtime"].(map[string]any); rt["rate"] != 0.5 {
		t.Fatalf("rate floor = %v, want 0.5", rt["rate"])
	}
}

// --- moveto guards -----------------------------------------------------------

// pos/to at or below zero (and malformed JSON) are 400s before the DB is
// touched; TestRemoteMoveToB2 only sends 1-based values.
func TestMoveToGuards(t *testing.T) {
	ts := newAPITest(t)
	for _, body := range []string{`{"pos":0,"to":2}`, `{"pos":1,"to":0}`, `{"pos":-1,"to":-2}`, `not json`} {
		code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/moveto", []byte(body), "application/json")
		if code != http.StatusBadRequest {
			t.Errorf("moveto %q: %d, want 400", body, code)
		}
	}
}

// --- cue replace validation --------------------------------------------------

// PUT /cues with a non-array body or an invalid cue is a 400; the replace
// test only sends the valid wire shape.
func TestReplaceCuesValidation(t *testing.T) {
	ts := newAPITest(t)
	path := "/api/shows/" + ts.showCode + "/cues"

	if code, _ := ts.call("PUT", path, []byte(`{"label":"nope"}`), "application/json"); code != http.StatusBadRequest {
		t.Errorf("non-array replace: %d, want 400", code)
	}
	bad := `[{"label":"x","durationMS":60000,"kind":"bogus"}]`
	if code, body := ts.call("PUT", path, []byte(bad), "application/json"); code != http.StatusBadRequest {
		t.Errorf("bad-kind replace: %d %s, want 400", code, body)
	} else if !strings.Contains(string(body), "kind") {
		t.Errorf("bad-kind replace error names no field: %s", body)
	}
	// The refused writes left the show empty.
	if cues := ts.snapshot()["cues"].([]any); len(cues) != 0 {
		t.Errorf("refused replace wrote rows: %v", cues)
	}
}

// --- REST messages surface (only WS addMsg was tested) -----------------------

// POST /messages validates like its WS twin and honors show:true;
// empty text and bad colors are 400s.
func TestRestMessagesSurface(t *testing.T) {
	ts := newAPITest(t)
	path := "/api/shows/" + ts.showCode + "/messages"

	if code, _ := ts.call("POST", path, []byte(`{"text":"  "}`), "application/json"); code != http.StatusBadRequest {
		t.Errorf("empty text: %d, want 400", code)
	}
	if code, body := ts.call("POST", path, []byte(`{"text":"hi","color":"red"}`), "application/json"); code != http.StatusBadRequest || !strings.Contains(string(body), "invalid color") {
		t.Errorf("bad color: %d %s, want 400 invalid color", code, body)
	}

	code, _ := ts.call("POST", path, []byte(`{"text":"WRAP UP","color":"#ff4444","show":true}`), "application/json")
	if code != http.StatusOK {
		t.Fatalf("create shown message: %d", code)
	}
	msgs := ts.snapshot()["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["text"] != "WRAP UP" {
		t.Fatalf("shown message missing from snapshot: %v", msgs)
	}

	// Queued (unshown) messages ride the DB but not the snapshot wire.
	code, _ = ts.call("POST", path, []byte(`{"text":"later"}`), "application/json")
	if code != http.StatusOK {
		t.Fatalf("create queued message: %d", code)
	}
	if msgs := ts.snapshot()["messages"].([]any); len(msgs) != 1 {
		t.Fatalf("queued message leaked onto the wire: %v", msgs)
	}
}

// The custom-message form ships "Show now" (checked): Add puts the text
// on every display immediately instead of silently queueing it.
func TestMessageFormShowsNow(t *testing.T) {
	ts := newAPITest(t)
	_, body := ts.call("GET", "/c/"+ts.showCode, nil, "")
	if !strings.Contains(string(body), `name="show"`) || !strings.Contains(string(body), `value="true"`) {
		t.Error("message form missing the show-now checkbox")
	}
}

// --- board id + layout guards ------------------------------------------------

// Malformed :bid values are 400 "bad board id" (CRUD only uses numerics).
func TestBoardBidGuards(t *testing.T) {
	ts := newAPITest(t)
	for _, bid := range []string{"nope", "0", "-3", "1.5"} {
		code, _ := ts.call("PUT", "/api/shows/"+ts.showCode+"/boards/"+bid, []byte(`{}`), "application/json")
		if code != http.StatusBadRequest {
			t.Errorf("bid %q: %d, want 400", bid, code)
		}
	}
}

// Layout edges the CRUD test never sends: >48 widgets and zero widgets
// are both 400s through the whole PUT path.
func TestBoardsValidateEdges(t *testing.T) {
	ts := newAPITest(t)
	list := ts.boardsList(t)
	bid := itoa(int64(list[0]["id"].(float64)))

	var sb strings.Builder
	sb.WriteString(`{"layout":{"v":1,"widgets":[`)
	for i := 0; i < 49; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":"w%d","type":"rate","x":%d,"y":%d,"w":1,"h":1}`, i, i%12, i/12)
	}
	sb.WriteString(`]}}`)
	if code, body := ts.call("PUT", "/api/shows/"+ts.showCode+"/boards/"+bid, []byte(sb.String()), "application/json"); code != http.StatusBadRequest || !strings.Contains(string(body), "max") {
		t.Errorf("49 widgets: %d %s, want 400 max", code, body)
	}

	empty := `{"layout":{"v":1,"widgets":[]}}`
	if code, _ := ts.call("PUT", "/api/shows/"+ts.showCode+"/boards/"+bid, []byte(empty), "application/json"); code != http.StatusBadRequest {
		t.Errorf("zero widgets: %d, want 400", code)
	}
}

// --- import edges ------------------------------------------------------------

// Explicit kind= wins over the filename: CSV bytes named *.bin still
// parse as CSV (the suite only ever sent kind="" auto).
func TestImportExplicitKind(t *testing.T) {
	ts := newAPITest(t)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("kind", "csv")
	fw, _ := w.CreateFormFile("file", "cue-list.bin")
	_, _ = fw.Write([]byte("label,duration\nOpener,1:00\n"))
	w.Close()

	code, _ := ts.callType("POST", "/api/shows/"+ts.showCode+"/import", buf.Bytes(), "multipart/form-data; boundary="+w.Boundary())
	if code != http.StatusOK {
		t.Fatalf("explicit kind import: %d", code)
	}
	if cues := ts.snapshot()["cues"].([]any); len(cues) != 1 {
		t.Fatalf("explicit kind cues = %v", cues)
	}
}

// Append-mode salvage: good rows apply AND the bad row is reported
// (replace refuses everything — covered; append was never exercised).
func TestImportAppendSalvage(t *testing.T) {
	ts := newAPITest(t)
	bad := "label,duration\nGood,1:00\nBad1,banana\nBad2,5:00\n"
	imp := ts.multipart(bad, "cue-list.csv")
	code, body := ts.callType("POST", "/api/shows/"+ts.showCode+"/import?mode=append", imp.body, imp.ctype)
	if code != http.StatusOK || !bytes.Contains(body, []byte("Row 3")) || !bytes.Contains(body, []byte("banana")) {
		t.Fatalf("append salvage: %d %s (want row-3/banana report)", code, body)
	}
	var labels []string
	for _, c := range ts.snapshot()["cues"].([]any) {
		labels = append(labels, c.(map[string]any)["label"].(string))
	}
	if len(labels) != 2 || labels[0] != "Good" || labels[1] != "Bad2" {
		t.Fatalf("salvaged cues = %v, want [Good Bad2]", labels)
	}
}

// --- setup wizard + show-file guards (no setup_test.go exists) ---------------

// A show-file bundle with a future manifestVersion is refused before any
// show is created (importShowFile has no route-level test at all).
func TestShowFileVersionMismatch(t *testing.T) {
	ts := newAPITest(t)
	shows0, err := ts.db.ListShows()
	if err != nil {
		t.Fatalf("ListShows: %v", err)
	}
	body := `{"manifestVersion":99,"show":{"title":"Future"},"cues":[]}`
	code, resp := ts.call("POST", "/api/events/"+ts.eventCode+"/rooms/import", []byte(body), "application/json")
	if code != http.StatusBadRequest || !strings.Contains(string(resp), "unsupported show file version") {
		t.Fatalf("future bundle: %d %s", code, resp)
	}
	shows1, err := ts.db.ListShows()
	if err != nil {
		t.Fatalf("ListShows: %v", err)
	}
	if len(shows1) != len(shows0) {
		t.Fatalf("refused bundle created a show (%d → %d)", len(shows0), len(shows1))
	}
}
