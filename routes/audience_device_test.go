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

// The per-(room, IP) mint budget: a script that drops its cookie every
// request gets a bounded number of identities, then 429 — on the vote, as
// JSON the phone page turns into a retry. Viewing the page never mints
// (E2E #2: phone 21 on venue Wi-Fi got a raw JSON 429 page).
func TestAudienceDeviceMintBudget(t *testing.T) {
	ts := newAPITest(t)
	pid := mustPollCreate(t, ts, `{"kind":"poll","question":"Lunch?","options":["Pizza","Skyr"]}`)
	modPost(t, ts, fmt.Sprintf("/%d/show", pid), `{"target":"audience","on":true}`)
	old := routes.SetAudienceMintBudget(3)
	defer routes.SetAudienceMintBudget(old)
	for i := 0; i < 5; i++ {
		code, b := ts.anon("GET", "/a/"+ts.showCode, nil, "")
		if code != 200 || strings.Contains(string(b), "tp_aud") {
			t.Fatalf("page view %d: %d", i, code)
		}
	}
	codes := []int{}
	for i := 0; i < 5; i++ {
		code, _ := ts.anon("POST", "/api/audience/"+ts.showCode+"/vote", []byte(fmt.Sprintf(`{"pollId":%d,"choice":"0"}`, pid)), "application/json")
		codes = append(codes, code)
	}
	if codes[2] != 200 || codes[3] != http.StatusTooManyRequests {
		t.Fatalf("mint budget 3: codes %v", codes)
	}
}

// PRODUCT A10 / E2E #2: 1,000 phones behind ONE address (venue NAT) open
// the page and vote within ~30 s. Sequential, so it stays fast in CI; a
// "room is busy" 429 (the per-room soak budget) is retried after its
// Retry-After like the phone does.
func TestAudienceThousandPhonesOneIP(t *testing.T) {
	ts := newAPITest(t)
	pid := mustPollCreate(t, ts, `{"kind":"poll","question":"Lunch?","options":["Pizza","Skyr"]}`)
	modPost(t, ts, fmt.Sprintf("/%d/show", pid), `{"target":"audience","on":true}`)
	start := time.Now()
	body := fmt.Sprintf(`{"pollId":%d,"choice":"1"}`, pid)
	for i := 0; i < 1000; i++ {
		phone := newPersona(ts)
		if code, _ := phone.do("GET", "/a/"+ts.showCode, ""); code != 200 {
			t.Fatalf("phone %d page: %d", i, code)
		}
		for try := 0; ; try++ {
			code, b := phone.do("POST", "/api/audience/"+ts.showCode+"/vote", body)
			if code == 200 {
				break
			}
			if code != http.StatusTooManyRequests || !strings.Contains(b, "busy") || try > 5 {
				t.Fatalf("phone %d vote: %d %s", i, code, b)
			}
			time.Sleep(time.Second)
		}
	}
	var n int
	if err := ts.db.Get(&n, `SELECT COUNT(*) FROM votes WHERE poll_id = ?`, pid); err != nil {
		t.Fatal(err)
	}
	if n != 1000 {
		t.Errorf("votes = %d, want 1000", n)
	}
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("1,000 phones took %s, want under 30 s", d)
	}
}

// voteGuardWait outlasts the 300 ms per-device vote throttle.
func voteGuardWait() { time.Sleep(320 * time.Millisecond) }
