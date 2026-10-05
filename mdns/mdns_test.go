package mdns

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"
)

func TestServiceMetaEncodeDecodeTXT(t *testing.T) {
	meta := ServiceMeta{Host: "pi-stage", Role: "primary", Ver: "1.2.3", Epoch: 1700000000000}
	got := meta.EncodeTXT()

	// Fixed order keeps announces byte-identical across devices.
	want := []string{"host=pi-stage", "role=primary", "ver=1.2.3", "epoch=1700000000000"}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("EncodeTXT[%d] = %q, want %q", i, got[i], w)
		}
	}

	round, ok := MetaFromTXT(got)
	if !ok {
		t.Fatalf("MetaFromTXT: ok=false for %v", got)
	}
	if round != meta {
		t.Fatalf("roundtrip mismatch: got %v want %v", round, meta)
	}
}

func TestMetaFromTXTRejectsMissingEpoch(t *testing.T) {
	meta, ok := MetaFromTXT([]string{"host=solo", "role=idle"})
	if ok {
		t.Fatalf("expected !ok without epoch key, got %v", meta)
	}
	if meta.Host != "solo" || meta.Role != "idle" {
		t.Fatalf("partial meta lost: %v", meta)
	}
}

func TestDecodeTXTKeepsValuesAndParsesNonKVPairs(t *testing.T) {
	kv := DecodeTXT([]string{"role=display", "note=some free text", "bareflag"})
	if kv["role"] != "display" || kv["note"] != "some free text" {
		t.Fatalf("decode lost values: %v", kv)
	}
	if _, present := kv["bareflag"]; !present {
		t.Fatalf("non kv= line dropped: %v", kv)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"Pi-Studio.local.": "pi-studio",
		"pi-2.local":       "pi-2",
		"HOST":             "host",
		"plain":            "plain",
		"":                 "",
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Fatalf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func testEntry(instance, host string, addrs []string, text []string) *zeroconf.ServiceEntry {
	e := zeroconf.NewServiceEntry(instance, ServiceType, Domain)
	e.HostName = host
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip.To4() != nil {
			e.AddrIPv4 = append(e.AddrIPv4, ip)
		} else {
			e.AddrIPv6 = append(e.AddrIPv6, ip)
		}
	}
	e.Text = text
	e.Port = 80
	return e
}

func TestBuildPeersDedupesByHostAndExcludesSelf(t *testing.T) {
	txt := ServiceMeta{Host: "pi-a", Role: "display", Ver: "0.1", Epoch: 7}.EncodeTXT()
	entries := []*zeroconf.ServiceEntry{
		testEntry("pi-a", "pi-a.local.", []string{"192.168.1.20"}, append(append([]string{}, txt...), "extra=x")),
		testEntry("pi-a", "pi-a.local.", []string{"192.168.1.20", "fd00::20"}, append(append([]string{}, txt...), "extra=x")), // repeat sighting, same payload
		testEntry(selfHostname(), selfHostname()+".local.", []string{"127.0.0.1"}, txt),                                       // must be dropped
		testEntry("pi-b", "Pi-B.local.", []string{"192.168.1.23"}, []string{"host=pi-b", "epoch=9"}),
	}

	// "" excludes this container's hostname automatically.
	peers := buildPeers(entries, "")
	if len(peers) != 2 {
		t.Fatalf("want 2 peers (dedup pi-a, keep pi-b, drop self), got %d: %+v", len(peers), peers)
	}
	var piA *Peer
	for i := range peers {
		if peers[i].Host == "pi-a" {
			piA = &peers[i]
		}
	}
	if piA == nil {
		t.Fatalf("pi-a missing from results")
	}
	if piA.Port != 80 {
		t.Fatalf("pi-a port = %d, want 80", piA.Port)
	}
	if got := piA.Addrs; len(got) != 2 || got[0] != "192.168.1.20" || got[1] != "fd00::20" {
		t.Fatalf("pi-a addrs = %v, want deduped pair", got)
	}
	if piA.TXT["host"] != "pi-a" || piA.TXT["extra"] != "x" {
		t.Fatalf("pi-a TXT = %v", piA.TXT)
	}
	if piA.LastSeen.IsZero() {
		t.Fatalf("LastSeen not stamped")
	}

	// Explicit exclusion works with mixed case, matching normalizeHost.
	peers = buildPeers(entries, selfHostname())
	for _, p := range peers {
		if p.Host == selfHostname() {
			t.Fatalf("explicit exclude failed: host %q present", p.Host)
		}
	}
}

// selfHostname returns the container hostname ("" on error), so tests can
// anchor self-exclusion without juggling two return values.
func selfHostname() string {
	h, _ := os.Hostname()
	return h
}

func TestLookForCollision(t *testing.T) {
	self := "pi-stage"
	same := []*zeroconf.ServiceEntry{
		testEntry("pi-stage", "pi-stage.local.", []string{"192.168.1.5"}, []string{"host=pi-stage"}),
	}
	if lookForCollision(same, "pi-stage", self) {
		t.Fatal("own routine announce flagged as collision")
	}

	other := []*zeroconf.ServiceEntry{
		testEntry("pi-stage", "rogue-box.local.", []string{"192.168.1.6"}, []string{"host=rogue-box"}),
		testEntry("pi-stage", "pi-stage.local.", []string{"192.168.1.5"}, []string{"host=pi-stage"}),
	}
	if !lookForCollision(other, "pi-stage", self) {
		t.Fatal("same instance from another host not detected as collision")
	}

	otherInstance := []*zeroconf.ServiceEntry{
		testEntry("pi-b", "rogue-box.local.", []string{"192.168.1.6"}, nil),
	}
	if lookForCollision(otherInstance, "pi-stage", self) {
		t.Fatal("unrelated instance flagged as collision")
	}
}

func newLoopback(t *testing.T) *net.Interface {
	ifi, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skipf("no loopback interface in this environment: %v", err)
	}
	return ifi
}

// TestLoopbackAnnounceBrowse is the end-to-end check: announce on the
// loopback interface, browse it back. Multicast on loopback is not
// guaranteed in every container, so real packet movement is best-effort:
// a silent run skips rather than fails. The deterministic (TXT/dedupe)
// parts of the contract are covered by the tests above regardless.
func TestLoopbackAnnounceBrowse(t *testing.T) {
	lo := newLoopback(t)

	// Speed up the collision watcher so it exercises one probe cycle
	// inside the test window.
	origInterval, origListen := watchInterval, watchListen
	watchInterval, watchListen = 300*time.Millisecond, 1200*time.Millisecond
	defer func() { watchInterval, watchListen = origInterval, origListen }()

	port := 4591
	name := "timerpi-mdns-selftest"
	meta := ServiceMeta{Host: "selfloop", Role: "idle", Ver: "test", Epoch: 42}

	collides, stop := AnnounceOnInterfaces(name, port, meta, []net.Interface{*lo})
	defer stop()

	peers, err := FindPeersOnInterfaces(t.Context(), 4*time.Second, []net.Interface{*lo}, "unrelated-hostname")
	if err != nil {
		t.Fatalf("FindPeersOnInterfaces error: %v", err)
	}
	var found *Peer
	for i := range peers {
		if peers[i].Host == "selfloop" {
			found = &peers[i]
			break
		}
	}
	if found == nil {
		t.Skipf("loopback multicast unavailable in this container (no entries seen): %v peers", peers)
		return
	}
	if found.Port != port {
		t.Fatalf("selftest peer port = %d, want %d", found.Port, port)
	}
	if found.TXT["host"] != "selfloop" || found.TXT["epoch"] != "42" {
		t.Fatalf("selftest peer TXT = %v, want host/epoch keys", found.TXT)
	}

	// The watcher should have examined our own entries and found no
	// conflict with itself.
	time.Sleep(2 * watchInterval)
	if collides() {
		t.Fatal("collides() true on a quiet loopback")
	}
}

// --------------------------------------------------------------- edges ----

// TestBuildPeersSameHostTwoPorts: two instances sharing one hostname are
// DISTINCT peers (drill-rig regression). Host-only dedupe collapsed them
// into one flapping record; identity is (host, port).
func TestBuildPeersSameHostTwoPorts(t *testing.T) {
	txt1 := ServiceMeta{Host: "timerpi", Role: "primary", Ver: "v1", Epoch: 1000}.EncodeTXT()
	txt2 := ServiceMeta{Host: "timerpi", Role: "idle", Ver: "v1", Epoch: 0}.EncodeTXT()
	e1 := testEntry("timerpi", "timerpi-3.local.", []string{"192.168.1.10"}, txt1)
	e1.Port = 8080
	e2 := testEntry("timerpi", "timerpi-3.local.", []string{"192.168.1.10"}, txt2)
	e2.Port = 8090
	peers := buildPeers([]*zeroconf.ServiceEntry{e1, e2}, "\x00")
	if len(peers) != 2 {
		t.Fatalf("same-host instances deduped to %d peers, want 2", len(peers))
	}
	ports := map[int]bool{}
	for _, p := range peers {
		ports[p.Port] = true
	}
	if !ports[8080] || !ports[8090] {
		t.Fatalf("ports lost in dedupe: %v", ports)
	}
}

// TestBuildPeersAdversarialEntries: nil, empty-identity, excluded-host and
// TXT-less entries never crash nor leak the excluded host.
func TestBuildPeersAdversarialEntries(t *testing.T) {
	txt := ServiceMeta{Host: "timerpi-4", Role: "idle", Ver: "v1", Epoch: 0}.EncodeTXT()
	e4 := testEntry("timerpi", "timerpi-4.local.", []string{"192.168.1.11"}, txt)
	e4.Port = 99
	eExcl := testEntry("excl", "excl-4.local.", []string{"192.168.1.12"}, nil)
	eExcl.Port = 81
	peers := buildPeers([]*zeroconf.ServiceEntry{
		nil,
		testEntry("", "", []string{"192.168.1.13"}, nil), // no identity
		e4,
		eExcl,
	}, "excl-4")
	for _, p := range peers {
		if p.Host == "excl-4" {
			t.Fatalf("excluded host leaked: %+v", p)
		}
	}
	if len(peers) != 1 || peers[0].Host != "timerpi-4" || peers[0].Port != 99 {
		t.Fatalf("adversarial build = %+v", peers)
	}
}
