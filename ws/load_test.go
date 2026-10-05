//go:build !race

// Load harness (PLAN §11.5, phase 4) — gated behind TP_LOAD=1 so the normal
// suite stays fast: `TP_LOAD=1 go test ./ws/ -run TestLoad -v`.
//
// TestLoadSteadyAudience: N audience phones on the lane, 50 broadcasts/s —
// p95 delivery latency < 100 ms, ≥99% of frames delivered.
// TestLoadJoinStorm: N phones scanning the QR within a few seconds — every
// join answered, no cap spills, boards unaffected.
//
// N scales down to the process fd limit (1000 dials need ~2000 fds) so the
// harness runs on modest containers; set TP_LOAD_N to force a number.
package ws

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"timerpi/timerpi"
)

func loadN(t *testing.T) int {
	t.Helper()
	if v := os.Getenv("TP_LOAD_N"); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil && n > 0 {
			return n
		}
	}
	n := 1000
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rl); err == nil {
		headroom := int(rl.Cur)/2 - 200
		if headroom < n {
			t.Logf("fd limit %d: scaling audience count to %d", rl.Cur, headroom)
			n = headroom
		}
	}
	if n < 50 {
		t.Skipf("fd limit too low for a meaningful run (audience %d)", n)
	}
	return n
}

func dialAudience(t *testing.T, ts *testServer, i int) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + ts.srv.URL[4:] + "/ws" // http:// → ws://
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Errorf("dial %d: %v", i, err)
		return nil
	}
	join := fmt.Sprintf(`{"v":1,"t":"join","role":"audience","show":%q,"peerId":"phone-%d","joinedAt":%d}`,
		ts.showCode, i, time.Now().UnixMilli())
	if err := conn.WriteMessage(websocket.TextMessage, []byte(join)); err != nil {
		t.Errorf("join %d: %v", i, err)
		conn.Close()
		return nil
	}
	return conn
}

func TestLoadSteadyAudience(t *testing.T) {
	if os.Getenv("TP_LOAD") == "" {
		t.Skip("load harness: set TP_LOAD=1")
	}
	ts := newTestServer(t, false)
	n := loadN(t)

	var mu sync.Mutex
	var conns []*websocket.Conn
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if c := dialAudience(t, ts, i); c != nil {
				mu.Lock()
				conns = append(conns, c)
				mu.Unlock()
			}
		}(i)
		if i%50 == 0 {
			wg.Wait() // gentle staggering — the join storm test does the wild one
		}
	}
	wg.Wait()
	if len(conns) < n*99/100 {
		t.Fatalf("only %d/%d audience joins landed", len(conns), n)
	}
	// Joins are processed asynchronously — wait for the lane to fill.
	fillDeadline := time.Now().Add(3 * time.Second)
	for ts.hub.AudSessions() < n*99/100 && time.Now().Before(fillDeadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if got := ts.hub.AudSessions(); got < n*99/100 {
		t.Fatalf("lane count %d, want ≥%d", got, n*99/100)
	}

	// Every client counts the poll frames it receives; ts carries the send
	// instant so latency is measurable (same clock in-process).
	var gotMu sync.Mutex
	got := make([]int, len(conns))
	latencies := make([][]int64, len(conns))
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i, c := range conns {
		readers.Add(1)
		go func(i int, c *websocket.Conn) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				c.SetReadDeadline(time.Now().Add(2 * time.Second))
				_, raw, err := c.ReadMessage()
				if err != nil {
					return
				}
				var f struct {
					Ts int64 `json:"ts"`
				}
				_ = json.Unmarshal(raw, &f)
				gotMu.Lock()
				got[i]++
				latencies[i] = append(latencies[i], time.Now().UnixMilli()-f.Ts)
				gotMu.Unlock()
			}
		}(i, c)
	}

	// 50 broadcasts/s for 3 s — the vote-storm broadcast shape.
	sent := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ts.hub.BroadcastPoll(ts.showID)
		sent++
		time.Sleep(20 * time.Millisecond)
	}
	close(stop)
	readers.Wait()
	for _, c := range conns {
		c.Close()
	}

	// ≥99% of frames reached ≥99% of the lane; p95 latency < 100 ms.
	var all []int64
	missedClients := 0
	for i := range conns {
		if sent > 0 && got[i] < sent*99/100 {
			missedClients++
		}
		all = append(all, latencies[i]...)
	}
	if missedClients > len(conns)/100 {
		t.Errorf("%d/%d clients missed >1%% of frames", missedClients, len(conns))
	}
	if len(all) > 1 {
		p95 := all[len(all)*95/100]
		t.Logf("audience lane: %d clients, %d frames sent, p95 latency %dms", len(conns), sent, p95)
		if p95 > 100 {
			t.Errorf("p95 broadcast latency %dms > 100ms", p95)
		}
	}
	_ = timerpi.PollView{}
}

func TestLoadJoinStorm(t *testing.T) {
	if os.Getenv("TP_LOAD") == "" {
		t.Skip("load harness: set TP_LOAD=1")
	}
	ts := newTestServer(t, false)
	n := loadN(t)

	var mu sync.Mutex
	var conns []*websocket.Conn
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ { // a phone per ~10 ms = the QR-stampede shape
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if c := dialAudience(t, ts, i); c != nil {
				mu.Lock()
				conns = append(conns, c)
				mu.Unlock()
			}
		}(i)
		time.Sleep(10 * time.Millisecond)
	}
	wg.Wait()
	t.Logf("join storm: %d/%d phones on the lane in %s", len(conns), n, time.Since(start).Round(time.Millisecond))
	if len(conns) < n*99/100 {
		t.Fatalf("storm dropped joins: %d/%d", len(conns), n)
	}
	// The boards' own budget is untouched by the audience lane.
	if got := ts.hub.Sessions(); got > 512 {
		t.Errorf("board sessions leaked into the audience count: %d", got)
	}
	for _, c := range conns {
		c.Close()
	}
}
