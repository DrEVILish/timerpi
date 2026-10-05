// apicmd_test.go — B5 remote-control aliases: engine-identical semantics
// through REST, show-gated, rate clamp, jump guard.
package routes_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"timerpi/timerpi"
)

func cmdBody(t *testing.T, ts *apiTest, action string, body string) (int, map[string]any) {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("{}")
	}
	path := ts.srv.URL + "/api/shows/" + ts.showCode + action
	req, _ := http.NewRequest("POST", path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Logf("%s → %d body: %s", path, res.StatusCode, raw[:minInt(300, len(raw))])
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, m
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestRemoteCmdB5(t *testing.T) {
	ts := newAPITest(t)
	// seed two cues so transport actions have real rows to walk
	for _, cue := range []timerpi.Cue{
		{Label: "A", DurationMS: 300_000},
		{Label: "B", DurationMS: 300_000},
	} {
		if _, err := ts.db.CreateCue(ts.showID, cue); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// go → running
	code, m := cmdBody(t, ts, "/cmd/go", "")
	if code != http.StatusOK || m["ok"] != true {
		t.Fatalf("cmd go: %d %v", code, m)
	}
	if rt := m["snapshot"].(map[string]any)["runtime"].(map[string]any); rt["running"] != true {
		t.Fatalf("snapshot.runtime after go: %v", rt)
	}
	// jump without pos → 400
	code, m = cmdBody(t, ts, "/cmd/jump", "")
	if code != http.StatusBadRequest {
		t.Fatalf("jump guard: %d %v", code, m)
	}
	// rate clamped to the ×0.5–×2.0 contract
	code, m = cmdBody(t, ts, "/cmd/rate", `{"rate":9}`)
	if code != http.StatusOK {
		t.Fatalf("rate: %d %v", code, m)
	}
	if rt := m["snapshot"].(map[string]any)["runtime"].(map[string]any); rt["rate"] != 2.0 {
		t.Fatalf("rate not clamped: %v", rt["rate"])
	}
	// next advances to the following cue and answers 200 with the snapshot
	code, m = cmdBody(t, ts, "/cmd/next", "")
	if code != http.StatusOK {
		t.Fatalf("next: %d %v", code, m)
	}
	if rt := m["snapshot"].(map[string]any)["runtime"].(map[string]any); rt["activePos"] != float64(2) {
		t.Fatalf("next did not advance: %v", rt["activePos"])
	}
	// unknown action → 400 with the honest message
	code, m = cmdBody(t, ts, "/cmd/warp", "")
	if code != http.StatusBadRequest {
		t.Fatalf("unknown: %d %v", code, m)
	}
	if s, _ := m["error"].(string); !strings.Contains(s, "unknown action") {
		t.Fatalf("unknown message: %v", m)
	}
}

// B4: the REST day-start endpoint persists + anchors in one call, refuses
// garbage times, and empty clears the setting.
func TestRemoteDayStartB4(t *testing.T) {
	ts := newAPITest(t)
	code, m := cmdBody(t, ts, "/daystart", `{"hhmm":"14:05"}`)
	if code != http.StatusOK || m["ok"] != true || m["anchored"] != true {
		t.Fatalf("daystart: %d %v", code, m)
	}
	// persisted with the honest shape
	got, err := ts.db.ShowDayStart(ts.showID)
	if err != nil || got != "14:05" {
		t.Fatalf("stored day_start err=%v %q", err, got)
	}
	// garbage time refused (and not stored)
	code, _ = cmdBody(t, ts, "/daystart", `{"hhmm":"25:99"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("garbage accepted")
	}
	if got2, _ := ts.db.ShowDayStart(ts.showID); got2 != "14:05" {
		t.Fatalf("garbage overwrote? %q", got2)
	}
	// empty clears the automation
	code, _ = cmdBody(t, ts, "/daystart", `{"hhmm":""}`)
	if code != http.StatusOK {
		t.Fatalf("clear: %d", code)
	}
	if got3, _ := ts.db.ShowDayStart(ts.showID); got3 != "" {
		t.Fatalf("clear failed: %q", got3)
	}
}

// B2 REST twin: /moveto persists a full-slot reposition, show-gated;
// empty shows answer honestly, same-slot is a friendly no-op.
func TestRemoteMoveToB2(t *testing.T) {
	ts := newAPITest(t)
	// Empty show: from-slot doesn't exist → 400, exact message.
	code, m := cmdBody(t, ts, "/moveto", `{"pos":1,"to":2}`)
	if code != http.StatusBadRequest || m["ok"] != false {
		t.Fatalf("moveto on empty show: %d %v", code, m)
	}
	// Seed → real move lands; same-slot no-op answers ok.
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "A", DurationMS: 60_000}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "B", DurationMS: 60_000}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	same := map[string]any{"pos": float64(1), "to": float64(1)}
	code, m = cmdBody(t, ts, "/moveto", `{"pos":1,"to":1}`)
	if code != http.StatusOK || m["ok"] != true {
		t.Fatalf("same-slot: %d %v", code, m)
	}
	cues, err := ts.db.ListCues(ts.showID)
	if err != nil || len(cues) != 2 || cues[0].Label != "A" {
		t.Fatalf("same-slot moved: %v (err %v)", labelList(cues), err)
	}
	_ = same
	code, m = cmdBody(t, ts, "/moveto", `{"pos":2,"to":1}`)
	if code != http.StatusOK {
		t.Fatalf("real move: %d %v", code, m)
	}
	cues, _ = ts.db.ListCues(ts.showID)
	if len(cues) != 2 || cues[0].Label != "B" || cues[1].Label != "A" {
		t.Fatalf("full-slot move did not land: %v", labelList(cues))
	}
}

func labelList(cues []timerpi.Cue) []string {
	out := []string{}
	for _, c := range cues {
		out = append(out, c.Label)
	}
	return out
}
