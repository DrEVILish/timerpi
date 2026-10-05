// Phase 5 tests (PLAN §11.3): the operator polls-list view (counts +
// moderation children), the full moderation flow (submit → approve → on
// air → hide), item create/state/delete over the API, and the BroadcastPoll
// seam the panel rides.
package routes_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestModerationFlow(t *testing.T) {
	ts := newAPITest(t)

	// Operator opens a word cloud.
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/polls",
		[]byte(`{"kind":"wordcloud","question":"One word for the venue"}`), ""); code != 200 {
		t.Fatalf("create cloud: %d %s", code, b)
	}
	// Find its id via the operator list.
	code, b := ts.call("GET", "/api/shows/"+ts.showCode+"/polls", nil, "")
	if code != 200 {
		t.Fatalf("polls list: %d %s", code, b)
	}
	var list struct {
		Polls []struct {
			ID       int64  `json:"id"`
			Kind     string `json:"kind"`
			State    string `json:"state"`
			Parent   int64  `json:"parent"`
			Question string `json:"question"`
		} `json:"polls"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("list body: %s", b)
	}
	if len(list.Polls) != 1 || list.Polls[0].State != "hidden" {
		t.Fatalf("operator list: %s", b)
	}
	cloudID := list.Polls[0].ID

	// Two different audience members submit words (hidden children) — the
	// per-peer ask throttle means distinct peers for a fast pair.
	peers := []string{"mod-1", "mod-2"}
	for i, w := range []string{"electric", "cozy"} {
		if code, b := ts.call("POST", "/api/audience/"+ts.showCode+"/ask",
			[]byte(fmt.Sprintf(`{"kind":"wordcloud","text":%q,"peer":%q,"parent":%d}`, w, peers[i], cloudID)), ""); code != 200 {
			t.Fatalf("submit %q: %d %s", w, code, b)
		}
	}
	// The operator list shows them as hidden children (the moderation queue).
	code, b = ts.call("GET", "/api/shows/"+ts.showCode+"/polls", nil, "")
	if code != 200 || !strings.Contains(string(b), "electric") || !strings.Contains(string(b), "cozy") {
		t.Fatalf("submissions not in the operator list: %d %s", code, b)
	}

	// Operator flow: SHOW the cloud, then approve words into it (child
	// approval accumulates without closing the parent — the phase-4 fix).
	if code, bb := ts.call("POST", fmt.Sprintf("/api/shows/%s/polls/%d/state", ts.showCode, cloudID),
		[]byte(`{"state":"open"}`), ""); code != 200 {
		t.Fatalf("open cloud: %d %s", code, bb)
	}
	byWord := map[string]int64{}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("list2: %s", b)
	}
	for _, p := range list.Polls {
		if p.Parent == cloudID {
			byWord[p.Question] = p.ID
		}
	}
	if len(byWord) != 2 {
		t.Fatalf("want 2 submissions, got %d: %s", len(byWord), b)
	}
	// Approve both; upvote "cozy" so it leads the wall (loudest first).
	for _, w := range []string{"electric", "cozy"} {
		if code, bb := ts.call("POST", fmt.Sprintf("/api/shows/%s/polls/%d/state", ts.showCode, byWord[w]),
			[]byte(`{"state":"open"}`), ""); code != 200 {
			t.Fatalf("approve %q: %d %s", w, code, bb)
		}
	}
	if code, bb := ts.call("POST", "/api/audience/"+ts.showCode+"/vote",
		[]byte(fmt.Sprintf(`{"pollId":%d,"choice":"1","peer":"voter"}`, byWord["cozy"])), ""); code != 200 {
		t.Fatalf("upvote cozy: %d %s", code, bb)
	}

	// On air: the cloud's children carry the approved words with upvotes.
	code, b = ts.call("GET", "/api/audience/"+ts.showCode, nil, "")
	if code != 200 || !strings.Contains(string(b), "cozy") || !strings.Contains(string(b), `"upvotes":1`) {
		t.Fatalf("audience read after approval: %d %s", code, b)
	}

	// Hide "electric" again → it leaves the audience surface.
	if code, _ := ts.call("POST", fmt.Sprintf("/api/shows/%s/polls/%d/state", ts.showCode, byWord["electric"]),
		[]byte(`{"state":"hidden"}`), ""); code != 200 {
		t.Fatalf("hide: %d", code)
	}
	code, b = ts.call("GET", "/api/audience/"+ts.showCode, nil, "")
	if strings.Contains(string(b), "electric") {
		t.Fatalf("hidden word still on air: %s", b)
	}

	// Delete the cloud; the whole item set goes with it (FK cascade).
	if code, _ := ts.call("DELETE", fmt.Sprintf("/api/shows/%s/polls/%d", ts.showCode, cloudID), nil, ""); code != 200 {
		t.Fatalf("delete cloud: %d", code)
	}
	code, b = ts.call("GET", "/api/shows/"+ts.showCode+"/polls", nil, "")
	if strings.Contains(string(b), `"question":"One word for the venue"`) {
		t.Fatalf("deleted cloud still listed: %s", b)
	}
}

func TestPollCreateValidation(t *testing.T) {
	ts := newAPITest(t)
	// Poll without options refused; quiz with options + correct accepted.
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/polls",
		[]byte(`{"kind":"poll","question":"No options"}`), ""); code != 400 {
		t.Errorf("optionless poll accepted: %d", code)
	}
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/polls",
		[]byte(`{"kind":"quiz","question":"2+2?","options":["3","4"],"correct":1}`), ""); code != 200 {
		t.Fatalf("quiz create: %d %s", code, b)
	}
	code, b := ts.call("GET", "/api/shows/"+ts.showCode+"/polls", nil, "")
	if !strings.Contains(string(b), `"correct":1`) {
		t.Errorf("correct index not returned to the operator: %s", b)
	}
	_ = code
}
