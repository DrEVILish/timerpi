// CuTePi bridge e2e (PLAN §11.6, phase 6): a hand GO (and an auto-advanced
// cue) fire the outbound OSC packet to the paired peer; BLANK routes
// panic/unblank routes go; the settings test endpoint reports send status.
package routes_test

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"timerpi/oscbridge"
	"timerpi/timerpi"
)

func TestCuTePiOutboundEndToEnd(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "Welcome", DurationMS: 300_000}); err != nil {
		t.Fatalf("cue: %v", err)
	}
	// The peer: a local UDP listener standing in for CuTePi's QLab channel.
	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("peer listen: %v", err)
	}
	defer srv.Close()
	port := srv.LocalAddr().(*net.UDPAddr).Port

	// Wire exactly what main.go wires (engines.OnStart + oscbridge.Target).
	prev := oscbridge.Target
	oscbridge.Target = func() string { return fmt.Sprintf("127.0.0.1:%d", port) }
	defer func() { oscbridge.Target = prev }()
	ts.engines.OnStart = func(showID, pos int64) { oscbridge.FireOut("cue", pos) }

	// Hand GO → the peer hears the cue fire.
	if code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/cmd/go", nil, ""); code != 200 {
		t.Fatalf("go: %d %s", code, b)
	}
	got := readOSCPacket(t, srv)
	if got != "/cue/1/start" {
		t.Fatalf("peer heard %q, want /cue/1/start", got)
	}

	// BLANK → panic holding image; unblank → resume playout.
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/blank", []byte(`{"on":true}`), ""); code != 200 {
		t.Fatalf("blank: %d", code)
	}
	if got := readOSCPacket(t, srv); got != "/panic" {
		t.Fatalf("peer heard %q, want /panic", got)
	}
	if code, _ := ts.call("POST", "/api/shows/"+ts.showCode+"/blank", []byte(`{"on":false}`), ""); code != 200 {
		t.Fatalf("unblank: %d", code)
	}
	if got := readOSCPacket(t, srv); got != "/go" {
		t.Fatalf("peer heard %q, want /go", got)
	}
}

func TestOscTestEndpoint(t *testing.T) {
	ts := newAPITest(t)
	// Unconfigured: honest 400, not a silent success.
	if code, b := ts.call("POST", "/api/osc/test", nil, ""); code != 400 {
		t.Fatalf("unconfigured test: %d %s", code, b)
	}
	// Point it at a local listener; the packet must leave this host.
	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srv.Close()
	port := srv.LocalAddr().(*net.UDPAddr).Port
	if code, b := ts.call("POST", "/api/osc", []byte(
		fmt.Sprintf(`{"in":{"enabled":false},"out":{"enabled":true,"host":"127.0.0.1","port":"%d"}}`, port)), ""); code != 200 {
		t.Fatalf("save: %d %s", code, b)
	}
	if code, b := ts.call("POST", "/api/osc/test", nil, ""); code != 200 || !strings.Contains(string(b), `"sent"`) {
		t.Fatalf("test: %d %s", code, b)
	}
	buf := make([]byte, 512)
	srv.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := srv.ReadFrom(buf)
	if err != nil {
		t.Fatalf("no probe packet: %v", err)
	}
	m, perr := oscbridge.Parse(buf[:n])
	if perr != nil || m.Address != "/timerpi/test" {
		t.Fatalf("probe: %q err %v", m.Address, perr)
	}
}

func readOSCPacket(t *testing.T, srv net.PacketConn) string {
	t.Helper()
	buf := make([]byte, 512)
	srv.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := srv.ReadFrom(buf)
	if err != nil {
		t.Fatalf("peer packet: %v", err)
	}
	m, err := oscbridge.Parse(buf[:n])
	if err != nil {
		t.Fatalf("peer parse: %v", err)
	}
	return m.Address
}
