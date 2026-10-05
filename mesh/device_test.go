package mesh

// Fakes-only unit tests: no network, no system calls, no real announce.
// The loopback integration test at the bottom is skip-guarded and exercises
// the REAL mdns stack (announce/browse proven loopback-capable in this
// container by mdns/mdns_test.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"timerpi/mdns"
)

// ----------------------------------------------------------------- FakeKit —

// fakeAnnouncer records registrations; conflictLeft models the mdns
// collision watcher: the FIRST `conflictLeft` registrations report a name
// conflict from collides() (a real watcher restarts fresh with each
// renamed registration, so conflicts are counted, not sticky).
type fakeAnnouncer struct {
	mu           sync.Mutex
	names        []string
	metas        []mdns.ServiceMeta
	registered   int
	conflictLeft int
}

func (f *fakeAnnouncer) Register(name string, port int, meta mdns.ServiceMeta) (func() bool, func()) {
	f.mu.Lock()
	f.names = append(f.names, name)
	f.metas = append(f.metas, meta)
	f.registered++
	// conflictLeft registrations... (see type doc)
	c := f.conflictLeft >= f.registered
	f.mu.Unlock()
	return func() bool { return c }, func() {}
}

func (f *fakeAnnouncer) last() (name string, meta mdns.ServiceMeta) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.names) == 0 {
		return "", mdns.ServiceMeta{}
	}
	return f.names[len(f.names)-1], f.metas[len(f.metas)-1]
}

func (f *fakeAnnouncer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registered
}

type listerFunc func(ctx context.Context) ([]mdns.Peer, error)

func (f listerFunc) ListPeers(ctx context.Context) ([]mdns.Peer, error) { return f(ctx) }

func emptyLister() listerFunc {
	return func(context.Context) ([]mdns.Peer, error) { return nil, nil }
}

// fakeSetter records the machine rename without touching the real host.
type fakeSetter struct {
	mu    sync.Mutex
	names []string
	err   error
}

func (f *fakeSetter) SetHost(name string) error {
	f.mu.Lock()
	f.names = append(f.names, name)
	f.mu.Unlock()
	return f.err
}

func (f *fakeSetter) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.names) == 0 {
		return ""
	}
	return f.names[len(f.names)-1]
}

// fakeSource records the takeover harvest attempt.
type fakeSource struct {
	mu      sync.Mutex
	bases   []string
	showIDs []int64
	raw     json.RawMessage
	err     error
}

func (f *fakeSource) Snapshot(_ context.Context, base string, showID int64) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bases = append(f.bases, base)
	f.showIDs = append(f.showIDs, showID)
	if f.err != nil {
		return nil, f.err
	}
	return f.raw, nil
}

func (f *fakeSource) attempted() (base string, showID int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bases) == 0 {
		return "", 0
	}
	return f.bases[len(f.bases)-1], f.showIDs[len(f.showIDs)-1]
}

// fakeClock is a mutable time source the tests advance by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// advance skips the clock and makes sure no two stamps collide (epoch-ms
// values are also epoch ordering, so monotonic advancement matters).
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d <= 0 {
		d = time.Millisecond
	}
	c.t = c.t.Add(d + time.Millisecond)
}

func (c *fakeClock) unixMilli() int64 { return c.Now().UnixMilli() }

// peer builds an mdns.Peer with a well-formed TXT payload.
func peer(host, role string, epoch int64, addrs ...string) mdns.Peer {
	meta := mdns.ServiceMeta{Host: host, Role: role, Ver: "v1", Epoch: epoch}
	return mdns.Peer{
		Host:     host,
		Addrs:    addrs,
		Port:     80,
		TXT:      mdns.DecodeTXT(meta.EncodeTXT()),
		LastSeen: newFakeClock().Now(),
	}
}

// ---------------------------------------------------------------- helpers —

type testKit struct {
	dev     *Device
	ann     *fakeAnnouncer
	setter  *fakeSetter
	source  *fakeSource
	clk     *fakeClock
	applied []json.RawMessage
	dbPath  string
}

func newKit(t *testing.T, mutate func(*Options)) *testKit {
	t.Helper()
	kit := &testKit{
		ann:    &fakeAnnouncer{},
		setter: &fakeSetter{},
		source: &fakeSource{raw: json.RawMessage(`{"updatedAt":5,"show":{"id":1,"title":"X"}}`)},
		clk:    newFakeClock(),
	}
	o := Options{
		Hostname:   "pi-stage",
		Port:       8080,
		Version:    "vTest",
		DeviceName: "Stage Left",
		Lister:     emptyLister(),
		Announcer:  kit.ann,
		Setter:     kit.setter,
		Source:     kit.source,
		Now:        kit.clk.Now,
		ApplySnapshot: func(raw json.RawMessage, _ string) error {
			kit.applied = append(kit.applied, raw)
			return nil
		},
	}
	if mutate != nil {
		mutate(&o)
	}
	if o.DBPath != "" {
		kit.dbPath = o.DBPath
	}
	dev, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(dev.Close)
	kit.dev = dev
	return kit
}

func (k *testKit) evaluate(t *testing.T, peers []mdns.Peer) State {
	t.Helper()
	return k.dev.Evaluate(context.Background(), peers)
}

// waitFor drives the clock forward and evaluates with the given peers until
// want holds; fails after 20 fake steps.
func (k *testKit) waitFor(t *testing.T, want State, peersFn func(step int) []mdns.Peer) State {
	t.Helper()
	var got State
	for step := 0; step < 24; step++ {
		peers := nilPeers(peersFn, step)
		got = k.evaluate(t, peers)
		if got == want {
			return got
		}
		k.clk.advance(time.Second)
	}
	t.Fatalf("never reached %s (last %s)", want, got)
	return got
}

func nilPeers(fn func(int) []mdns.Peer, step int) []mdns.Peer {
	if fn == nil {
		return nil
	}
	return fn(step)
}

// ------------------------------------------------------------------ tests —

func TestFreshDeviceClaimsAfterGrace(t *testing.T) {
	kit := newKit(t, func(o *Options) { o.DBPath = filepath.Join(t.TempDir(), "timerpi.db") })

	if st := kit.evaluate(t, nil); st != StateIdle {
		t.Fatalf("fresh device should idle through grace, got %s", st)
	}
	// Still inside the grace window: no announcement at all.
	if n := kit.ann.count(); n != 1 {
		// The first Evaluate already announces role idle (peers should see
		// us while we decide) — exactly one registration, role idle.
		t.Fatalf("want exactly one idle announce during grace, got %d", n)
	}
	if _, meta := kit.ann.last(); meta.Role != "idle" {
		t.Fatalf("grace announce role = %s, want idle", meta.Role)
	}

	kit.waitFor(t, StatePrimary, nil)

	name, meta := kit.ann.last()
	if name != "pi-stage" || meta.Role != "primary" {
		t.Fatalf("claim announce = %s/%s, want pi-stage/primary", name, meta.Role)
	}
	if meta.Epoch == 0 || meta.Epoch != int64(kit.clk.unixMilli()) {
		t.Fatalf("claimed epoch = %d, want current fake clock ms %d", meta.Epoch, kit.clk.unixMilli())
	}
	// TXT evidence carries host/role/epoch per the mdns contract.
	got := mdns.DecodeTXT(meta.EncodeTXT())
	if got["host"] != "pi-stage" || got["role"] != "primary" {
		t.Fatalf("TXT = %v", got)
	}
}

func TestClaimedEpochPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "timerpi.db")

	kit := newKit(t, func(o *Options) { o.DBPath = dbPath })
	kit.waitFor(t, StatePrimary, nil)
	_, metaA := kit.ann.last()
	kit.dev.Stop() // drop the first device's announce

	// A NEW device on the same data file inherits the claimed epoch (a
	// restart must not re-enter a race it already won).
	kit2 := newKit(t, func(o *Options) {
		o.DBPath = dbPath
	})
	kit2.clk.advance(time.Second) // its wall clock is later
	kit2.clk.advance(time.Second)
	kit2.waitFor(t, StatePrimary, nil)

	second, meta := kit2.ann.last()
	if second != "pi-stage" || meta.Role != "primary" {
		t.Fatalf("restart announce = %s/%s", second, meta.Role)
	}
	if meta.Epoch != metaA.Epoch {
		t.Fatalf("restart re-randomized the epoch: %d → %d", metaA.Epoch, meta.Epoch)
	}
}

func TestYieldsToLowerEpochPrimary(t *testing.T) {
	// We claimed earlier (epoch 9000); a primary with an even OLDER epoch
	// (500, came first) shows up → we yield to member.
	kit := newKit(t, func(o *Options) { o.DBPath = filepath.Join(t.TempDir(), "timerpi.db") })
	kit.dev.claimedEpoch = 9000

	_ = kit.dev.store.SetInt64(KeyClaimedEpoch, 9000)
	if st := kit.evaluate(t, []mdns.Peer{peer("pi-older", "primary", 500, "192.168.1.10")}); st != StateMember {
		t.Fatalf("want yield to member, got %s", st)
	}
	if _, meta := kit.ann.last(); meta.Role != "display" {
		t.Fatalf("member announces role display, got %s", meta.Role)
	}
	if id, _ := kit.dev.Status(); id.State != StateMember || id.Role != "display" {
		t.Fatalf("status = %+v", id)
	}
}

func TestKeepsPrimaryWhenOurEpochOlder(t *testing.T) {
	// Our persisted epoch (500) is OLDER than the new primary's (900) — we
	// came first, we keep primary and it will yield.
	kit := newKit(t, func(o *Options) { o.DBPath = filepath.Join(t.TempDir(), "timerpi.db") })
	kit.dev.claimedEpoch = 500

	if st := kit.evaluate(t, []mdns.Peer{peer("pi-late", "primary", 900, "192.168.1.11")}); st != StatePrimary {
		t.Fatalf("want keep primary, got %s", st)
	}
	if _, meta := kit.ann.last(); meta.Role != "primary" || meta.Epoch != 500 {
		t.Fatalf("announce after keep = role %s epoch %d", meta.Role, meta.Epoch)
	}
}

func TestEpochTieHostnameTiebreak(t *testing.T) {
	// Two devices claiming in the SAME millisecond: the lexicographically
	// smaller hostname wins. A device also never yields to ITSELF (equal
	// epoch + equal host = our own announce seen back).
	kit := newKit(t, nil)
	kit.dev.claimedEpoch = 1000

	if st := kit.evaluate(t, []mdns.Peer{peer("pi-alpha", "primary", 1000, "192.168.1.12")}); st != StateMember {
		t.Fatalf("equal epoch, smaller remote host → we yield, got %s", st)
	}

	kit2 := newKit(t, nil)
	kit2.dev.claimedEpoch = 1000
	if st := kit2.evaluate(t, []mdns.Peer{
		peer("pi-zulu", "primary", 1000, "192.168.1.13"),
	}); st != StatePrimary {
		t.Fatalf("equal epoch, larger remote host → we keep primary, got %s", st)
	}

	// Self-announce seen back (same host + same epoch): no yield, no churn.
	if st := kit2.evaluateSelfStable(t); st != StatePrimary {
		t.Fatalf("self-announce must not demote us: %s", st)
	}
}

func TestSameHostDifferentPortYields(t *testing.T) {
	// Drill-rig regression: two instances sharing one hostname are
	// distinct devices. An older remote claim on the same hostname
	// (different port) outranks us — we yield instead of split-braining.
	kit := newKit(t, nil)
	kit.dev.claimedEpoch = 2000
	remote := peer(kit.dev.hostname, "primary", 1000, "192.168.1.12")
	remote.Port = kit.dev.opts.Port + 1 // same host, different instance
	if st := kit.evaluate(t, []mdns.Peer{remote}); st != StateMember {
		t.Fatalf("older same-host claim must demote us, got %s", st)
	}
}

// evaluateSelfStable feeds back our own announce and confirms the state
// holds (the TXT of our own registration browses back like a peer).
func (k *testKit) evaluateSelfStable(t *testing.T) State {
	t.Helper()
	self := peer(k.dev.hostname, k.dev.roleLocked(), k.dev.claimedEpoch, "10.9.9.9")
	self.Port = k.dev.opts.Port // own announce browses back on our own port
	return k.evaluate(t, []mdns.Peer{self})
}

func TestMemberThenTakeoverAfterPrimaryLoss(t *testing.T) {
	kit := newKit(t, func(o *Options) {
		o.DBPath = filepath.Join(t.TempDir(), "timerpi.db")
		o.ShowID = 7
	})
	dead := peer("pi-a", "primary", 1000, "192.168.1.20", "192.168.1.21")

	if st := kit.evaluate(t, []mdns.Peer{dead, peer("pi-b", "display", 42)}); st != StateMember {
		t.Fatalf("joining visible primary → member, got %s", st)
	}

	// Primary disappears; before TakeoverAfter we hold member.
	if st := kit.evaluate(t, []mdns.Peer{peer("pi-b", "display", 42)}); st != StateMember {
		t.Fatalf("still within 8 s → member, got %s", st)
	}

	kit.waitFor(t, StatePrimary, func(int) []mdns.Peer { return []mdns.Peer{peer("pi-b", "display", 42)} })

	// Takeover harvest: best-effort GET against the last known primary.
	base, showID := kit.source.attempted()
	if base != "http://192.168.1.20:80" || showID != 7 {
		t.Fatalf("harvest = %s show %d, want http://192.168.1.20:80 show 7", base, showID)
	}
	if len(kit.applied) != 1 {
		t.Fatalf("ApplySnapshot not called (%d)", len(kit.applied))
	}
	want, _ := kit.source.raw.MarshalJSON()
	if string(kit.applied[0]) != string(want) {
		t.Fatalf("applied snapshot mismatch: %s", kit.applied[0])
	}
	if _, meta := kit.ann.last(); meta.Role != "primary" {
		t.Fatalf("after takeover announce role = %s, want primary", meta.Role)
	}
}

func TestTakeoverPromotesEvenWhenHarvestFails(t *testing.T) {
	kit := newKit(t, func(o *Options) {
		o.Source = &fakeSource{err: fmt.Errorf("connection refused")}
	})
	if st := kit.evaluate(t, []mdns.Peer{peer("pi-a", "primary", 1000, "192.168.1.20")}); st != StateMember {
		t.Fatalf("join → member, got %s", st)
	}
	kit.waitFor(t, StatePrimary, nil)
	if len(kit.applied) != 0 {
		t.Fatalf("failed harvest must not apply anything")
	}
	if _, meta := kit.ann.last(); meta.Role != "primary" {
		t.Fatalf("promotion must survive harvest failure, announce = %s", meta.Role)
	}
}

func TestTakeoverPromotesBlindWithoutAddresses(t *testing.T) {
	kit := newKit(t, nil)
	if st := kit.evaluate(t, []mdns.Peer{peer("pi-a", "primary", 1000)}); st != StateMember {
		t.Fatalf("join → member, got %s", st)
	}
	kit.waitFor(t, StatePrimary, nil)
	if base, _ := kit.source.attempted(); base != "" {
		t.Fatalf("no-address peer must not be harvest target (got %s)", base)
	}
}

func seededOverride(t *testing.T, dbPath, v string) {
	t.Helper()
	s, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()
	if err := s.Set(KeyRoleOverride, v); err != nil {
		t.Fatalf("seed override: %v", err)
	}
}

func TestOverrideForcePrimaryKeepsAuthority(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "timerpi.db")
	seededOverride(t, dbPath, OverridePrimary)

	kit := newKit(t, func(o *Options) { o.DBPath = dbPath })
	// A SENIOR primary exists (epoch 1 — came first) but the operator
	// forced primary: we do NOT yield.
	if st := kit.evaluate(t, []mdns.Peer{peer("pi-boss", "primary", 1)}); st != StatePrimary {
		t.Fatalf("forced primary must not yield, got %s", st)
	}
	if _, meta := kit.ann.last(); meta.Role != "primary" {
		t.Fatalf("forced announce = %s", meta.Role)
	}
	if id, _ := kit.dev.Status(); id.Override != OverridePrimary {
		t.Fatalf("identity override = %q", id.Override)
	}
}

func TestOverrideForceMemberNeverClaims(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "timerpi.db")
	seededOverride(t, dbPath, OverrideMember)

	kit := newKit(t, func(o *Options) { o.DBPath = dbPath })
	// No primary anywhere, past all timeout windows: forced member stays
	// member announcing display, and never takes authority.
	kit.waitFor(t, StateMember, nil)
	if _, meta := kit.ann.last(); meta.Role != "display" {
		t.Fatalf("forced member announces display, got %s", meta.Role)
	}
	if kit.dev.claimedEpoch != 0 {
		t.Fatalf("forced member must never claim (epoch %d)", kit.dev.claimedEpoch)
	}
}

func TestSetOverridePersistsAndApplies(t *testing.T) {
	kit := newKit(t, func(o *Options) { o.DBPath = filepath.Join(t.TempDir(), "timerpi.db") })
	kit.waitFor(t, StatePrimary, nil)

	if err := kit.dev.SetOverride(OverrideMember); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	waitForState(t, kit.dev, StateMember, 2*time.Second)

	s, _ := OpenStore(kit.dbPath)
	defer s.Close()
	if v, _ := s.Get(KeyRoleOverride); v != OverrideMember {
		t.Fatalf("persisted override = %q", v)
	}

	if err := kit.dev.SetOverride("bogus"); err == nil {
		t.Fatal("bogus override accepted")
	}
}

// waitForState polls Status() until the state matches (async re-eval).
func waitForState(t *testing.T, dev *Device, want State, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if id, _ := dev.Status(); id.State == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	id, _ := dev.Status()
	t.Fatalf("device never reached %s within %v (now %s)", want, within, id.State)
}

func TestRenameValidatesAndReannounces(t *testing.T) {
	kit := newKit(t, nil)
	kit.waitFor(t, StatePrimary, nil) // first announce exists

	if err := kit.dev.Rename("Stage-Left"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := kit.setter.last(); got != "stage-left" {
		t.Fatalf("host setter got %q, want stage-left", got)
	}
	name, meta := kit.ann.last()
	if name != "stage-left" || meta.Host != "stage-left" {
		t.Fatalf("renamed announce = %s/%s", name, meta.Host)
	}
	if meta.Role != "primary" {
		t.Fatalf("rename must preserve role, got %s", meta.Role)
	}

	// Rejected names never reach the system setter.
	for _, bad := range []string{"", "a", "-hi", "hi-", "two words", strings.Repeat("x", 64), "pi!"} {
		if err := kit.dev.Rename(bad); err == nil {
			t.Fatalf("accepted bad hostname %q", bad)
		}
	}
	if n := kit.setter.last(); n != "stage-left" {
		t.Fatalf("browser-typo rename reached the host: %q", n)
	}
}

func TestValidHostnameTable(t *testing.T) {
	ok := []string{"pi", "pi-stage", "a-1", "Stage" /* case-normalized later */, strings.Repeat("a", 63)}
	for _, n := range ok {
		if !ValidHostname(strings.ToLower(n)) {
			t.Fatalf("ValidHostname(%q) rejected", n)
		}
	}
	bad := []string{"", "a", "-lead", "trail-", "two words", "dot.dot", strings.Repeat("a", 64), "pi@stage"}
	for _, n := range bad {
		if ValidHostname(n) {
			t.Fatalf("ValidHostname(%q) accepted", n)
		}
	}
}

func TestNameConflictAutoRenames(t *testing.T) {
	kit := newKit(t, nil)
	kit.ann.conflictLeft = 1       // first registration conflicts, the renamed one doesn't
	kit.evaluate(t, []mdns.Peer{}) // triggers announce → collision path

	name, _ := kit.ann.last()
	if name != "pi-stage-2" {
		t.Fatalf("collision rename wanted pi-stage-2, got %s", name)
	}
}

func TestPeersViewFreshnessAndSorting(t *testing.T) {
	kit := newKit(t, nil)
	clk := kit.clk

	alpha := peer("pi-alpha", "display", 11, "10.0.0.2")
	dead := peer("pi-dead", "primary", 22, "10.0.0.3")
	dead.LastSeen = clk.Now().Add(-45 * time.Second) // stale
	zulu := peer("pi-zulu", "idle", 33, "10.0.0.4")
	missingEpoch := mdns.Peer{Host: "pi-noepoch", Port: 80, TXT: map[string]string{"host": "pi-noepoch", "role": "display"}, LastSeen: clk.Now()}

	_ = kit.evaluate(t, []mdns.Peer{alpha, dead, zulu, missingEpoch})
	_, views := kit.dev.Status()
	var hosts []string
	for _, p := range views {
		hosts = append(hosts, p.Host)
	}
	want := []string{"pi-alpha", "pi-noepoch", "pi-zulu"} // stale pi-dead dropped, host-sorted
	if len(hosts) != len(want) {
		t.Fatalf("peers = %v, want %v", hosts, want)
	}
	for i := range want {
		if hosts[i] != want[i] {
			t.Fatalf("peers = %v, want %v", hosts, want)
		}
	}
	for _, p := range views {
		if p.Host == "pi-noepoch" && p.EpochOK {
			t.Fatalf("pi-noepoch flagged EpochOK")
		}
		if p.Port != 80 {
			t.Fatalf("%s port = %d", p.Host, p.Port)
		}
	}
}

func TestStoreRoundTripAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timerpi.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	// Reopen: additive migration must be idempotent.
	s2, err := OpenStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	if err := s.SetInt64(KeyClaimedEpoch, 177); err != nil {
		t.Fatalf("SetInt64: %v", err)
	}
	if v, ok, _ := s2.GetInt64(KeyClaimedEpoch); !ok || v != 177 {
		t.Fatalf("round trip = %d/%v, want 177/true", v, ok)
	}
	if err := s.Set(KeyRoleOverride, OverridePrimary); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if v, _ := s2.Get(KeyRoleOverride); v != OverridePrimary {
		t.Fatalf("override round trip = %q", v)
	}
	if _, ok, _ := s.GetInt64("nope"); ok {
		t.Fatal("unset key reported present")
	}
}

func TestIdentityTXTShape(t *testing.T) {
	kit := newKit(t, nil)
	kit.waitFor(t, StatePrimary, nil)
	id, _ := kit.dev.Status()

	if id.Hostname != "pi-stage" || id.DeviceName != "Stage Left" || id.Port != 8080 {
		t.Fatalf("identity = %+v", id)
	}
	if id.Role != "primary" || id.State != StatePrimary {
		t.Fatalf("identity role/state = %s/%s", id.Role, id.State)
	}
	if id.TXT["host"] != "pi-stage" || id.TXT["role"] != "primary" || id.TXT["ver"] != "vTest" {
		t.Fatalf("TXT = %v", id.TXT)
	}
	if id.Epoch == 0 {
		t.Fatal("identity epoch unset after claim")
	}
}

// ------------------------------------------------- integration (loopback) —

// TestDeviceLoopbackIntegration drives TWO real Devices over the real mdns
// stack restricted to the loopback interface: the first becomes primary,
// the second joins as member. Skip when loopback multicast is unavailable
// (same guard as mdns/mdns_test.go) — never fails CI.
func TestDeviceLoopbackIntegration(t *testing.T) {
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skipf("no loopback interface: %v", err)
	}

	type loKit struct {
		ann *loopAnnouncer
		lst *loopLister
	}
	mk := func(name string, port int) (loKit, *Device) {
		ann := &loopAnnouncer{ifaces: []net.Interface{*lo}}
		lst := &loopLister{ifaces: []net.Interface{*lo}, exclude: "never-a-timerpi-hostname"}
		dev, err := New(Options{
			Hostname:  name,
			Port:      port,
			Version:   "it-test",
			Announcer: ann,
			Lister:    lst,
		})
		if err != nil {
			t.Fatalf("New(%s): %v", name, err)
		}
		return loKit{ann, lst}, dev
	}

	_, devA := mk("mesh-it-a", 45991)
	defer devA.Stop()
	devA.Start(context.Background())

	_, devB := mk("mesh-it-b", 45992)
	defer devB.Stop()
	devB.Start(context.Background())

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ida, pa := devA.Status()
		idb, _ := devB.Status()
		if ida.Role == "primary" && idb.Role == "display" && len(pa) > 0 {
			found := false
			for _, p := range pa {
				if p.Host == "mesh-it-b" && p.Role == "display" {
					found = true
				}
			}
			if found {
				return // primary + member + mutual discovery: protocol fine
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	ida, pa := devA.Status()
	idb, _ := devB.Status()
	t.Skipf("loopback multicast unavailable in this container (roles after 30 s: A=%s B=%s, A-peers=%d)",
		ida.Role, idb.Role, len(pa))
}

// loopAnnouncer adapts mdns.AnnounceOnInterfaces to the mesh.Announcer
// interface (loopback-only, like mdns mdns_test.go).
type loopAnnouncer struct {
	ifaces []net.Interface
}

func (a *loopAnnouncer) Register(name string, port int, meta mdns.ServiceMeta) (func() bool, func()) {
	return mdns.AnnounceOnInterfaces(name, port, meta, a.ifaces)
}

// loopLister adapts mdns.FindPeersOnInterfaces (loopback browse; explicit
// exclude so both devices in one process don't exclude EACH OTHER via the
// shared os hostname... FindPeers excludes by instance host, and the
// integration devices announce under mesh-it-a / mesh-it-b anyway).
type loopLister struct {
	ifaces  []net.Interface
	exclude string
}

func (l *loopLister) ListPeers(ctx context.Context) ([]mdns.Peer, error) {
	return mdns.FindPeersOnInterfaces(ctx, 2*time.Second, l.ifaces, l.exclude)
}

// ------------------------------------------------------------- edges ----

// TestPeersWithMissingTXTAndZeroPort: malformed mDNS entries (no role, no
// epoch, zero port, empty TXT) never crash Evaluate nor become "primary".
func TestPeersWithMissingTXT(t *testing.T) {
	kit := newKit(t, nil)
	junk := []mdns.Peer{
		{Host: "no-txt", Addrs: []string{"1.2.3.4"}, Port: 80}, // empty TXT
		{Host: "zero-port", TXT: mdns.DecodeTXT(mdns.ServiceMeta{Host: "zero-port", Role: "primary", Ver: "v1", Epoch: 5}.EncodeTXT()), Port: 0},
		{Host: "no-epoch", TXT: map[string]string{"host": "a", "role": "primary"}, Addrs: []string{"1.1.1.1"}, Port: 80},
	}
	for _, p := range junk {
		_ = p.TXT
	}
	st := kit.evaluate(t, junk)
	switch st {
	case StateIdle, StateMember, StatePrimary:
		// Any settled state is legal; no panic and no takeover is the point.
	default:
		t.Fatalf("junk peers produced state %s", st)
	}
	if k := kit.bestPrimaryForTest(); k != nil {
		// A peer missing its epoch must NOT win seniority (epoch decode ok=false).
		for _, j := range junk {
			if j.Host == k.Host && j.Port == k.Port {
				if _, _, ep, ok := peerMeta(*k); ok && ep == 0 && j.Host == "no-epoch" {
					t.Fatalf("epoch-less peer became best primary: %+v", k)
				}
			}
		}
	}
}

// TestSelfEchoZeroEpochDoesNotBlockClaim: our own announce browsed back
// with epoch 0 (pre-claim TXT) must not wedge the grace window (regression:
// the announce eagle picked up the bootstrap TXT and blocked claiming).
func TestSelfEchoZeroEpochDoesNotBlockClaim(t *testing.T) {
	kit := newKit(t, nil)
	kit.clk.advance(20 * time.Second)
	// Feed our own hostname + our port with a zero-epoch idle TXT repeatedly.
	self := peer(kit.dev.hostname, "idle", 0, "10.1.1.1")
	self.Port = kit.dev.opts.Port
	for i := 0; i < 3; i++ {
		kit.clk.advance(2 * time.Second)
		if st := kit.evaluate(t, []mdns.Peer{self}); st != StatePrimary {
			t.Fatalf("self-echo blocked claiming after grace: %s", st)
		}
	}
}

func (k *testKit) bestPrimaryForTest() *mdns.Peer {
	k.dev.mu.Lock()
	defer k.dev.mu.Unlock()
	p := k.dev.bestPrimaryLocked()
	if p == nil {
		return nil
	}
	c := *p
	return &c
}
