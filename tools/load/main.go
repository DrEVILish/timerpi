// Command load measures a real TimerPi across HTTP/WS from another machine.
// It only modifies the disposable event it creates and deletes that event on exit.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

type report struct {
	Clients         int     `json:"clients"`
	Joined          int     `json:"joined"`
	Disconnected    int     `json:"disconnected"`
	JoinMS          int64   `json:"join_ms"`
	Votes           int     `json:"votes"`
	VoteErrors      int     `json:"vote_errors"`
	VoteP95MS       float64 `json:"vote_p95_ms"`
	FinalTotal      int     `json:"final_total"`
	Delivered       int     `json:"delivered"`
	DeliveryP95MS   float64 `json:"delivery_p95_ms"`
	OperatorSamples int     `json:"operator_samples"`
	OperatorErrors  int     `json:"operator_errors"`
	OperatorP95MS   float64 `json:"operator_p95_ms"`
	Cleaned         bool    `json:"cleaned"`
	Pass            bool    `json:"pass"`
}

type phone struct {
	conn      *websocket.Conn
	client    *http.Client
	delivered chan time.Time
}
type frame struct {
	T    string `json:"t"`
	Poll struct {
		ID       int64  `json:"id"`
		Question string `json:"question"`
	} `json:"poll"`
}

type eventAccess struct{ Code, Password string }

func main() {
	base := flag.String("base", "", "TimerPi base URL; creates and deletes its own disposable event")
	n := flag.Int("clients", 200, "audience clients (200 floor, 1000 target)")
	event := flag.String("event", "", "owner-supplied disposable event; creates/deletes only a new load room")
	passwordFile := flag.String("password-file", "", "private file containing supplied event password")
	flag.Parse()
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signals, 90*time.Second)
	defer cancel()
	var access []eventAccess
	if *event != "" || *passwordFile != "" {
		info, e := os.Stat(*passwordFile)
		if e != nil || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
			fmt.Fprintln(os.Stderr, "password file must exist, be private (0600), and <=4096 bytes")
			os.Exit(1)
		}
		raw, e := os.ReadFile(*passwordFile)
		code := strings.ToUpper(strings.ReplaceAll(*event, "-", ""))
		valid := len(code) == 8
		for _, c := range code {
			if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				valid = false
			}
		}
		password := strings.TrimSpace(string(raw))
		if e != nil || !valid || password == "" {
			fmt.Fprintln(os.Stderr, "supply an event code and a nonempty private password file")
			os.Exit(1)
		}
		access = append(access, eventAccess{Code: code, Password: password})
	}
	r, err := probe(ctx, *base, *n, access...)
	if err == nil {
		err = assess(r)
	}
	r.Pass = err == nil
	_ = json.NewEncoder(os.Stdout).Encode(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load probe:", err)
		os.Exit(1)
	}
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 8 * time.Second}
}

// request never includes credential-bearing room/event paths or response bodies in errors.
func request(ctx context.Context, c *http.Client, base, method, path string, body, out any) error {
	var input io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, input)
	if err != nil {
		return errors.New("invalid request")
	}
	req.Header.Set("Origin", base)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return errors.New("HTTP request failed or timed out")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
			return errors.New("invalid JSON response")
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return nil
}

func probe(ctx context.Context, base string, n int, access ...eventAccess) (r report, err error) {
	r.Clients = n
	u, e := url.Parse(base)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || n < 1 || n > 1000 {
		return r, errors.New("supply an HTTP(S) origin without credentials/path and 1..1000 clients")
	}
	base = strings.TrimRight(base, "/")
	operator := newClient()
	var event struct {
		Code string `json:"code"`
	}
	room := ""
	cleanupPath := ""
	if len(access) > 0 {
		event.Code = access[0].Code
		if err = request(ctx, operator, base, "POST", "/api/events/"+event.Code+"/login", map[string]any{"pw": access[0].Password}, nil); err != nil {
			return r, fmt.Errorf("sign into supplied test event: %w", err)
		}
		var created struct {
			Code string `json:"code"`
		}
		if err = request(ctx, operator, base, "POST", "/api/events/"+event.Code+"/rooms", map[string]any{"name": fmt.Sprintf("Disposable load room %d", time.Now().UnixNano())}, &created); err != nil {
			return r, fmt.Errorf("create isolated load room: %w", err)
		}
		room = created.Code
		if room == "" {
			return r, errors.New("room creation returned no code")
		}
		cleanupPath = "/api/events/" + event.Code + "/rooms/" + room
	} else {
		var secret [24]byte
		if _, err = rand.Read(secret[:]); err != nil {
			return r, err
		}
		if err = request(ctx, operator, base, "POST", "/api/events", map[string]any{"name": "Disposable audience load probe", "password": hex.EncodeToString(secret[:]), "rooms": []string{"Load room"}}, &event); err != nil {
			return r, fmt.Errorf("create disposable event: %w", err)
		}
		if event.Code == "" {
			return r, errors.New("event creation returned no code")
		}
		cleanupPath = "/api/events/" + event.Code
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if e := request(cleanup, operator, base, "DELETE", cleanupPath, nil, nil); e != nil {
			err = errors.Join(err, fmt.Errorf("disposable fixture cleanup failed: %w", e))
		} else {
			r.Cleaned = true
		}
	}()
	if room == "" {
		var lobby struct {
			Event struct {
				Rooms []struct {
					Code string `json:"code"`
				} `json:"rooms"`
			} `json:"event"`
		}
		if err = request(ctx, operator, base, "GET", "/api/events/"+event.Code, nil, &lobby); err != nil {
			return r, err
		}
		if len(lobby.Event.Rooms) != 1 || lobby.Event.Rooms[0].Code == "" {
			return r, errors.New("disposable room missing")
		}
		room = lobby.Event.Rooms[0].Code
	}
	prefix := "/api/shows/" + room
	if err = request(ctx, operator, base, "PUT", prefix+"/cues", []map[string]any{{"label": "Load test timer", "durationMS": 600000}}, nil); err != nil {
		return r, err
	}
	if err = request(ctx, operator, base, "POST", prefix+"/cmd/go", map[string]any{}, nil); err != nil {
		return r, err
	}
	var poll struct {
		ID int64 `json:"id"`
	}
	if err = request(ctx, operator, base, "POST", prefix+"/polls", map[string]any{"kind": "poll", "question": "Load probe", "options": []string{"A", "B"}}, &poll); err != nil {
		return r, err
	}
	if poll.ID < 1 {
		return r, errors.New("poll creation returned no id")
	}
	pollPath := fmt.Sprintf("%s/polls/%d", prefix, poll.ID)
	if err = request(ctx, operator, base, "POST", pollPath+"/show", map[string]any{"target": "audience", "on": true}, nil); err != nil {
		return r, err
	}
	marker := fmt.Sprintf("Delivery marker %d", time.Now().UnixNano())
	phones := make([]*phone, n)
	var disconnected atomic.Int32
	var closing atomic.Bool
	defer func() {
		closing.Store(true)
		r.Disconnected = int(disconnected.Load())
		for _, p := range phones {
			if p != nil {
				p.conn.Close()
			}
		}
	}()
	var joined atomic.Int32
	var wg sync.WaitGroup
	sem := make(chan struct{}, 50)
	start := time.Now()
	joinCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	for i := range phones {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-joinCtx.Done():
				return
			}
			defer func() { <-sem }()
			wsBase := "ws" + strings.TrimPrefix(base, "http")
			dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second}
			conn, _, e := dialer.DialContext(joinCtx, wsBase+"/ws", http.Header{"Origin": []string{base}})
			if e != nil {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(8 * time.Second))
			if e = conn.WriteJSON(map[string]any{"v": 1, "t": "join", "role": "audience", "show": room, "peerId": fmt.Sprintf("probe-%d", i), "joinedAt": time.Now().UnixMilli()}); e != nil {
				conn.Close()
				return
			}
			deadline, _ := joinCtx.Deadline()
			conn.SetReadDeadline(deadline)
			for {
				var f frame
				if e = conn.ReadJSON(&f); e != nil || f.T == "err" {
					conn.Close()
					return
				}
				if f.T == "poll" && f.Poll.ID == poll.ID {
					break
				}
			}
			p := &phone{conn: conn, client: newClient(), delivered: make(chan time.Time, 1)}
			phones[i] = p
			joined.Add(1)
			go func() {
				seen := false
				for {
					conn.SetReadDeadline(time.Now().Add(45 * time.Second))
					var f frame
					if conn.ReadJSON(&f) != nil || f.T == "err" {
						if !closing.Load() {
							disconnected.Add(1)
						}
						return
					}
					if !seen && f.T == "poll" && f.Poll.Question == marker {
						seen = true
						select {
						case p.delivered <- time.Now():
						default:
						}
					}
				}
			}()
		}(i)
	}
	wg.Wait()
	cancel()
	r.JoinMS = time.Since(start).Milliseconds()
	r.Joined = int(joined.Load())
	if r.Joined != n {
		return r, fmt.Errorf("only %d/%d audience joins completed", r.Joined, n)
	}
	var mu sync.Mutex
	var opTimes, voteTimes []float64
	var opErrors, votes int
	stop := make(chan struct{})
	opDone := make(chan struct{})
	go func() {
		defer close(opDone)
		paused := false
		for {
			action := "pause"
			if paused {
				action = "resume"
			}
			began := time.Now()
			var out struct {
				OK       bool `json:"ok"`
				Snapshot struct {
					Runtime struct {
						Paused  bool `json:"paused"`
						Running bool `json:"running"`
					} `json:"runtime"`
				} `json:"snapshot"`
			}
			e := request(ctx, operator, base, "POST", prefix+"/cmd/"+action, map[string]any{}, &out)
			opTimes = append(opTimes, float64(time.Since(began).Microseconds())/1000)
			paused = !paused
			if e != nil || !out.OK || out.Snapshot.Runtime.Paused != paused || !out.Snapshot.Runtime.Running {
				opErrors++
			}
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()
	// At most 400 vote requests/s; measure fanout halfway through real voting.
	var sent time.Time
	var markerErr error
	for i, p := range phones {
		wg.Add(1)
		go func(i int, p *phone) {
			defer wg.Done()
			began := time.Now()
			e := request(ctx, p.client, base, "POST", "/api/audience/"+room+"/vote", map[string]any{"pollId": poll.ID, "choice": fmt.Sprint(i % 2)}, nil)
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				votes++
			}
			voteTimes = append(voteTimes, float64(time.Since(began).Microseconds())/1000)
		}(i, p)
		if i == n/2 {
			sent = time.Now()
			markerErr = request(ctx, operator, base, "PATCH", pollPath, map[string]any{"question": marker}, nil)
		}
		time.Sleep(time.Second / 400)
	}
	wg.Wait()
	// Give even small fixture runs concurrent operator pause and resume samples.
	time.Sleep(500 * time.Millisecond)
	close(stop)
	<-opDone
	r.Votes = votes
	r.VoteErrors = n - votes
	r.VoteP95MS = p95(voteTimes)
	r.OperatorSamples = len(opTimes)
	r.OperatorErrors = opErrors
	r.OperatorP95MS = p95(opTimes)
	if markerErr != nil {
		return r, markerErr
	}
	var items struct {
		Items []struct {
			ID    int64 `json:"id"`
			Total int   `json:"total"`
		} `json:"items"`
	}
	if err = request(ctx, operator, base, "GET", prefix+"/polls", nil, &items); err != nil {
		return r, err
	}
	for _, p := range items.Items {
		if p.ID == poll.ID {
			r.FinalTotal = p.Total
		}
	}
	timeout := time.NewTimer(4 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var delivery []float64
	for {
		for _, p := range phones {
			select {
			case at := <-p.delivered:
				r.Delivered++
				delivery = append(delivery, float64(at.Sub(sent).Microseconds())/1000)
			default:
			}
		}
		if r.Delivered == n {
			break
		}
		select {
		case <-tick.C:
		case <-timeout.C:
			r.DeliveryP95MS = p95(delivery)
			return r, nil
		case <-ctx.Done():
			return r, ctx.Err()
		}
	}
	r.DeliveryP95MS = p95(delivery)
	return r, nil
}

func p95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	return values[int(math.Ceil(float64(len(values))*.95))-1]
}
func assess(r report) error {
	if r.Clients < 1 || r.Joined != r.Clients || r.Disconnected != 0 || r.JoinMS > 30000 {
		return errors.New("join target failed")
	}
	if r.Votes != r.Clients || r.FinalTotal != r.Clients || r.VoteErrors != 0 {
		return errors.New("vote target failed")
	}
	if r.Delivered*100 < r.Clients*99 || r.DeliveryP95MS <= 0 || r.DeliveryP95MS > 1000 {
		return errors.New("fanout target failed (99% delivery, p95 <=1000ms)")
	}
	if r.OperatorSamples < 2 || r.OperatorErrors != 0 || r.OperatorP95MS > 250 {
		return errors.New("operator target failed (verified pause/resume, p95 <=250ms)")
	}
	if !r.Cleaned {
		return errors.New("disposable event cleanup failed")
	}
	return nil
}
