// Clear (reset) keeps an item but drops its responses; Export lists every
// item's responses as CSV (2026-10-07 audience notes).
package routes_test

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestAudienceClearKeepsItem(t *testing.T) {
	ts := newAPITest(t)
	qa := mustPollCreate(t, ts, `{"kind":"qa","question":"Ask the panel","autoApprove":true}`)
	poll := mustPollCreate(t, ts, `{"kind":"poll","question":"Lunch?","options":["Pizza","Soup"]}`)
	modPost(t, ts, fmt.Sprintf("/%d/show", qa), `{"target":"audience","on":true}`)
	if code, b := ts.anon("POST", "/api/audience/"+ts.showCode+"/ask", []byte(fmt.Sprintf(`{"item":%d,"text":"When is lunch?"}`, qa)), "application/json"); code != 200 {
		t.Fatalf("ask: %d %s", code, b)
	}
	q := modList(t, ts)
	var qid int64
	for _, it := range q {
		if it.ID == qa {
			qid = it.Children[0].ID
		}
	}
	modPost(t, ts, fmt.Sprintf("/%d/spotlight", qa), fmt.Sprintf(`{"entry":%d}`, qid))
	modPost(t, ts, fmt.Sprintf("/%d/show", poll), `{"target":"audience","on":true}`)
	if code, b := ts.anon("POST", "/api/audience/"+ts.showCode+"/vote", []byte(fmt.Sprintf(`{"pollId":%d,"choice":"1"}`, poll)), "application/json"); code != 200 {
		t.Fatalf("vote: %d %s", code, b)
	}
	modPost(t, ts, fmt.Sprintf("/%d/results", poll), `{"on":true}`)

	modPost(t, ts, fmt.Sprintf("/%d/reset", qa), `{}`)
	modPost(t, ts, fmt.Sprintf("/%d/reset", poll), `{}`)
	for _, it := range modList(t, ts) {
		if len(it.Children) != 0 {
			t.Errorf("%q kept its entries after clear: %+v", it.Question, it.Children)
		}
		if it.ID == poll && !it.ToAudience {
			t.Errorf("%q came off the screen after clear", it.Question)
		}
		if it.ID == poll && it.State != "open" {
			t.Errorf("cleared poll state = %q, want open (voting again)", it.State)
		}
	}
	_, b := ts.call("GET", "/api/shows/"+ts.showCode+"/polls", nil, "")
	if strings.Contains(string(b), `"spotlight"`) || strings.Contains(string(b), `"total":1`) {
		t.Errorf("clear left a spotlight or votes: %s", b)
	}
	// An entry can't be cleared on its own.
	if code, _ := ts.call("POST", fmt.Sprintf("/api/shows/%s/polls/%d/reset", ts.showCode, qid), []byte(`{}`), "application/json"); code == 200 {
		t.Error("clearing a single entry was accepted")
	}
}

func TestAudienceExportCSV(t *testing.T) {
	ts := newAPITest(t)
	poll := mustPollCreate(t, ts, `{"kind":"quiz","question":"Capital of France?","options":["Paris","Lyon"],"correct":0}`)
	cloud := mustPollCreate(t, ts, `{"kind":"wordcloud","question":"One word","autoApprove":true}`)
	modPost(t, ts, fmt.Sprintf("/%d/show", poll), `{"target":"audience","on":true}`)
	ts.anon("POST", "/api/audience/"+ts.showCode+"/vote", []byte(fmt.Sprintf(`{"pollId":%d,"choice":"0"}`, poll)), "application/json")
	modPost(t, ts, fmt.Sprintf("/%d/show", cloud), `{"target":"audience","on":true}`)
	for _, w := range []string{"fun", "=HYPERLINK(1)"} {
		ts.anon("POST", "/api/audience/"+ts.showCode+"/ask", []byte(fmt.Sprintf(`{"item":%d,"text":%q}`, cloud, w)), "application/json")
	}

	code, b := ts.call("GET", "/api/shows/"+ts.showCode+"/polls/export", nil, "")
	if code != 200 {
		t.Fatalf("export: %d %s", code, b)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(b), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatalf("export is not CSV: %v\n%s", err, b)
	}
	got := map[string][]string{}
	for _, r := range rows[1:] {
		got[r[3]] = r
	}
	if r := got["Paris"]; r == nil || r[4] != "1" || r[5] != "correct" || r[2] != "Capital of France?" {
		t.Errorf("quiz option row = %v", r)
	}
	if r := got["Lyon"]; r == nil || r[4] != "0" {
		t.Errorf("unvoted option row = %v", r)
	}
	if r := got["fun"]; r == nil || r[1] != "wordcloud" || r[5] != "approved" {
		t.Errorf("word row = %v", r)
	}
	if got["'=HYPERLINK(1)"] == nil {
		t.Errorf("a formula-like submission is not neutralised: %v", got)
	}
	// Moderators only.
	if code, _ := ts.anon("GET", "/api/shows/"+ts.showCode+"/polls/export", nil, ""); code == 200 {
		t.Error("export open without a session")
	}
}

// Phones keep a sent question under "Waiting for review" only while its id
// is in the public frame's waiting list: dismissed or cleared ones drop off.
func TestAudienceWaitingIDs(t *testing.T) {
	ts := newAPITest(t)
	qa := mustPollCreate(t, ts, `{"kind":"qa","question":"Ask"}`)
	modPost(t, ts, fmt.Sprintf("/%d/show", qa), `{"target":"audience","on":true}`)
	_, b := ts.anon("POST", "/api/audience/"+ts.showCode+"/ask", []byte(fmt.Sprintf(`{"item":%d,"text":"Hidden one?"}`, qa)), "application/json")
	var sent struct{ ID int64 }
	_ = json.Unmarshal(b, &sent)
	waiting := func() string {
		_, b := ts.anon("GET", "/api/audience/"+ts.showCode, nil, "")
		var out struct {
			Data struct {
				Poll struct {
					Waiting []int64 `json:"waiting"`
				} `json:"poll"`
			} `json:"data"`
		}
		_ = json.Unmarshal(b, &out)
		if strings.Contains(string(b), "Hidden one?") {
			t.Errorf("pending text leaked to phones: %s", b)
		}
		return fmt.Sprint(out.Data.Poll.Waiting)
	}
	if got, want := waiting(), fmt.Sprintf("[%d]", sent.ID); got != want {
		t.Fatalf("waiting = %s, want %s", got, want)
	}
	modPost(t, ts, fmt.Sprintf("/%d/moderate", sent.ID), `{"status":"dismissed"}`)
	if got := waiting(); got != "[]" {
		t.Errorf("dismissed entry still waiting: %s", got)
	}
}
