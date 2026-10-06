package routes_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"timerpi/routes"
)

// BUGLOG RW2/RW3: a phone's identity is the server's tp_aud cookie. A body
// "peer" is ignored, so inventing a new one per request no longer buys a
// new vote, and a 1 MB peer is never stored.
func TestAudienceVoteIdentityIsServerIssued(t *testing.T) {
	ts := newAPITest(t)
	pid := mustPollCreate(t, ts, `{"kind":"poll","question":"Lunch?","options":["Pizza","Skyr"]}`)
	if code, b := ts.call("POST", fmt.Sprintf("/api/shows/%s/polls/%d/show", ts.showCode, pid), []byte(`{"target":"audience","on":true}`), ""); code != 200 {
		t.Fatalf("show: %d %s", code, b)
	}
	phone := newPersona(ts)
	huge := strings.Repeat("x", 1<<20)
	for i, peer := range []string{"a", "b", huge} {
		code, b := phone.do("POST", "/api/audience/"+ts.showCode+"/vote", fmt.Sprintf(`{"pollId":%d,"choice":"0","peer":%q}`, pid, peer))
		if code != 200 && code != http.StatusTooManyRequests {
			t.Fatalf("vote %d: %d %s", i, code, b)
		}
		voteGuardWait()
	}
	var n, longest int
	if err := ts.db.Get(&n, `SELECT COUNT(*) FROM votes WHERE poll_id = ?`, pid); err != nil {
		t.Fatal(err)
	}
	if err := ts.db.Get(&longest, `SELECT COALESCE(MAX(LENGTH(peer)), 0) FROM votes`); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("one phone with three invented peers cast %d votes, want 1", n)
	}
	if longest > 64 {
		t.Errorf("stored peer is %d bytes", longest)
	}
}

// The per-IP mint budget: a script that drops its cookie every request
// gets a few identities, then 429.
func TestAudienceDeviceMintBudget(t *testing.T) {
	ts := newAPITest(t)
	old := routes.SetAudienceMintBudget(3)
	defer routes.SetAudienceMintBudget(old)
	codes := []int{}
	for i := 0; i < 5; i++ {
		code, _ := ts.anon("GET", "/a/"+ts.showCode, nil, "")
		codes = append(codes, code)
	}
	if codes[2] != 200 || codes[3] != http.StatusTooManyRequests {
		t.Fatalf("mint budget 3: codes %v", codes)
	}
}

// voteGuardWait outlasts the 300 ms per-device vote throttle.
func voteGuardWait() { time.Sleep(320 * time.Millisecond) }
