// OSC bridge tests (test-gap round): the settings API round-trips and
// actually drives the UDP listener (enable → packet → engine verb; disable
// → silence), oscDispatch maps the inbound grammar onto engine verbs, the
// audience ask guard 429s bursts, and the poll list/delete/QR surface.
package routes_test

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"timerpi/oscbridge"
	"timerpi/timerpi"
)

func oscSet(t *testing.T, ts *apiTest, body string) {
	t.Helper()
	if code, b := ts.call("POST", "/api/osc", []byte(body), ""); code != 200 {
		t.Fatalf("osc set: %d %s", code, b)
	}
}

func oscFreePort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("grab port: %v", err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

func TestOscSettingsRoundtrip(t *testing.T) {
	ts := newAPITest(t)
	code, b := ts.call("GET", "/api/osc", nil, "")
	if code != 200 {
		t.Fatalf("osc get: %d %s", code, b)
	}
	port := oscFreePort(t)
	oscSet(t, ts, fmt.Sprintf(`{"in":{"enabled":true,"port":"%d"},"out":{"enabled":true,"host":"cuitepi.local","port":"53000"}}`, port))
	kv, err := ts.db.AllSettings()
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if kv["osc.in.enabled"] != "1" || kv["osc.in.port"] != fmt.Sprint(port) ||
		kv["osc.out.host"] != "cuitepi.local" || kv["osc.out.port"] != "53000" {
		t.Fatalf("settings not persisted: %v", kv)
	}
	// Bad port refused before any state change.
	if code, _ := ts.call("POST", "/api/osc", []byte(`{"in":{"enabled":true,"port":"0"}}`), ""); code != 400 {
		t.Errorf("bad inbound port accepted: %d", code)
	}
	// Disabled again: the listener must stop (oscSync "" swap).
	oscSet(t, ts, `{"in":{"enabled":false}}`)
	kv, _ = ts.db.AllSettings()
	if kv["osc.in.enabled"] == "1" {
		t.Fatal("disable did not persist")
	}
}

// The full inbound path: saved settings arm the listener, a wire packet
// lands in oscDispatch, and the engine verb really runs.
func TestOscInboundDrivesEngine(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Welcome", DurationMS: 300_000}); err != nil {
		t.Fatalf("cue: %v", err)
	}
	port := oscFreePort(t)
	oscSet(t, ts, fmt.Sprintf(`{"in":{"enabled":true,"port":"%d"}}`, port))

	send := func(addr string) {
		t.Helper()
		raw, err := oscbridge.Build(addr)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		c, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer c.Close()
		if _, err := c.Write(raw); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	running := func() bool {
		code, b := ts.call("GET", "/api/shows/"+ts.showCode, nil, "")
		if code != 200 {
			t.Fatalf("snapshot: %d %s", code, b)
		}
		var m struct {
			Runtime struct {
				Running bool `json:"running"`
			} `json:"runtime"`
			Show struct {
				Blanked bool `json:"blanked"`
			} `json:"show"`
		}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("snapshot body: %s", b)
		}
		return m.Runtime.Running
	}

	// /timerpi/<code>/go starts the first cue.
	send("/timerpi/" + ts.showCode + "/go")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !running() {
		time.Sleep(50 * time.Millisecond)
	}
	if !running() {
		t.Fatal("OSC go did not start the cue")
	}
	// blank lands as a store verb + shows in the snapshot.
	send("/timerpi/" + ts.showCode + "/blank")
	deadline = time.Now().Add(3 * time.Second)
	blanked := false
	for time.Now().Before(deadline) && !blanked {
		time.Sleep(50 * time.Millisecond)
		code, b := ts.call("GET", "/api/shows/"+ts.showCode, nil, "")
		if code == 200 && strings.Contains(string(b), `"blanked":true`) {
			blanked = true
		}
	}
	if !blanked {
		t.Fatal("OSC blank did not land")
	}
	// Unknown show code: no crash, no state change (and no 500 — the
	// listener just logs).
	send("/timerpi/ZZZZ-9999/go")
	// Unknown verb ignored the same way.
	send("/timerpi/" + ts.showCode + "/dance")

	oscSet(t, ts, `{"in":{"enabled":false}}`) // leave the listener OFF
}

func TestAudienceAskGuard(t *testing.T) {
	ts := newAPITest(t)
	qa := mustPollCreate(t, ts, `{"kind":"qa","question":"Ask"}`)
	if code, _ := ts.call("POST", fmt.Sprintf("/api/shows/%s/polls/%d/show", ts.showCode, qa), []byte(`{"target":"audience"}`), ""); code != 200 {
		t.Fatalf("show qa: %d", code)
	}
	ask := func() int {
		code, _ := ts.call("POST", "/api/audience/"+ts.showCode+"/ask",
			[]byte(`{"kind":"qa","text":"hello?","peer":"burst-1"}`), "")
		return code
	}
	if code := ask(); code != 200 {
		t.Fatalf("first ask: %d", code)
	}
	if code := ask(); code != 429 {
		t.Fatalf("burst ask: want 429, got %d", code)
	}
	// A DIFFERENT phone is not throttled by the first one's burst.
	code, _ := newPersona(ts).do("POST", "/api/audience/"+ts.showCode+"/ask", `{"kind":"qa","text":"from me"}`)
	if code != 200 {
		t.Fatalf("second peer throttled: %d", code)
	}
}

func TestPollListDeleteAndQR(t *testing.T) {
	ts := newAPITest(t)
	pid := mustPollCreate(t, ts, `{"kind":"poll","question":"Lunch?","options":["Pizza","Skyr"]}`)
	code, b := ts.call("GET", "/api/shows/"+ts.showCode+"/polls", nil, "")
	if code != 200 || !strings.Contains(string(b), "Lunch?") {
		t.Fatalf("poll list: %d %s", code, b)
	}
	// QR: the audience join URL renders a PNG.
	code, b = ts.call("GET", "/api/audience/"+ts.showCode+"/qr", nil, "")
	if code != 200 || len(b) < 100 || string(b[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("audience qr: %d %dB", code, len(b))
	}
	// Delete the poll; list is empty afterwards.
	if code, b := ts.call("DELETE", fmt.Sprintf("/api/shows/%s/polls/%d", ts.showCode, pid), nil, ""); code != 200 {
		t.Fatalf("poll delete: %d %s", code, b)
	}
	code, b = ts.call("GET", "/api/shows/"+ts.showCode+"/polls", nil, "")
	if code != 200 || strings.Contains(string(b), "Lunch?") {
		t.Fatalf("deleted poll still listed: %d %s", code, b)
	}
	// Deleting an unknown poll 404s, not 500.
	if code, _ := ts.call("DELETE", fmt.Sprintf("/api/shows/%s/polls/999", ts.showCode), nil, ""); code != 404 {
		t.Errorf("unknown poll delete: %d", code)
	}
}
