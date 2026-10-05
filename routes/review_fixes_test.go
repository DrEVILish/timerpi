package routes_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"timerpi/timerpi"
)

// Review fix: the TV surfaces that must work WITHOUT an operator login —
// the join-card QR <img> and the F4 client-log POST — pass AuthGate, while
// operator-shaped API calls still refuse.
func TestTVSubresourcesPassAuthGate(t *testing.T) {
	ts := newAPITest(t)
	if code, _ := ts.anon("GET", "/api/shows/"+ts.showCode+"/qr?data=x", nil, ""); code != http.StatusOK {
		t.Errorf("TV QR under auth gate: %d, want 200", code)
	}
	code, body := ts.anon("POST", "/api/shows/"+ts.showCode+"/client-log",
		[]byte(`{"entries":[{"kind":"error","message":"tv said hi","source":""}]}`), "application/json")
	if code != http.StatusOK {
		t.Errorf("TV client-log under auth gate: %d %s, want 200", code, body)
	}
	// Control: mutating operator endpoints still refuse.
	if code, _ := ts.anon("POST", "/api/shows/"+ts.showCode+"/blank", []byte(`{"on":true}`), "application/json"); code != http.StatusUnauthorized {
		t.Errorf("anonymous operator POST: %d, want 401", code)
	}
}

// Review fix: request bodies are capped (8 MiB) before handlers decode.
func TestBodyCeiling(t *testing.T) {
	ts := newAPITest(t)
	big := `{"title":"` + strings.Repeat("a", 9<<20) + `"}`
	if code, _ := ts.call("POST", "/api/events", []byte(big), "application/json"); code == http.StatusCreated {
		t.Fatalf("9 MiB create-event accepted; ceiling middleware dead")
	}
	// Import paths get the 32 MiB ceiling: 9 MiB JSON must be *parsed* and
	// refused for shape, not by the byte ceiling.
	imp := `{"title":"` + strings.Repeat("b", 9<<20) + `"}`
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/import", []byte(imp), "application/json"); code == http.StatusCreated {
		t.Fatalf("9 MiB import accepted")
	}
}

// Review fix: board assignments must reference a real board of the show,
// and deleting a board clears screens that pointed at it (no 404 navigations).
func TestScreenBoardValidationAndDelete(t *testing.T) {
	ts := newAPITest(t)
	base := "/api/shows/" + ts.showCode
	screensGet := func() map[string]map[string]any {
		code, body := ts.call("GET", base+"/screens", nil, "")
		if code != 200 {
			t.Fatalf("screens: %d", code)
		}
		var out struct {
			Screens []map[string]any `json:"screens"`
		}
		_ = json.Unmarshal(body, &out)
		m := map[string]map[string]any{}
		for _, s := range out.Screens {
			m[s["name"].(string)] = s
		}
		return m
	}

	if code, _ := ts.call("POST", base+"/screens/config", []byte(`{"name":"Stage","theme":"blue-future","boardId":999999}`), "application/json"); code != http.StatusBadRequest {
		t.Fatalf("phantom board: %d, want 400", code)
	}
	if code, _ := ts.call("POST", base+"/screens/config", []byte(`{"name":"Stage","boardId":-4}`), "application/json"); code != http.StatusBadRequest {
		t.Fatalf("negative board: %d, want 400", code)
	}

	b := ts.boardsList(t)
	bid := int64(b[0]["id"].(float64))
	if code, _ := ts.call("POST", base+"/screens/config", []byte(`{"name":"Stage","boardId":`+itoa(bid)+`}`), "application/json"); code != 200 {
		t.Fatalf("valid board assign: %d", code)
	}
	if got := screensGet()["Stage"]["boardId"].(float64); got != float64(bid) {
		t.Fatalf("assignment missing: %v", got)
	}
	if code, _ := ts.call("DELETE", base+"/boards/"+itoa(bid), nil, ""); code != http.StatusOK {
		t.Fatalf("delete board: %d", code)
	}
	if got := screensGet()["Stage"]["boardId"].(float64); got != 0 {
		t.Errorf("deleted board still assigned: %v", got)
	}
}

// Review fix: the client-log journal line is flattened + kind-bounded; the
// stored kind is clipped (40) so the capped tail can't be widened by junk.
func TestClientLogJournalHygiene(t *testing.T) {
	ts := newAPITest(t)
	path := "/api/shows/" + ts.showCode + "/client-log"
	body := `{"entries":[{"kind":"` + strings.Repeat("K", 500) + `","message":"line1\nFORGED journal line\nline3","source":"s"}]}`
	code, resp := ts.call("POST", path, []byte(body), "application/json")
	if code != http.StatusOK {
		t.Fatalf("post: %d %s", code, resp)
	}
	code, tail := ts.call("GET", path+"?limit=1", nil, "")
	if code != 200 {
		t.Fatalf("tail: %d", code)
	}
	var out struct {
		Errors []struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(tail, &out); err != nil || len(out.Errors) != 1 {
		t.Fatalf("tail decode: %v %s", err, tail)
	}
	if len(out.Errors[0].Kind) > 40 {
		t.Errorf("stored kind not bounded: %d chars", len(out.Errors[0].Kind))
	}
	// DB keeps message text for debugging; the journal copy (not observable
	// here) is flattened by routes.journalSafe.
}

// CloneShow copies start_at — guard against a clone silently inheriting a
// live day (clone dashboard must render unstarted).
func TestCloneCarriesStartAtButIdle(t *testing.T) {
	ts := newAPITest(t)
	cue, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Timed", DurationMS: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	cue.StartAt = "09:30"
	if _, err := ts.db.UpdateCue(ts.showID, cue); err != nil {
		t.Fatal(err)
	}
	code, body := ts.call("POST", "/api/shows/"+ts.showCode+"/clone", []byte(`{}`), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("clone: %d %s", code, body)
	}
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &out)
	snap := snapshotOf(t, ts, out.Code)
	cues := snap["cues"].([]any)
	if len(cues) != 1 {
		t.Fatalf("clone cues: %v", cues)
	}
	if cues[0].(map[string]any)["startAt"] != "09:30" {
		t.Errorf("startAt not carried: %v", cues[0])
	}
	if rt := snap["runtime"].(map[string]any); rt["running"] != false || rt["activePos"] != float64(0) {
		t.Errorf("clone started running: %v", rt)
	}
}
