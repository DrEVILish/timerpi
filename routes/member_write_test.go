package routes_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"timerpi/timerpi"
)

func TestMemberRejectsReplicaRESTWrites(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Original", DurationMS: 60000}); err != nil {
		t.Fatal(err)
	}
	var writable atomic.Bool
	ts.deps.BoxWriteTarget = func() (string, bool) { return "http://primary.local:18080", writable.Load() }
	for _, tc := range []struct{ method, path, body string }{
		{"PUT", "/api/shows/" + ts.showCode + "/cues", `[{"label":"Lost edit","durationMS":60000}]`},
		{"POST", "/api/shows/" + ts.showCode + "/cmd/go", `{}`},
		{"POST", "/api/shows/" + ts.showCode + "/blank", `{"on":true}`},
		{"POST", "/api/shows/" + ts.showCode + "/messages", `{"text":"Lost message"}`},
		{"PATCH", "/api/events/" + ts.eventCode, `{"name":"Lost event name"}`},
		{"POST", "/api/audience/" + ts.showCode + "/vote", `{}`},
		{"POST", "/api/assets", `{}`},
	} {
		status, body := ts.call(tc.method, tc.path, []byte(tc.body), "application/json")
		if status != http.StatusConflict || !strings.Contains(string(body), `"primary":"http://primary.local:18080"`) {
			t.Errorf("%s %s: %d %s", tc.method, tc.path, status, body)
		}
	}
	cues, _ := ts.db.ListCues(ts.showID)
	if len(cues) != 1 || cues[0].Label != "Original" {
		t.Errorf("replica changed: %+v", cues)
	}
	e, _ := ts.engines.Get(ts.showID)
	if e.Runtime().Running {
		t.Error("member ran local GO")
	}
	show, _ := ts.db.GetShow(ts.showID)
	if show.Blanked {
		t.Error("member changed blackout")
	}
	ts.signInSuper()
	if status, body := ts.call("POST", "/api/box/login", []byte(`{"password":"`+testBoxPW+`"}`), "application/json"); status != 200 {
		t.Fatalf("box login refused: %d %s", status, body)
	}
	if code, _ := ts.call("GET", "/api/shows/"+ts.showCode, nil, ""); code != 200 {
		t.Errorf("read refused: %d", code)
	}
	if code, _ := ts.call("POST", "/api/waiting/register", []byte(`{"name":"Waiting"}`), "application/json"); code != 200 {
		t.Errorf("waiting registration refused: %d", code)
	}
	raw, err := ts.deps.ExportEvent(show.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.deps.ImportEvent(raw); err != nil {
		t.Fatalf("internal mirror blocked: %v", err)
	}
	writable.Store(true)
	if code, body := ts.call("POST", "/api/shows/"+ts.showCode+"/cmd/go", []byte(`{}`), "application/json"); code != 200 {
		t.Fatalf("promoted primary refused: %d %s", code, body)
	}
}

func TestMemberRejectsWebSocketCommands(t *testing.T) {
	ts := newAPITest(t)
	var writable atomic.Bool
	ts.deps.BoxWriteTarget = func() (string, bool) { return "http://primary.local:18080", writable.Load() }
	ts.deps.Hub.(interface{ SetCommandGate(func(int64) error) }).SetCommandGate(ts.deps.CommandGate)
	d := websocket.Dialer{Jar: http.DefaultClient.Jar, HandshakeTimeout: time.Second}
	c, _, err := d.Dial("ws"+strings.TrimPrefix(ts.srv.URL, "http")+"/ws", http.Header{"Origin": []string{ts.srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.WriteJSON(map[string]any{"v": 1, "t": "join", "role": "controls", "show": ts.showCode, "peerId": "member-operator", "joinedAt": 1}); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var frame map[string]json.RawMessage
	for {
		if err := c.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if string(frame["t"]) == `"joined"` {
			break
		}
	}
	c.WriteJSON(map[string]any{"t": "cmd", "action": "cueAdd", "args": map[string]any{"label": "Lost websocket edit", "durationMS": 60000}})
	for {
		if err := c.ReadJSON(&frame); err != nil {
			t.Fatalf("member did not reject WS write: %v", err)
		}
		if string(frame["t"]) == `"err"` {
			if !strings.Contains(string(frame["message"]), "primary.local") {
				t.Errorf("missing target: %s", frame["message"])
			}
			break
		}
	}
	cues, _ := ts.db.ListCues(ts.showID)
	if len(cues) != 0 {
		t.Errorf("WS changed member replica: %+v", cues)
	}
	writable.Store(true)
	if err := c.WriteJSON(map[string]any{"t": "cmd", "action": "cueAdd", "args": map[string]any{"label": "Primary edit", "durationMS": 60000}}); err != nil {
		t.Fatal(err)
	}
	for {
		if err := c.ReadJSON(&frame); err != nil {
			t.Fatalf("promoted socket did not resume writing: %v", err)
		}
		if string(frame["t"]) != `"state"` {
			continue
		}
		var snap struct {
			Cues []timerpi.Cue `json:"cues"`
		}
		if err := json.Unmarshal(frame["snapshot"], &snap); err != nil {
			t.Fatal(err)
		}
		if len(snap.Cues) == 1 && snap.Cues[0].Label == "Primary edit" {
			break
		}
	}
}

func TestElectionRejectsWritesWithoutInventingPrimary(t *testing.T) {
	ts := newAPITest(t)
	ts.deps.BoxWriteTarget = func() (string, bool) { return "", false }
	code, body := ts.call("POST", "/api/shows/"+ts.showCode+"/notes", []byte(`{"text":"Unowned change"}`), "application/json")
	if code != 409 || !strings.Contains(string(body), "election") {
		t.Fatalf("election write: %d %s", code, body)
	}
	show, _ := ts.db.GetShow(ts.showID)
	if show.Notes != "" {
		t.Fatal("election mutated replica")
	}
}
