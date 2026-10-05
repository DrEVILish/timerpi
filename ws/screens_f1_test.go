package ws

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// joinScreenClient dials + joins a display carrying a F1 screen name.
func (ts *testServer) joinScreenClient(t *testing.T, peerID, screen string) *wsClient {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	join := fmt.Sprintf(`{"v":1,"t":"join","role":"display","show":%q,"peerId":%q,"joinedAt":%d,"screen":%q}`,
		ts.showCode, peerID, 1000, screen)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(join)); err != nil {
		t.Fatalf("join write: %v", err)
	}
	return &wsClient{conn: conn}
}

// F1 plumbing over WS: a joining display registers its screen, receives
// the operator assignment pushed on join ({t:"display"} theme), appears in
// the controls' peers frame with the screen name, and targeted pushes
// (SendToScreen) reach exactly that screen's sessions.
func TestScreenJoinAndPushF1(t *testing.T) {
	ts := newTestServer(t, false)
	ctrl := ts.joinClient(t, "controls", "peer-ctrl")
	defer ctrl.close()
	ctrl.readUntil(t, "joined")

	// Operator assignment exists BEFORE the display (re)joins.
	if err := ts.db.SetScreenConfig(ts.showID, "Stage Left", "blue-future", 7); err != nil {
		t.Fatalf("SetScreenConfig: %v", err)
	}

	disp := ts.joinScreenClient(t, "peer-disp", "Stage Left")
	defer disp.close()
	disp.readUntil(t, "joined")

	// Join push: theme frame then the board assignment frame (session.go).
	tf := disp.readUntil(t, "display")
	if tf["theme"] != "blue-future" {
		t.Fatalf("join theme push = %v", tf)
	}
	bf := disp.readUntil(t, "screen-board")
	if int64(bf["boardId"].(float64)) != 7 {
		t.Fatalf("join board push = %v", bf)
	}

	// Registry upsert on join (persisted screen, survives the session).
	rows, err := ts.db.ListScreens(ts.showID)
	if err != nil || len(rows) != 1 || rows[0].Name != "Stage Left" {
		t.Fatalf("screens registry after join: %+v err=%v", rows, err)
	}

	// Controls' peers frame carries the screen name.
	pf := ctrl.readUntil(t, "peers")
	var screenOf func(string) string
	screenOf = func(peerID string) string {
		for _, p := range pf["peers"].([]any) {
			if m := p.(map[string]any); m["peerId"] == peerID {
				return fmt.Sprint(m["screen"])
			}
		}
		return ""
	}
	if screenOf("peer-disp") != "Stage Left" {
		t.Errorf("peers frame missing screen: %v", pf["peers"])
	}

	// Targeted push reaches the display, never the controls session.
	push := []byte(`{"t":"display","theme":"timerpi"}`)
	if n := ts.hub.SendToScreen(ts.showID, "Stage Left", push); n != 1 {
		t.Fatalf("SendToScreen reached %d sessions, want 1", n)
	}
	if got := disp.readUntil(t, "display"); got["theme"] != "timerpi" {
		t.Errorf("display push = %v", got)
	}
	ctrl.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := ctrl.conn.ReadMessage(); err == nil {
		t.Error("controls session received a screen-targeted push")
	}

	// ScreenSessions counts live presence per name.
	live := ts.hub.ScreenSessions(ts.showID)
	if live["Stage Left"] != 1 {
		t.Errorf("ScreenSessions = %v", live)
	}
}

// Owner bug report 2026-10-05: multiple display windows on ONE browser
// must appear as SEPARATE screen rows (never grouped) — each window joins
// with its own per-window sessionStorage name, and the registry + presence
// key on that name.
func TestSeparateWindowsSeparateRows(t *testing.T) {
	ts := newTestServer(t, false)
	a := ts.joinScreenClient(t, "tv-1", "Screen-AAAA")
	defer a.close()
	a.readUntil(t, "joined")
	b := ts.joinScreenClient(t, "tv-2", "Screen-BBBB")
	defer b.close()
	b.readUntil(t, "joined")

	rows, err := ts.db.ListScreens(ts.showID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("registry rows = %+v err=%v, want 2 separate", rows, err)
	}
	live := ts.hub.ScreenSessions(ts.showID)
	if live["Screen-AAAA"] != 1 || live["Screen-BBBB"] != 1 {
		t.Fatalf("presence grouped: %v", live)
	}
}
