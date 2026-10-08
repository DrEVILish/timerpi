package ws

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"timerpi/timerpi"
)

// dialAnon joins with no cookie (an unkeyed /d/<room> tab: the room code
// is printed on the audience QR).
func (ts *testServer) dialAnon(t *testing.T, peerID, screen, key string) *wsClient {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(
		`{"v":1,"t":"join","role":"display","show":%q,"peerId":%q,"screen":%q,"key":%q}`, ts.showCode, peerID, screen, key)))
	return &wsClient{conn: conn}
}

// quiet asserts nothing of type typ arrives within d.
func (c *wsClient) quiet(t *testing.T, typ string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		c.conn.SetReadDeadline(deadline)
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if strings.Contains(string(raw), `"t":"`+typ+`"`) {
			t.Fatalf("unexpected %s frame: %s", typ, raw)
		}
	}
}

// E2E #1/#5: an untrusted display never learns peer ids, can't signal
// into the mesh, and can't knock a keyed screen off by reusing its id.
func TestUntrustedDisplayOutOfMesh(t *testing.T) {
	ts := newTestServer(t, false)
	op := ts.joinClient(t, "controls", "op-1")
	defer op.close()
	op.readUntil(t, "joined")

	key, err := ts.db.ScreenKey(ts.showID, "Stage")
	if err != nil {
		t.Fatal(err)
	}
	tv := ts.dialAnon(t, "tv-keyed", "Stage", key)
	if j := tv.readUntil(t, "joined"); len(j["peers"].([]any)) != 1 {
		t.Fatalf("keyed screen should see the operator: %v", j["peers"])
	}
	pf := op.readUntil(t, "peers")
	for _, p := range pf["peers"].([]any) {
		if m := p.(map[string]any); m["peerId"] == "tv-keyed" && m["trusted"] != true {
			t.Fatalf("keyed screen not marked trusted: %v", m)
		}
	}

	anon := ts.dialAnon(t, "anon-1", "", "")
	j := anon.readUntil(t, "joined")
	if peers, _ := j["peers"].([]any); len(peers) != 0 {
		t.Fatalf("untrusted display got peer ids: %v", peers)
	}

	// Its signals go nowhere, and it never gets a peers frame.
	anon.send(t, map[string]any{"t": "signal", "to": "op-1", "data": map[string]any{"description": map[string]any{"type": "offer", "sdp": "x"}}})
	op.quiet(t, "signal", 300*time.Millisecond)
	tv2 := ts.joinClient(t, "display", "tv-2") // trusted join → peers fanout
	defer tv2.close()
	tv2.readUntil(t, "joined")
	anon.quiet(t, "peers", 300*time.Millisecond)

	// Operators don't relay to it either.
	op.send(t, map[string]any{"t": "signal", "to": "anon-1", "data": map[string]any{"candidate": "x"}})
	anon.quiet(t, "signal", 300*time.Millisecond)

	// Hijack: an anonymous join reusing the keyed screen's id is refused
	// and the keyed screen stays connected.
	hj := ts.dialAnon(t, "tv-keyed", "", "")
	if m := hj.read(t); m["t"] != "err" {
		t.Fatalf("hijack join accepted: %v", m)
	}
	if live := ts.hub.ScreenSessions(ts.showID); live["Stage"] != 1 {
		t.Fatalf("keyed screen knocked off: %v", live)
	}
	// The real screen reconnecting with its key still replaces itself.
	re := ts.dialAnon(t, "tv-keyed", "Stage", key)
	if m := re.readUntil(t, "joined"); m["t"] != "joined" {
		t.Fatalf("keyed reconnect refused: %v", m)
	}
}

// Anonymous ?screen= names don't create Screens-page rows.
func TestAnonScreenNoRegistryRow(t *testing.T) {
	ts := newTestServer(t, false)
	c := ts.dialAnon(t, "x1", "Made Up", "")
	c.readUntil(t, "joined")
	if rows, _ := ts.db.ListScreens(ts.showID); len(rows) != 0 {
		t.Fatalf("anonymous join wrote the registry: %+v", rows)
	}
}

// E2E #12: audience/walk-in screens never receive the Presenter-only item;
// presenter screens and operators do.
func TestPresenterItemOnlyToPresenterScreens(t *testing.T) {
	ts := newTestServer(t, false)
	pres := &timerpi.PollView{ID: 9, Kind: "quiz", Question: "SECRET-Q", Options: []string{"a", "b"}, Correct: 1, ToPresenter: true}
	ts.hub.SetPollsFunc(func(int64) (timerpi.OnAir, error) { return timerpi.OnAir{Presenter: pres}, nil })
	join := func(name, kind string) *wsClient {
		if err := ts.db.SetScreenLook(ts.showID, name, kind, 0); err != nil {
			t.Fatal(err)
		}
		key, _ := ts.db.ScreenKey(ts.showID, name)
		c := ts.dialAnon(t, "p-"+kind, name, key)
		c.readUntil(t, "joined")
		return c
	}
	aud := join("Main", timerpi.ScreenAudience)
	dsm := join("DSM", timerpi.ScreenPresenter)

	ts.hub.BroadcastPoll(ts.showID)
	if p := aud.readUntil(t, "poll"); p["presenter"] != nil {
		t.Fatalf("audience screen got the presenter item: %v", p)
	}
	if p := dsm.readUntil(t, "poll"); p["presenter"] == nil {
		t.Fatalf("presenter screen lost the presenter item: %v", p)
	}
	eng, _ := ts.engines.Get(ts.showID)
	_ = eng.Notify()
	if s := aud.readUntil(t, "state"); s["snapshot"].(map[string]any)["presenter"] != nil {
		t.Fatalf("audience screen state carries the presenter item")
	}
	if s := dsm.readUntil(t, "state"); s["snapshot"].(map[string]any)["presenter"] == nil {
		t.Fatalf("presenter screen state lost the presenter item")
	}
	// Re-typing the screen live follows (screen-look push).
	ts.hub.SendToScreen(ts.showID, "Main", []byte(`{"t":"screen-look","kind":"presenter","rotation":0}`))
	ts.hub.BroadcastPoll(ts.showID)
	for p := aud.readUntil(t, "poll"); p["presenter"] == nil; p = aud.readUntil(t, "poll") {
	} // readUntil fails the test if it never comes
}

// E2E #6: per-cue commands address the cue by id. Operator Y deletes cue 1;
// operator X's stale view then edits "pos 2" by id (Talk, now at 1) — the
// edit lands on Talk, and an edit of the deleted id is refused.
func TestCueCommandsByID(t *testing.T) {
	ts := newTestServer(t, false)
	cues, _ := ts.db.ListCues(ts.showID)
	welcome, talk := cues[0], cues[1]
	x := ts.joinClient(t, "controls", "op-x")
	defer x.close()
	x.readUntil(t, "joined")

	x.send(t, map[string]any{"t": "cmd", "action": "cueDel", "args": map[string]any{"id": welcome.ID, "pos": 1}})
	x.readUntil(t, "state")
	x.send(t, map[string]any{"t": "cmd", "action": "cueEdit", "args": map[string]any{"id": talk.ID, "pos": 2, "label": "Talk (renamed)"}})
	x.readUntil(t, "state")
	got, _ := ts.db.ListCues(ts.showID)
	if len(got) != 1 || got[0].ID != talk.ID || got[0].Label != "Talk (renamed)" {
		t.Fatalf("edit by id hit the wrong cue: %+v", got)
	}
	x.send(t, map[string]any{"t": "cmd", "action": "cueEdit", "args": map[string]any{"id": welcome.ID, "pos": 1, "label": "ghost"}})
	if e := x.readUntil(t, "err"); !strings.Contains(fmt.Sprint(e["message"]), "no longer exists") {
		t.Fatalf("stale id err = %v", e)
	}
	x.send(t, map[string]any{"t": "cmd", "action": "cueDel", "args": map[string]any{"id": welcome.ID, "pos": 1}})
	x.readUntil(t, "err")
	if got, _ := ts.db.ListCues(ts.showID); len(got) != 1 || got[0].Label != "Talk (renamed)" {
		t.Fatalf("stale delete removed a cue: %+v", got)
	}
}

// A moderator (room session, not the Event Technician) can't rename the
// room over WS.
func TestRenameNeedsEventTechnician(t *testing.T) {
	ts := newTestServer(t, false)
	s := &session{hub: ts.hub, send: make(chan []byte, 8), done: make(chan struct{}), role: "controls", showID: ts.showID, id: "mod", trusted: true}
	ts.hub.command(s, "settings", []byte(`{"title":"Hacked"}`), func(error) {})
	if b := <-s.send; !strings.Contains(string(b), "Event Technician") {
		t.Fatalf("moderator rename: %s", b)
	}
	if sh, _ := ts.db.GetShow(ts.showID); sh.Title == "Hacked" {
		t.Fatal("moderator renamed the room")
	}
}
