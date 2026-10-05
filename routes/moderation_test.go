// Moderator API tests: create, push to targets, moderate submissions,
// spotlight, results, and the views phones and screens get.
package routes_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type modItem struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Question    string `json:"question"`
	ToAudience  bool   `json:"toAudience"`
	ToPresenter bool   `json:"toPresenter"`
	Pending     int    `json:"pending"`
	Correct     int64  `json:"correct"`
	Children    []struct {
		ID       int64  `json:"id"`
		Question string `json:"question"`
		State    string `json:"state"`
		Upvotes  int64  `json:"upvotes"`
	} `json:"children"`
}

func modList(t *testing.T, ts *apiTest) []modItem {
	t.Helper()
	code, b := ts.call("GET", "/api/shows/"+ts.showCode+"/polls", nil, "")
	if code != 200 {
		t.Fatalf("moderator list: %d %s", code, b)
	}
	var out struct {
		Items []modItem `json:"items"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("list body: %s", b)
	}
	return out.Items
}

func modPost(t *testing.T, ts *apiTest, path, body string) {
	t.Helper()
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/polls"+path, []byte(body), "application/json"); code != 200 {
		t.Fatalf("POST %s: %d %s", path, code, b)
	}
}

func TestModerationFlow(t *testing.T) {
	ts := newAPITest(t)
	qa := mustPollCreate(t, ts, `{"kind":"qa","question":"Ask the panel"}`)
	if it := modList(t, ts); len(it) != 1 || it[0].State != "hidden" || it[0].ToAudience || it[0].ToPresenter {
		t.Fatalf("new item must be off air: %+v", it)
	}
	// Show to Audience AND to Presenter.
	modPost(t, ts, fmt.Sprintf("/%d/show", qa), `{"target":"audience","on":true}`)
	modPost(t, ts, fmt.Sprintf("/%d/show", qa), `{"target":"presenter","on":true}`)

	for i, q := range []string{"When is lunch?", "Slides online?"} {
		if code, b := ts.anon("POST", "/api/audience/"+ts.showCode+"/ask",
			[]byte(fmt.Sprintf(`{"item":%d,"text":%q,"peer":"mod-%d"}`, qa, q, i)), "application/json"); code != 200 {
			t.Fatalf("ask %q: %d %s", q, code, b)
		}
	}
	items := modList(t, ts)
	if items[0].Pending != 2 || len(items[0].Children) != 2 {
		t.Fatalf("moderation queue: %+v", items[0])
	}
	byQ := map[string]int64{}
	for _, c := range items[0].Children {
		byQ[c.Question] = c.ID
	}
	modPost(t, ts, fmt.Sprintf("/%d/moderate", byQ["When is lunch?"]), `{"status":"approved"}`)
	modPost(t, ts, fmt.Sprintf("/%d/moderate", byQ["Slides online?"]), `{"status":"dismissed"}`)
	modPost(t, ts, fmt.Sprintf("/%d/spotlight", qa), fmt.Sprintf(`{"entry":%d}`, byQ["When is lunch?"]))

	_, b := ts.anon("GET", "/api/audience/"+ts.showCode, nil, "")
	if !strings.Contains(string(b), "When is lunch?") || strings.Contains(string(b), "Slides online?") || !strings.Contains(string(b), `"spotlight"`) {
		t.Fatalf("phone view: %s", b)
	}
	modPost(t, ts, fmt.Sprintf("/%d/moderate", byQ["When is lunch?"]), `{"status":"answered"}`)
	_, b = ts.anon("GET", "/api/audience/"+ts.showCode, nil, "")
	if strings.Contains(string(b), `"spotlight"`) || !strings.Contains(string(b), `"answered"`) {
		t.Fatalf("answered question should leave the spotlight but stay listed: %s", b)
	}
	// Hide takes it off both targets; phones see nothing.
	modPost(t, ts, fmt.Sprintf("/%d/hide", qa), `{}`)
	_, b = ts.anon("GET", "/api/audience/"+ts.showCode, nil, "")
	if strings.Contains(string(b), "Ask the panel") {
		t.Fatalf("hidden item still on air: %s", b)
	}
	// Delete the item with its entries.
	if code, _ := ts.call("DELETE", fmt.Sprintf("/api/shows/%s/polls/%d", ts.showCode, qa), nil, ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if it := modList(t, ts); len(it) != 0 {
		t.Fatalf("deleted item still listed: %+v", it)
	}
	// Strangers cannot moderate.
	if code, _ := ts.anon("GET", "/api/shows/"+ts.showCode+"/polls", nil, ""); code != 401 {
		t.Errorf("anonymous moderator list: %d, want 401", code)
	}
}

func TestPresenterTargetNeverReachesPhones(t *testing.T) {
	ts := newAPITest(t)
	p := mustPollCreate(t, ts, `{"kind":"poll","question":"Speaker only?","options":["A","B"]}`)
	modPost(t, ts, fmt.Sprintf("/%d/show", p), `{"target":"presenter"}`)
	_, b := ts.anon("GET", "/api/audience/"+ts.showCode, nil, "")
	if strings.Contains(string(b), "Speaker only?") {
		t.Fatalf("presenter-only item reached phones: %s", b)
	}
	if code, _ := ts.call("POST", "/api/audience/"+ts.showCode+"/vote", []byte(fmt.Sprintf(`{"pollId":%d,"choice":"0","peer":"x"}`, p)), ""); code != 400 {
		t.Errorf("vote on a presenter-only item: %d, want 400", code)
	}
	// Results before showing to anyone are refused; after, they follow.
	q := mustPollCreate(t, ts, `{"kind":"poll","question":"Hidden","options":["A","B"]}`)
	if code, _ := ts.call("POST", fmt.Sprintf("/api/shows/%s/polls/%d/results", ts.showCode, q), []byte(`{}`), ""); code != 400 {
		t.Errorf("results on a hidden item: %d, want 400", code)
	}
}

func TestPollCreateValidation(t *testing.T) {
	ts := newAPITest(t)
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/polls",
		[]byte(`{"kind":"poll","question":"No options"}`), ""); code != 400 {
		t.Errorf("optionless poll accepted: %d", code)
	}
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/polls",
		[]byte(`{"kind":"quiz","question":"2+2?","options":["3","4"]}`), ""); code != 400 {
		t.Errorf("quiz without an answer accepted: %d", code)
	}
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/polls",
		[]byte(`{"kind":"quiz","question":"2+2?","options":["4","3"],"correct":0}`), ""); code != 200 {
		t.Fatalf("quiz create: %d %s", code, b)
	}
	if it := modList(t, ts); len(it) != 1 || it[0].Correct != 0 {
		t.Errorf("correct index 0 not returned to the moderator: %+v", it)
	}
}
