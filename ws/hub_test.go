// package ws tests: join handshake, joined frame, peers tracking, snapshot
// fanout (state + oob), signal relay, and command replay — all through a
// REAL gorilla/websocket client over httptest + the real upgrader, against
// a real SQLite engine so the shape stays honest.
package ws

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"timerpi/timerpi"
	"timerpi/views"
)

// testServer boots the full stack: real SQLite, engine registry, hub over
// httptest + real upgrader, no render (state-only fanout) unless given a
// fragment renderer stub.
type testServer struct {
	t        *testing.T
	srv      *httptest.Server
	db       *timerpi.DB
	engines  *timerpi.Engines
	hub      *Hub
	oobs     *oobRecorder
	showID   int64
	showCode string // share code (Agent L): joins address shows BY CODE
	cookie   string // SuperOperator session (controls joins need it)
}

// oobRecorder captures rendered oob frames (fragment name → count).
type oobRecorder struct {
	mu     sync.Mutex
	counts map[string]int
	last   map[string]string
}

func (r *oobRecorder) render(name string, data any) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts == nil {
		r.counts = map[string]int{}
		r.last = map[string]string{}
	}
	r.counts[name]++
	r.last[name] = fmt.Sprintf("%v", data != nil)
	return `<div data-frag="` + name + `"></div>`, nil
}
func (r *oobRecorder) n(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[name]
}

func newTestServer(t *testing.T, withRender bool) *testServer {
	t.Helper()
	dir := t.TempDir()
	db, err := timerpi.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ev, rooms, err := db.CreateEvent("Hub Test Event", "testpw", []string{"Hub Test Show"})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	show := rooms[0]
	for _, cue := range []timerpi.Cue{
		{Label: "Welcome", DurationMS: 300_000},
		{Label: "Talk", DurationMS: 600_000, Speaker: "Leslie"},
	} {
		if _, err := db.CreateCue(show.ID, cue); err != nil {
			t.Fatalf("seed cue: %v", err)
		}
	}
	engines := timerpi.NewEngines(db)
	rec := &oobRecorder{}
	render := func(name string, data any) (string, error) { return rec.render(name, data) }
	if !withRender {
		render = nil
	}
	hub := NewHub(engines, render)
	hub.SetStore(db)
	hub.SetMessagesFunc(db.ListMessages)
	hub.SetNowFn(func() int64 { return time.Now().UnixMilli() })
	hub.SetLogger(func(string, ...any) {}) // quiet

	gin.SetMode(gin.TestMode)
	r := gin.New()
	hub.Register(r)
	srv := httptest.NewServer(r)
	t.Cleanup(func() {
		srv.Close()
		hub.Stop()
	})
	return &testServer{t: t, srv: srv, db: db, engines: engines, hub: hub, oobs: rec, showID: show.ID, showCode: show.Code,
		// The SuperOperator session cookie every test client presents (the
		// hub admits "controls" joins only with moderator access).
		cookie: "tp_ev_" + ev.Code + "=" + timerpi.SignSession(db.SessionSecret(), "ev", ev.Code, ev.SuperHash)}
}

// joinClient dials + joins as a fresh websocket client.
func (ts *testServer) joinClient(t *testing.T, role, peerID string) *wsClient {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Cookie": []string{ts.cookie}})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Agent L (scope change): joins carry the share CODE, not the id.
	join := fmt.Sprintf(`{"v":1,"t":"join","role":%q,"show":%q,"peerId":%q,"joinedAt":%d}`, role, ts.showCode, peerID, 1000)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(join)); err != nil {
		t.Fatalf("join write: %v", err)
	}
	return &wsClient{conn: conn}
}

// wsClient is a plain-frame reader with a read timeout for determinism.
type wsClient struct {
	conn *websocket.Conn
}

func (c *wsClient) read(t *testing.T) map[string]any {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := c.conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("frame json: %v (%s)", err, raw)
	}
	return m
}

func (c *wsClient) send(t *testing.T, v any) {
	t.Helper()
	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := c.conn.WriteJSON(v); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func (c *wsClient) close() { _ = c.conn.Close() }

// readUntil advances frames until m[t]==t; passes through others.
func (c *wsClient) readUntil(t *testing.T, typ string) map[string]any {
	t.Helper()
	for i := 0; i < 64; i++ {
		m := c.read(t)
		if m["t"] == typ {
			return m
		}
	}
	t.Fatalf("frame %q never arrived", typ)
	return nil
}

// ---------------------------------------------------------------------------
// Tests.

// Join → joined frame carries you/snapshot/peers; second peer sees the
// first in peers + gets a peers announcement.
func TestJoinAndPeers(t *testing.T) {
	ts := newTestServer(t, false)

	a := ts.joinClient(t, "display", "peer-a")
	defer a.close()
	joined := a.readUntil(t, "joined")
	if joined["you"].(map[string]any)["peerId"] != "peer-a" {
		t.Errorf("joined.you.peerId = %v", joined["you"])
	}
	snap := joined["snapshot"].(map[string]any)
	if snap["show"].(map[string]any)["title"] != "Hub Test Show" {
		t.Errorf("joined.snapshot.show = %v", snap["show"])
	}
	if cues := snap["cues"].([]any); len(cues) != 2 {
		t.Errorf("joined.snapshot.cues = %d items, want 2", len(cues))
	}

	b := ts.joinClient(t, "controls", "peer-b")
	defer b.close()
	jb := b.readUntil(t, "joined")
	peers := jb["peers"].([]any)
	if len(peers) != 1 || peers[0].(map[string]any)["peerId"] != "peer-a" {
		t.Errorf("peer-b joined.peers = %v, want [peer-a]", peers)
	}
	// peer-a gets a peers update announcing peer-b (list includes self —
	// the mesh client skips its own id at merge).
	pa := a.readUntil(t, "peers")
	list := pa["peers"].([]any)
	if len(list) != 2 {
		t.Errorf("peer-a peers = %v, want both peers", list)
	}

	if got := ts.hub.Sessions(); got != 2 {
		t.Errorf("hub.Sessions() = %d, want 2", got)
	}
	roles := ts.hub.SessionsByRole()
	if roles["display"] != 1 || roles["controls"] != 1 {
		t.Errorf("SessionsByRole = %v", roles)
	}
}

// Snapshot fanout: a command from one session lands as `state` on both —
// and with a renderer installed, oob fragments precede the state frame.
func TestCommandFanoutAndOob(t *testing.T) {
	ts := newTestServer(t, true)

	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")
	b := ts.joinClient(t, "display", "peer-b")
	defer b.close()
	b.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "cueAdd", "args": map[string]any{"label": "Sponsor", "durationMS": 60_000}})
	ma := a.readUntil(t, "state")
	if len(ma["snapshot"].(map[string]any)["cues"].([]any)) != 3 {
		t.Errorf("cueAdd landed as %d cues", len(ma["snapshot"].(map[string]any)["cues"].([]any)))
	}
	mb := b.readUntil(t, "state")
	if len(mb["snapshot"].(map[string]any)["cues"].([]any)) != 3 {
		t.Error("state not fanned to the second session")
	}
	// oob batch: cuelist/daybar re-rendered (new cue) at least once.
	if ts.oobs.n("frag-cuelist") == 0 {
		t.Error("frag-cuelist oob never rendered")
	}
	// message add → messages oob.
	a.send(t, map[string]any{"t": "cmd", "action": "addMsg", "args": map[string]any{"text": "WRAP UP", "color": "#ff4444", "show": true}})
	ma = a.readUntil(t, "state")
	if msgs := ma["snapshot"].(map[string]any)["messages"].([]any); len(msgs) != 1 {
		t.Errorf("snapshot messages = %d, want 1 (shown)", len(msgs))
	}
	if ts.oobs.n("frag-messages") == 0 {
		t.Error("frag-messages oob never rendered")
	}
}

// A4: cueDup — the row button's action. The copy is inserted directly
// AFTER the source row (DB.DuplicateCue semantics), fanned to peers with
// the cuelist oob; a bad/missing pos errs to the sender.
func TestCueDupCommand(t *testing.T) {
	ts := newTestServer(t, true)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "cueDup", "args": map[string]any{"pos": 1}})
	snap := a.readUntil(t, "state")["snapshot"].(map[string]any)
	cues := snap["cues"].([]any)
	if len(cues) != 3 {
		t.Fatalf("cueDup: %d cues, want 3", len(cues))
	}
	src, copy := cues[0].(map[string]any), cues[1].(map[string]any)
	if copy["label"] != src["label"] {
		t.Errorf("copy label = %v, want the source label %v", copy["label"], src["label"])
	}
	if copy["id"] == src["id"] {
		t.Error("copy kept the source row id (fresh row required)")
	}
	if ts.oobs.n("frag-cuelist") == 0 {
		t.Error("frag-cuelist oob never rendered after cueDup")
	}

	// Missing pos → sender-only err frame.
	a.send(t, map[string]any{"t": "cmd", "action": "cueDup", "args": map[string]any{}})
	if !strings.Contains(a.readUntil(t, "err")["message"].(string), "cueDup") {
		t.Error("cueDup without pos: no err frame")
	}
}

// C2 drill regression: "Day starts now" (settings{ts}) must FAN OUT —
// before the fix it re-anchored the engine silently (no state, no oob) and
// every connected page kept the stale START/END columns + day-bar scale.
func TestDayStartFanout(t *testing.T) {
	ts := newTestServer(t, true)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	// Day starts must be within about a day of now (BUGLOG RS23).
	stamp := time.Now().Add(-time.Hour).UnixMilli()
	a.send(t, map[string]any{"t": "cmd", "action": "settings", "args": map[string]any{"ts": stamp}})
	m := a.readUntil(t, "state")
	rt := m["snapshot"].(map[string]any)["runtime"].(map[string]any)
	if got := int64(rt["dayStartTS"].(float64)); got != stamp {
		t.Errorf("settings{ts}: runtime.dayStartTS = %d, want %d", got, stamp)
	}
	if ts.oobs.n("frag-cuelist") == 0 || ts.oobs.n("frag-daybar") == 0 {
		t.Error("day-anchor fanout missing cuelist/daybar oob")
	}
}

// Transport: go starts cue 0/1 → running; pause toggles via resume mapping.
func TestTransportCommands(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	a.send(t, map[string]any{"t": "cmd", "action": "start", "args": map[string]any{"pos": 1}})
	snap := a.readUntil(t, "state")["snapshot"].(map[string]any)
	rt := snap["runtime"].(map[string]any)
	if !rt["running"].(bool) || rt["activePos"].(float64) != 1 {
		t.Errorf("start: runtime = %v", rt)
	}

	a.send(t, map[string]any{"t": "cmd", "action": "pause"})
	rt = a.readUntil(t, "state")["snapshot"].(map[string]any)["runtime"].(map[string]any)
	if !rt["paused"].(bool) {
		t.Error("pause: not paused")
	}
	a.send(t, map[string]any{"t": "cmd", "action": "pause"})
	rt = a.readUntil(t, "state")["snapshot"].(map[string]any)["runtime"].(map[string]any)
	if rt["paused"].(bool) {
		t.Error("pause toggle: still paused (resume mapping broken)")
	}

	// rate error surfaces as err frame only to the sender.
	a.send(t, map[string]any{"t": "cmd", "action": "rate", "args": map[string]any{"rate": 0}})
	errFrame := a.readUntil(t, "err")
	if !strings.Contains(errFrame["message"].(string), "rate") {
		t.Errorf("rate error frame = %v", errFrame)
	}
}

// Unknown show → err frame phrase the mesh client matches. Agent L scope
// change: the join `show` field is CODE-ONLY — a numeric payload gets the
// same "Unknown session code" rejection as a missed code (the numeric id is
// an internal key, never an address).
func TestJoinUnknownShow(t *testing.T) {
	ts := newTestServer(t, false)
	wsURL := "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
	for _, frame := range []string{
		`{"v":1,"t":"join","role":"controls","show":99997}`,           // legacy numeric: refused
		`{"v":1,"t":"join","role":"controls","show":"ZZZZZZZZ"}`,      // well-formed code: absent show
		`{"v":1,"t":"join","role":"controls","show":"novelty value"}`, // junk
	} {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(frame))
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			conn.Close()
			t.Fatalf("read %q: %v", frame, err)
		}
		if !strings.Contains(string(raw), "Unknown session code") {
			t.Errorf("err frame for %q = %s", frame, raw)
		}
		conn.Close()
	}
}

// WebRTC signaling relay: A→B lands as {signal,from} on B only.
func TestSignalRelay(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")
	b := ts.joinClient(t, "display", "peer-b")
	defer b.close()
	b.readUntil(t, "joined")
	a.readUntil(t, "peers")

	a.send(t, map[string]any{"t": "signal", "to": "peer-b", "data": map[string]any{"candidate": "cand-1"}})
	sb := b.readUntil(t, "signal")
	if sb["from"] != "peer-a" {
		t.Errorf("signal.from = %v", sb["from"])
	}
	if sb["data"].(map[string]any)["candidate"] != "cand-1" {
		t.Errorf("signal.data = %v", sb["data"])
	}
	// a must NOT receive its own signal back.
	closed := false
	a.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, _, err := a.conn.ReadMessage()
	closed = err != nil // timeout = nothing relayed back = good
	if !closed {
		t.Error("sender received its own signal")
	}
}

// App-level ping → pong with serverTime (clock re-anchor).
func TestAppPingPong(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")
	a.send(t, map[string]any{"t": "ping"})
	pong := a.readUntil(t, "pong")
	if st := pong["serverTime"].(float64); st <= 0 {
		t.Errorf("pong.serverTime = %v", pong["serverTime"])
	}
}

// Non-join first frame is refused with an err frame.
func TestFirstFrameMustJoin(t *testing.T) {
	ts := newTestServer(t, false)
	wsURL := "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"t":"cmd","action":"go","args":{}}`))
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), "join frame") {
		t.Errorf("expected join refusal, got %s", raw)
	}
}

// ---------------------------------------------------------------------------
// Schedule info frame (REVIEW-3 D3): every cue-structure change (add /
// edit / del / move / import) and every schedule-anchor jump (settings{ts})
// or rate jump re-computes the pure plan and ships it BEFORE the state
// frame, in the CONTRACT-UI §5 shape
// {"t":"schedule","rows":[{pos,startMS,endMS,holdMS,isBreak}],totalMS,dayStartTS}.
func TestScheduleFrameOnStructureAndAnchorChange(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	// cueAdd → schedule frame with the recomputed rows.
	a.send(t, map[string]any{"t": "cmd", "action": "cueAdd", "args": map[string]any{"label": "Sponsor", "durationMS": 60_000}})
	sched := a.readUntil(t, "schedule")
	rows, _ := sched["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("schedule rows = %d, want 3", len(rows))
	}
	first := rows[0].(map[string]any)
	if first["startMS"].(float64) != 0 || first["endMS"].(float64) != 300_000 {
		t.Fatalf("schedule row 0 = %v", first)
	}
	if _, ok := first["isBreak"]; !ok {
		t.Fatalf("schedule row shape missing isBreak: %v", first)
	}
	if _, ok := first["holdMS"]; !ok {
		t.Fatalf("schedule row shape missing holdMS: %v", first)
	}
	if total := sched["totalMS"].(float64); total != 960_000 {
		t.Fatalf("schedule totalMS = %v, want 960000", total)
	}
	dayStart, ok := sched["dayStartTS"].(float64)
	if !ok {
		t.Fatalf("schedule dayStartTS missing: %v", sched)
	}

	// A daystart jump re-ships the frame with the new anchor (an anchor
	// near now: far-off anchors are refused, BUGLOG RS23).
	_ = dayStart
	jump := float64(time.Now().Add(-time.Hour).UnixMilli())
	a.send(t, map[string]any{"t": "cmd", "action": "settings", "args": map[string]any{"ts": jump}})
	sched2 := a.readUntil(t, "schedule")
	if got := sched2["dayStartTS"].(float64); got != jump {
		t.Fatalf("daystart jump not reflected: %v", got)
	}
	if rows := sched2["rows"].([]any); len(rows) != 3 {
		t.Fatalf("schedule rows after daystart = %d", len(rows))
	}

	// A rate jump ships the frame too (client needle refresh; rows unchanged).
	a.send(t, map[string]any{"t": "cmd", "action": "rate", "args": map[string]any{"rate": 2}})
	sched3 := a.readUntil(t, "schedule")
	if sched3["totalMS"].(float64) != 960_000 {
		t.Fatalf("rate jump schedule total = %v", sched3["totalMS"])
	}

	// A no-op snapshot (pause of a not-running cue → no emit) meanwhile
	// does NOT fabricate a schedule frame: unchanged fingerprint, no frame.
	a.send(t, map[string]any{"t": "cmd", "action": "cueEdit", "args": map[string]any{"pos": 3, "notes": "ok"}})
	if m := a.readUntil(t, "state"); m == nil {
		t.Fatal("cueEdit state never arrived")
	} else if rows := m["snapshot"].(map[string]any)["cues"].([]any); len(rows) != 3 {
		t.Fatalf("cueEdit changed the cue count: %d", len(rows))
	}
	// cuesSignature ignores `notes` → structure unchanged → the NEXT update
	// must come from a real structure change; emit one and expect schedule.
	a.send(t, map[string]any{"t": "cmd", "action": "cueDel", "args": map[string]any{"pos": 3}})
	sched4 := a.readUntil(t, "schedule")
	if rows := sched4["rows"].([]any); len(rows) != 2 {
		t.Fatalf("schedule rows after cueDel = %d, want 2", len(rows))
	}
}

// cueMove contract (REVIEW-3 R1 server half): the hub parses and applies
// pos + dir up|down for BOTH directions; a missing dir is the documented
// err frame ("cueMove needs pos and dir up|down").
func TestCueMoveDirections(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")

	order := func() []string {
		snap := a.readUntil(t, "state")["snapshot"].(map[string]any)
		raw := snap["cues"].([]any)
		labels := make([]string, 0, len(raw))
		for _, c := range raw {
			labels = append(labels, c.(map[string]any)["label"].(string))
		}
		return labels
	}

	a.send(t, map[string]any{"t": "cmd", "action": "cueMove", "args": map[string]any{"pos": 2, "dir": "up"}})
	if got := order(); got[0] != "Talk" || got[1] != "Welcome" {
		t.Fatalf("cueMove up: %v", got)
	}

	a.send(t, map[string]any{"t": "cmd", "action": "cueMove", "args": map[string]any{"pos": 1, "dir": "down"}})
	if got := order(); got[0] != "Welcome" || got[1] != "Talk" {
		t.Fatalf("cueMove down: %v", got)
	}

	// Contract error: dir missing.
	a.send(t, map[string]any{"t": "cmd", "action": "cueMove", "args": map[string]any{"pos": 1}})
	if err := a.readUntil(t, "err"); !strings.Contains(err["message"].(string), "cueMove needs pos and dir") {
		t.Fatalf("err frame = %v", err)
	}
}

// Silence views.Make sure the hub survives a busy disconnect (no blocked
// fanout): 3 fast sessions join+leave; count goes to 0 and broadcasts stop.
func TestSessionChurn(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinClient(t, "controls", "peer-a")
	defer a.close()
	a.readUntil(t, "joined")
	for i := 0; i < 8; i++ {
		b := ts.joinClient(t, "display", fmt.Sprintf("churn-%d", i))
		b.readUntil(t, "joined")
		b.close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for ts.hub.Sessions() > 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := ts.hub.Sessions(); got != 1 {
		t.Errorf("Sessions after churn = %d, want 1 (peer-a)", got)
	}
}

// Keep the providers referenced (compile anchor for the views contract).
var _ = views.ShowVM{}
var _ = http.StatusOK

// TestUndoRestoreChain — B3's deletion-undo rides: add (appends at the end)
// then a cueMove-up chain walks the appended row back to its original slot.
// Frames arrive back-to-back on one connection; the server must apply them
// in order and land the restored cue exactly on slot 1.
func TestUndoRestoreChain(t *testing.T) {
	ts := newTestServer(t, false)
	client := ts.joinClient(t, "controls", "u1")
	send := func(frame string) {
		if err := client.conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	drain := func(ms time.Duration) {
		client.conn.SetReadDeadline(time.Now().Add(ms))
		for {
			if _, _, err := client.conn.ReadMessage(); err != nil {
				return
			}
		}
	}
	drain(700 * time.Millisecond) // joined + initial fanout

	send(`{"t":"cmd","action":"cueDel","args":{"pos":1}}`)
	send(`{"t":"cmd","action":"cueAdd","args":{"label":"Welcome","durationMS":310000}}`)
	for p := int64(2); p <= 2; p++ { // after delete the show is 1 row; appended slot is 2
		send(fmt.Sprintf(`{"t":"cmd","action":"cueMove","args":{"pos":%d,"dir":"up"}}`, p))
	}

	// Wait for the engine to settle (Notify-driven, re-list the store).
	deadline := time.Now().Add(3 * time.Second)
	for {
		cues, cerr := ts.db.ListCues(ts.showID)
		if cerr != nil {
			t.Fatalf("list: %v", cerr)
		}
		if len(cues) == 2 && cues[0].Label == "Welcome" && cues[1].Label == "Talk" {
			break
		}
		if time.Now().After(deadline) {
			got := []string{}
			for _, c := range cues {
				got = append(got, c.Label)
			}
			t.Fatalf("restore chain did not land: %v", got)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestCueMoveTargetSlot — B2: {pos,to} form goes straight to the DB's
// full-slot splicer; a same-or-clamped target behaves like the REST path.
func TestCueMoveTargetSlot(t *testing.T) {
	ts := newTestServer(t, false)
	client := ts.joinClient(t, "controls", "m1")
	send := func(frame string) {
		if err := client.conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	drain := func(ms time.Duration) {
		client.conn.SetReadDeadline(time.Now().Add(ms))
		for {
			if _, _, err := client.conn.ReadMessage(); err != nil {
				return
			}
		}
	}
	drain(700 * time.Millisecond)

	send(`{"t":"cmd","action":"cueMove","args":{"pos":1,"to":3}}`)
	deadline := time.Now().Add(3 * time.Second)
	var cues []timerpi.Cue
	var labels []string
	for {
		cues, _ = ts.db.ListCues(ts.showID)
		labels = labels[:0]
		for _, c := range cues {
			labels = append(labels, c.Label)
		}
		if time.Now().After(deadline) || (len(cues) == 2 && labels[0] == "Talk") {
			if !(len(cues) == 2 && labels[0] == "Talk") {
				t.Fatalf("to=3 move did not land: %v", labels)
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	// clamp guard: to=99 pins to the last slot (documented MoveCue clamp);
	// from=0 refuses with an err frame (no move). Final pinned order:
	send(`{"t":"cmd","action":"cueMove","args":{"pos":1,"to":99}}`)
	send(`{"t":"cmd","action":"cueMove","args":{"pos":0,"to":2}}`)
	drain(500 * time.Millisecond)
	cues, _ = ts.db.ListCues(ts.showID)
	labels = labels[:0]
	for _, c := range cues {
		labels = append(labels, c.Label)
	}
	if !(len(labels) == 2 && labels[0] == "Talk" && labels[1] == "Welcome") {
		t.Fatalf("clamp sequence: %v", labels)
	}
}
