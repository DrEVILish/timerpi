package routes_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The operator screens-gallery page (GET /screens/:ident) renders the
// ftl-element shell the JS builds cards into, and the nav offers it.
func TestGalleryPageF(t *testing.T) {
	ts := newAPITest(t)
	code, body := ts.call("GET", "/screens/"+ts.showCode, nil, "")
	if code != 200 {
		t.Fatalf("gallery page: %d %.200s", code, body)
	}
	s := string(body)
	for _, sub := range []string{`id="tp-gal"`, `id="tp-waiting-list"`, `id="tp-screen-edit"`,
		`id="tp-screen-edit-frame"`, `href="/screens/`, `data-page="screens"`} {
		if !strings.Contains(s, sub) {
			t.Errorf("gallery missing %q", sub)
		}
	}
}

// Waiting-room round trip: TV paths pass AuthGate unauthenticated; the
// operator list requires identity when a password is set; capture assigns
// (consumed once, exactly) and dismiss removes.
func TestWaitingRoomFlow(t *testing.T) {
	ts := newAPITest(t)

	reg := func(name, host string) int {
		c, _ := ts.call("POST", "/api/waiting/register",
			[]byte(fmt.Sprintf(`{"name":%q,"host":%q}`, name, host)), "application/json")
		return c
	}
	// TV paths are AuthGate-exempt (a stage TV never logs in).
	if code := reg("Stage Left", "tv1.local"); code != 200 {
		t.Fatalf("register under auth gate: %d, want 200", code)
	}
	if code, _ := ts.call("GET", "/api/waiting/mine?name=Stage+Left&host=tv1.local", nil, ""); code != 200 {
		t.Fatalf("mine unauthed: %d, want 200", code)
	}
	// Operator paths stay gated.
	if code, _ := ts.anon("GET", "/api/waiting", nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("list without an operator session: %d, want 401", code)
	}

	// Authenticated operator flow via login (cookie reuse like the real UI).
	// Use HTTP Basic — same gate, simpler in tests.
	opGet := func(path string) (int, []byte) {
		req, _ := http.NewRequest("GET", ts.srv.URL+path, nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer res.Body.Close()
		var buf strings.Builder
		b := make([]byte, 4096)
		for {
			n, rerr := res.Body.Read(b)
			buf.Write(b[:n])
			if rerr != nil {
				break
			}
		}
		return res.StatusCode, []byte(buf.String())
	}

	// Basic auth covers operator login — list, capture, dismiss.
	code, listBody := opGet("/api/waiting")
	if code != 200 {
		t.Fatalf("authed list: %d %s", code, listBody)
	}
	var lst struct {
		Waiting []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Host string `json:"host"`
		} `json:"waiting"`
	}
	if err := json.Unmarshal(listBody, &lst); err != nil || len(lst.Waiting) != 1 {
		t.Fatalf("list = %s", listBody)
	}
	wid := lst.Waiting[0].ID

	// Capture into the harness show (its code is credential-gated-free by
	// definition here); unknown show code is refused.
	capture := func(body string) int {
		req, _ := http.NewRequest("POST", ts.srv.URL+fmt.Sprintf("/api/waiting/%d/capture", wid), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if code := capture(`{"code":"ZZZZZZZZ"}`); code != http.StatusNotFound {
		t.Errorf("capture unknown code: %d, want 404", code)
	}
	if code := capture(`{"code":"` + ts.showCode + `"}`); code != 200 {
		t.Fatalf("capture: %d", code)
	}

	// The waiting tab claims it ONCE.
	_, mine := ts.call("GET", "/api/waiting/mine?name=Stage+Left&host=tv1.local", nil, "")
	if !strings.Contains(string(mine), ts.showCode) {
		t.Fatalf("claim did not carry the code: %s", mine)
	}
	_, mine2 := ts.call("GET", "/api/waiting/mine?name=Stage+Left&host=tv1.local", nil, "")
	if !strings.Contains(string(mine2), `"assigned":""`) {
		t.Fatalf("claim not consumed: %s", mine2)
	}

	// Second display, then dismiss removes it from the list.
	_ = reg("Lobby", "tv2.local")
	_, lstMid := opGet("/api/waiting")
	var mid struct {
		Waiting []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"waiting"`
	}
	if err := json.Unmarshal(lstMid, &mid); err != nil {
		t.Fatalf("mid list: %v %s", err, lstMid)
	}
	lobbyID := int64(0)
	for _, wd := range mid.Waiting {
		if wd.Name == "Lobby" {
			lobbyID = wd.ID
		}
	}
	if lobbyID == 0 {
		t.Fatalf("Lobby never listed: %s", lstMid)
	}
	req, _ := http.NewRequest("DELETE", ts.srv.URL+fmt.Sprintf("/api/waiting/%d", lobbyID), nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if res.StatusCode != 200 {
		b := make([]byte, 200)
		n, _ := res.Body.Read(b)
		res.Body.Close()
		t.Fatalf("dismiss: status %d body %s", res.StatusCode, b[:n])
	}
	res.Body.Close()
	_, lst2 := opGet("/api/waiting")
	if strings.Contains(string(lst2), "Lobby") || !strings.Contains(string(lst2), "Stage Left") {
		t.Errorf("dismiss list wrong: %s", lst2)
	}
}

// Session lifecycle over the real hub: a named display is listed with its
// live peers, the kick endpoint disconnects exactly that peer (and the
// goodbye err frame makes the client stand down), and unknown peers 404.
func TestSessionKickFlow(t *testing.T) {
	ts := newAPITest(t)

	if code, _ := ts.call("DELETE", "/api/shows/"+ts.showCode+"/sessions/ghost-peer", nil, ""); code != http.StatusNotFound {
		t.Fatalf("kick unknown peer: %d, want 404", code)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	join, _ := json.Marshal(map[string]any{"v": 1, "t": "join", "role": "display",
		"show": ts.showCode, "peerId": "tv-peer", "joinedAt": 1000, "screen": "Stage Left"})
	if err := conn.WriteMessage(websocket.TextMessage, join); err != nil {
		t.Fatal(err)
	}
	// Drain until joined (peers fanout may interleave).
	for i := 0; i < 8; i++ {
		conn.SetReadDeadline(kickDeadline())
		var m map[string]any
		if err := conn.ReadJSON(&m); err != nil {
			t.Fatalf("read joined: %v", err)
		}
		if m["t"] == "joined" {
			break
		}
	}

	code, body := ts.call("GET", "/api/shows/"+ts.showCode+"/screens", nil, "")
	if code != 200 {
		t.Fatalf("screens: %d", code)
	}
	var out struct {
		Screens []struct {
			Name      string `json:"name"`
			Connected bool   `json:"connected"`
			Peers     []struct {
				PeerID string `json:"peerId"`
				Role   string `json:"role"`
			} `json:"peers"`
		} `json:"screens"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, s := range out.Screens {
		if s.Name == "Stage Left" {
			found = s.Connected && len(s.Peers) == 1 && s.Peers[0].PeerID == "tv-peer"
		}
	}
	if !found {
		t.Fatalf("live screen not listed with peers: %s", body)
	}

	if code, body := ts.call("DELETE", "/api/shows/"+ts.showCode+"/sessions/tv-peer", nil, ""); code != http.StatusOK {
		t.Fatalf("kick: %d %s", code, body)
	}
	// The kicked client receives the goodbye frame, then the close.
	conn.SetReadDeadline(kickDeadline())
	var m map[string]any
	for {
		if err := conn.ReadJSON(&m); err != nil {
			t.Fatalf("kicked client got nothing: %v", err)
		}
		if m["t"] == "err" {
			break
		}
	}
	if m["t"] != "err" || !strings.Contains(fmt.Sprint(m["message"]), "session deleted") {
		t.Fatalf("kicked client frame = %v", m)
	}
	// And it is gone from presence (unregister is async behind the close
	// write — poll briefly rather than sleep a constant).
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, body = ts.call("GET", "/api/shows/"+ts.showCode+"/screens", nil, "")
		if !strings.Contains(string(body), "tv-peer") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("kicked peer still present: %s", body)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func kickDeadline() time.Time { return time.Now().Add(5 * time.Second) }
