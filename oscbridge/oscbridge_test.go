// oscbridge tests — codec roundtrip (Build → Parse), malformed-packet
// refusal, the inbound verb map, the outbound address grammar, and one
// real UDP listener roundtrip so the dispatch/report paths stay honest.
package oscbridge

import (
	"net"
	"testing"
	"time"
)

func TestBuildParseRoundtrip(t *testing.T) {
	for _, m := range []Message{
		{Address: "/timerpi/AQ2D-7WKP/go"},
		{Address: "/timerpi/AQ2D-7WKP/start", Args: []any{int32(7)}},
		{Address: "/cue/12/start"},
		{Address: "/panic"},
		{Address: "/weird", Args: []any{"tag", int32(3), float32(0.5), true, false}},
	} {
		raw, err := Build(m.Address, m.Args...)
		if err != nil {
			t.Fatalf("build %s: %v", m.Address, err)
		}
		got, err := Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", m.Address, err)
		}
		if got.Address != m.Address {
			t.Errorf("address: want %q got %q", m.Address, got.Address)
		}
		if len(got.Args) != len(m.Args) {
			t.Fatalf("args: want %d got %d (%v)", len(m.Args), len(got.Args), got.Args)
		}
		for i, a := range m.Args {
			if a == got.Args[i] {
				continue
			}
			// int literals enter Build as int and leave Parse as int32.
			in, isInt := a.(int)
			if isInt {
				if n, ok := got.Args[i].(int32); ok && int64(n) == int64(in) {
					continue
				}
			}
			t.Errorf("arg %d: want %v (%T) got %v (%T)", i, a, a, got.Args[i], got.Args[i])
		}
	}
}

func TestParseRejects(t *testing.T) {
	for name, raw := range map[string][]byte{
		"bundle":    {0x23, 0x62, 0x75, 0x6e, 0x64}, // "#bund…"
		"no-comma":  []byte("/x/y"),
		"not-osc":   []byte("timerpi/blabla"),
		"bad-tag":   []byte("/x,y meh"),
		"truncated": []byte("/timerpi/a/g,o\x00\x00\x00\x00\x00\x00\x00"),
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestVerbMap(t *testing.T) {
	must := func(addr string, args []any, code, verb string, pos int64) {
		t.Helper()
		gotCode, gotVerb, gotPos := VerbMap(Message{Address: addr, Args: args})
		if gotCode != code || gotVerb != verb || gotPos != pos {
			t.Fatalf("%s: got (%q,%q,%d) want (%q,%q,%d)", addr, gotCode, gotVerb, gotPos, code, verb, pos)
		}
	}
	must("/timerpi/AQ2D-7WKP/go", nil, "AQ2D-7WKP", "go", 0)
	must("/timerpi/AQ2D-7WKP/Pause", nil, "AQ2D-7WKP", "pause", 0)
	must("/timerpi/AQ2D-7WKP/start", []any{int32(9)}, "AQ2D-7WKP", "start", 9)
	must("/cue/12/start", nil, "", "", 0)
	must("/qlab/other", nil, "", "", 0)
}

func TestOutAddress(t *testing.T) {
	if got := OutAddress("cue", 12); got != "/cue/12/start" {
		t.Errorf("cue: %q", got)
	}
	if got := OutAddress("panic", 0); got != "/panic" {
		t.Errorf("panic: %q", got)
	}
	if got := OutAddress("nonsense", 0); got != "" {
		t.Errorf("unknown kind: %q", got)
	}
}

func TestInboundListener(t *testing.T) {
	raw, err := Build("/timerpi/AQ2D-7WKP/go")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	bad := raw[:len(raw)-2] // truncated tag block → report path

	in := &Inbound{}
	got := make(chan Message, 2)
	broke := make(chan error, 2)
	if err := in.SetInbound("127.0.0.1:0", func(m Message) { got <- m },
		func(e error) { broke <- e }); err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer in.SetInbound("", nil, nil)
	addr, _ := in.conn.LocalAddr().(*net.UDPAddr)

	c, err := net.Dial("udp", addr.String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := c.Write(raw); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case m := <-got:
		if m.Address != "/timerpi/AQ2D-7WKP/go" {
			t.Errorf("dispatch: %q", m.Address)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no dispatch")
	}
	if _, err := c.Write(bad); err != nil {
		t.Fatalf("send bad: %v", err)
	}
	select {
	case <-broke:
	case <-time.After(2 * time.Second):
		t.Fatal("no error report")
	}
}

func TestFireOutDelivers(t *testing.T) {
	// A local UDP listener stands in for the CuTePi/QLab peer.
	srv, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer srv.Close()
	addr := srv.LocalAddr().String()

	oscbridge_Target_backup := Target
	Target = func() string { return addr }
	defer func() { Target = oscbridge_Target_backup }()

	FireOut("cue", 3)
	got := readPacket(t, srv)
	if got != "/cue/3/start" {
		t.Fatalf("cue fire: %q", got)
	}
	FireOut("panic", 0)
	if got := readPacket(t, srv); got != "/panic" {
		t.Fatalf("panic: %q", got)
	}
	FireOut("go", 0)
	if got := readPacket(t, srv); got != "/go" {
		t.Fatalf("go: %q", got)
	}
	// Disabled target: silence, no error.
	Target = func() string { return "" }
	FireOut("go", 0)
	Target = func() string { return addr }
	FireOut("nonsense", 0) // unknown kind: OutAddress "" → nothing sent
}

func readPacket(t *testing.T, srv net.PacketConn) string {
	t.Helper()
	buf := make([]byte, 512)
	srv.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := srv.ReadFrom(buf)
	if err != nil {
		return "" // timeout = nothing sent
	}
	m, err := Parse(buf[:n])
	if err != nil {
		t.Fatalf("peer parse: %v", err)
	}
	return m.Address
}

// BUGLOG RC1: a string arg whose padding runs past the packet end used to
// leave pos beyond len(raw), and the next 's' sliced out of range and
// panicked the listener goroutine (and the whole box with it).
func TestParseTruncatedStringPaddingNoPanic(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte("/a\x00\x00,ss\x00abcd\x00"),
		[]byte("/a\x00\x00,si\x00abcd\x00"),
		[]byte("/a\x00\x00,sf\x00ab"),
	} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("Parse(%q) = nil error, want truncation error", raw)
		}
	}
}

func TestHandlePacketRecoversDispatchPanic(t *testing.T) {
	raw, err := Build("/timerpi/X/go")
	if err != nil {
		t.Fatal(err)
	}
	var reported error
	handlePacket(raw, func(Message) { panic("boom") }, func(e error) { reported = e })
	if reported == nil {
		t.Fatal("dispatch panic was not reported")
	}
}
