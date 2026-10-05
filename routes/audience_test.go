// audience tests (phase 0 stitch): /a/:code page renders, the audience
// REST lane honors moderation-by-silence, votes replace per peer, and the
// show-passphrase gate applies to the REST lane as the page does

package routes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestAudiencePage(t *testing.T) {
	ts := newAPITest(t)
	code, body := ts.call("GET", "/a/"+ts.showCode, nil, "")
	if code != 200 {
		t.Fatalf("audience page: %d %.200s", code, body)
	}
	s := string(body)
	for _, sub := range []string{`tp-aud-card`, `tp-aud-main`,
		`data-show-code="` + ts.showCode + `"`, `WAITING FOR THE ROOM`} {
		if !strings.Contains(s, sub) {
			t.Errorf("audience page missing %q", sub)
		}
	}
}

func TestAudienceGateAndFlow(t *testing.T) {
	ts := newAPITest(t)
	codeBase := "/api/shows/" + ts.showCode + "/polls"

	// Unknown code 404s on read.
	if code, _ := ts.call("GET", "/api/audience/ZZZZ-9999", nil, ""); code != 404 {
		t.Fatalf("unknown code: %d", code)
	}

	// Operator creates a poll (hidden), audience reads nothing.
	pid := mustPollCreate(t, ts, `{"kind":"poll","question":"Lunch?","options":["Pizza","Skyr"]}`)
	if body := audienceRead(t, ts); pollID(body) != 0 {
		t.Fatalf("audience saw a hidden poll: %s", audienceRead(t, ts))
	}

	// Open voting; audience votes + revotes (dedupe per peer).
	if code, b := ts.call("POST", fmt.Sprintf("%s/%d/state", codeBase, pid),
		[]byte(`{"state":"open"}`), ""); code != 200 {
		t.Fatalf("state open: %d %s", code, b)
	}
	d := audienceRead(t, ts)
	if pollID(d) != pid || state(d) != "open" {
		t.Fatalf("open poll not visible to audience: %s", d)
	}
	vote := func(choice, peer string) (int, string) {
		code, b := ts.call("POST", "/api/audience/"+ts.showCode+"/vote",
			[]byte(fmt.Sprintf(`{"pollId":%d,"choice":%q,"peer":%q}`, pid, choice, peer)), "")
		return code, string(b)
	}
	if code, _ := vote("5", "ph1"); code != 400 {
		t.Errorf("out-of-range vote: %d", code)
	}
	if code, _ := vote("0", "ph1"); code != 200 {
		t.Fatalf("vote: %d", code)
	}
	// The change-of-mind revote inside the 300 ms window is rate-limited…
	if code, _ := vote("1", "ph1"); code != http.StatusTooManyRequests {
		t.Fatalf("revote inside throttle: %d", code)
	}
	// …the same peer may vote again after the window (DB UNIQUE makes the
	// eventual revote idempotent — covered by TestClaimWaiting-style unit
	// tests in timerpi).
	if code, _ := vote("1", "ph2"); code != 200 {
		t.Fatalf("vote2: %d", code)
	}
	d = audienceRead(t, ts)
	if counts := gjsonArr(d, "poll", "counts"); len(counts) != 2 || counts[0] != 1 || counts[1] != 1 {
		t.Fatalf("counts: %s", d)
	}
	if gjsonNum(d, "poll", "total") != 2 {
		t.Fatalf("total: %s", d)
	}

	// Questions are moderated by silence: submitted → invisible until open.
	if code, b := ts.call("POST", "/api/audience/"+ts.showCode+"/ask",
		[]byte(`{"kind":"qa","text":"Louder please","peer":"ph3"}`), ""); code != 200 {
		t.Fatalf("ask: %d %s", code, b)
	}
	if body := audienceRead(t, ts); strings.Contains(body, "Louder please") {
		t.Fatalf("concealed question leaked to audience: %s", body)
	}

	// A room's moderator password never gates the audience: phones read,
	// vote and ask with no session at all.
	if err := ts.db.SetRoomPassword(ts.showID, "op-pass"); err != nil {
		t.Fatalf("set room password: %v", err)
	}
	if code, body := ts.anon("GET", "/api/audience/"+ts.showCode, nil, ""); code != 200 ||
		!strings.Contains(string(body), "Lunch?") || !strings.Contains(string(body), "counts") {
		t.Fatalf("anonymous audience read: %d %.200s", code, body)
	}
	if code, _ := ts.anon("GET", "/a/"+ts.showCode, nil, ""); code != 200 {
		t.Fatalf("anonymous audience page: %d", code)
	}
}

// --- JSON helpers (hand-rolled: no dependency churn for one test file) ---

func mustPollCreate(t *testing.T, ts *apiTest, body string) int64 {
	t.Helper()
	code, raw := ts.call("POST", "/api/shows/"+ts.showCode+"/polls", []byte(body), "")
	if code != 200 {
		t.Fatalf("poll create: %d %s", code, raw)
	}
	var out struct {
		OK bool  `json:"ok"`
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || !out.OK {
		t.Fatalf("poll create body: %s", raw)
	}
	return out.ID
}

func audienceRead(t *testing.T, ts *apiTest) string {
	t.Helper()
	code, raw := ts.call("GET", "/api/audience/"+ts.showCode, nil, "")
	if code != 200 {
		t.Fatalf("audience read: %d %s", code, raw)
	}
	return string(raw)
}

func state(s string) string { return gjsonStr(s, "poll", "state") }
func pollID(s string) int64 { return gjsonNum(s, "poll", "id") }

// gjsonSection digs out the nested {data:{<section>:…}} the audience API
// returns (everything lives under data).
func gjsonSection(src, section string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(src), &m); err != nil {
		return nil
	}
	data, _ := m["data"].(map[string]any)
	s, _ := data[section].(map[string]any)
	return s
}

func gjsonNum(src, section, key string) int64 {
	s := gjsonSection(src, section)
	v, _ := s[key].(float64)
	return int64(v)
}

func gjsonStr(src, section, key string) string {
	s := gjsonSection(src, section)
	v, _ := s[key].(string)
	return v
}

func gjsonArr(src, section, key string) []float64 {
	s := gjsonSection(src, section)
	raw, _ := s[key].([]any)
	out := make([]float64, 0, len(raw))
	for _, v := range raw {
		n, _ := v.(float64)
		out = append(out, n)
	}
	return out
}
