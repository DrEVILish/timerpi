// device.go — TimerPi device role state machine (PLAN §5).
//
// One appliance on the LAN holds authority: the FIRST device that is up
// becomes the primary; the others join it as members (displays). When the
// primary goes silent (>8 s without presence — TakeoverAfter), the next
// device promotes itself, harvests the last-known show state from the dead
// primary over plain HTTP (best-effort, trust model in docs/MESH.md), and
// peers re-join it normally once they see its TXT role flip.
//
// Authority selection is epoch-ordered: the epoch is the epoch-milliseconds
// at the moment a device first CLAIMED, persisted in SQLite
// (mesh_state.claimed_epoch), so it survives restarts. SMALLER epoch = came
// first = wins. Seeing a primary with a higher epoch than our claimed one →
// we KEEP primary; seeing one with a LOWER epoch → we yield to member. Two
// devices claiming simultaneously converge deterministically on the next
// poll (epoch-ms collisions are vanishingly unlikely and self-heal).
//
// Poll cycle: list peers every PollEvery (5 s default, 3 s browse window).
// Claim grace: a fresh idle device waits GraceWindow (10 s) before claiming
// so a just-restarting primary isn't outvoted by whichever peer ticked
// first after it went down for maintenance.
//
// All environment dependencies are interfaces so unit tests run on fakes
// (no network in CI): PeerLister, Announcer, HostSetter, SnapshotSource,
// Store. A skip-guarded loopback integration test exercises the real mdns
// stack (loopback announce/browse proven by mdns/mdns_test.go in this
// container).
package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"timerpi/mdns"
)

// Tunables (defaults; Options may override — tests always do).
const (
	DefaultPollEvery     = 5 * time.Second  // peer poll cadence (PLAN §5)
	DefaultBrowse        = 3 * time.Second  // mdns browse window per poll
	DefaultGraceWindow   = 10 * time.Second // idle → primary claim delay
	DefaultTakeoverAfter = 8 * time.Second  // primary silent longer than this → promote
	PeersFreshFor        = 30 * time.Second // "detected in the last 30 s" view window
	harvestTimeout       = 3 * time.Second  // takeover snapshot GET deadline
)

// Mesh TXT role vocabulary (mdns package contract):
//
//	primary — holds authority (serves the show state)
//	display — member/display joined to a primary
//	idle    — present, still deciding
//
// Peer roles are passed through verbatim ("unknown" included).

// State is the internal state-machine state; the mDNS-announced role derives
// from it via AnnounceRole.
type State string

const (
	StateIdle     State = "idle"     // starting up, inside the claim grace window
	StatePrimary  State = "primary"  // we hold authority
	StateMember   State = "member"   // joined another primary (announces "display")
	StateTakeover State = "takeover" // transient: promoting after primary loss
)

// AnnounceRole maps an internal state onto the mdns TXT role token.
func (s State) AnnounceRole() string {
	switch s {
	case StatePrimary, StateTakeover:
		return "primary"
	case StateMember:
		return "display"
	default:
		return "idle"
	}
}

// ---------------------------------------------------------------------------
// Interfaces (all faked in unit tests — no network, no disk, no host calls)

// PeerLister produces the current peer snapshot (production: mdns.FindPeers).
type PeerLister interface {
	ListPeers(ctx context.Context) ([]mdns.Peer, error)
}

// mdnsLister is the production adapter.
type mdnsLister struct{}

func (mdnsLister) ListPeers(ctx context.Context) ([]mdns.Peer, error) {
	// No hostname exclusion here: same-host instances must see each other
	// (drill rigs, containers). The sentinel can never match a real
	// hostname; true self-echoes are dropped by the mesh layer, whose
	// identity is (host, port).
	return mdns.FindPeersOnInterfaces(ctx, DefaultBrowse, nil, "\x00")
}

// Announcer registers our _timerpi._tcp service (production: mdns.Announce).
// collides() reports a name conflict (another host announcing our instance
// name); stop() tears the registration down with a goodbye packet.
type Announcer interface {
	Register(name string, port int, meta mdns.ServiceMeta) (collides func() bool, stop func())
}

type mdnsAnnouncer struct{}

func (mdnsAnnouncer) Register(name string, port int, meta mdns.ServiceMeta) (func() bool, func()) {
	return mdns.Announce(name, port, meta)
}

// HostSetter applies a hostname rename to the machine (production:
// hostnamectl, falling back to /etc/hostname + sethostname(2)).
type HostSetter interface {
	SetHost(name string) error
}

// SnapshotSource harvests the PROTOCOL snapshot from a peer over HTTP
// (production: GET /api/shows/:id). base looks like "http://192.168.1.20:80".
type SnapshotSource interface {
	Snapshot(ctx context.Context, base string, showID int64) (json.RawMessage, error)
}

// Event reports a role flip (integration hook: the ws hub re-broadcasts its
// peers frame so browsers/dashboards see the new authority, see
// NOTES-network.md).
type Event struct {
	Device string `json:"device"`
	From   State  `json:"from"`
	To     State  `json:"to"`
	Role   string `json:"role"` // announced role at the flip instant
}

// ---------------------------------------------------------------------------
// Public read models

// PeerView is one discovered peer for APIs and the settings page.
type PeerView struct {
	Host    string   `json:"host"`
	Addrs   []string `json:"addrs"`
	Port    int      `json:"port"`
	Role    string   `json:"role"`          // TXT role verbatim
	Ver     string   `json:"ver,omitempty"` // TXT ver
	Epoch   int64    `json:"epoch"`         // TXT epoch; 0 when missing/unparsable
	EpochOK bool     `json:"epochOK"`       // false = peer ships no usable epoch
	AgeS    int64    `json:"ageS"`          // seconds since last advertisement
}

// Identity is this device's mesh identity (GET /api/network core).
type Identity struct {
	Hostname   string            `json:"hostname"`
	DeviceName string            `json:"deviceName,omitempty"`
	Port       int               `json:"port"`
	Version    string            `json:"version"`
	State      State             `json:"state"`
	Role       string            `json:"role"`     // effective announced role
	Override   string            `json:"override"` // ""|auto|primary|member
	Epoch      int64             `json:"epoch"`    // our claimed epoch (0 = not claimed)
	TXT        map[string]string `json:"txt"`      // our TXT payload (k→v)
	// Primary* describe the last primary we saw ("" / 0 when none since
	// start). Takeover harvests from this endpoint.
	PrimaryHost   string `json:"primaryHost,omitempty"`
	PrimaryEpoch  int64  `json:"primaryEpoch,omitempty"`
	PrimaryAddr   string `json:"primaryAddr,omitempty"`
	LastPrimaryAt int64  `json:"lastPrimaryAt"` // epoch-ms; 0 = never seen
}

// Options configures a Device. Zero durations get the defaults above.
type Options struct {
	Hostname   string // "" → os.Hostname
	DeviceName string // operator-facing display name (informational)
	Port       int    // our HTTP+WS port
	Version    string // short app version (TXT ver)
	DBPath     string // "<data>/timerpi.db"; "" = persistence off (memory only)
	ShowID     int64  // show harvested on takeover (default 1 — docs/MESH.md)

	PollEvery     time.Duration
	Browse        time.Duration
	GraceWindow   time.Duration
	TakeoverAfter time.Duration

	Announcer     Announcer                                        // nil → mdns.Announce adapter
	Lister        PeerLister                                       // nil → mdns.FindPeers adapter
	Setter        HostSetter                                       // nil → hostnamectl fallback chain
	Source        SnapshotSource                                   // nil → HTTP GET adapter
	ApplySnapshot func(raw json.RawMessage, fromHost string) error // optional; nil = log only
	OnChange      func(Event)                                      // optional role-flip hook
	Now           func() time.Time                                 // nil → time.Now (tests inject)
}

func (o *Options) fill() {
	if o.PollEvery <= 0 {
		o.PollEvery = DefaultPollEvery
	}
	if o.Browse <= 0 {
		o.Browse = DefaultBrowse
	}
	if o.GraceWindow <= 0 {
		o.GraceWindow = DefaultGraceWindow
	}
	if o.TakeoverAfter <= 0 {
		o.TakeoverAfter = DefaultTakeoverAfter
	}
	if o.ShowID <= 0 {
		o.ShowID = 1
	}
	if o.Port <= 0 {
		o.Port = 80
	}
	if o.Version == "" {
		o.Version = "v1"
	}
	if o.Announcer == nil {
		o.Announcer = mdnsAnnouncer{}
	}
	if o.Lister == nil {
		o.Lister = mdnsLister{}
	}
	if o.Setter == nil {
		o.Setter = hostctlSetter{}
	}
	if o.Source == nil {
		o.Source = httpSnapshotSource{client: &http.Client{Timeout: harvestTimeout}}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Device runs the state machine. Safe for concurrent use by HTTP handlers
// and the poll goroutine.
type Device struct {
	opts Options
	logf func(format string, a ...any)

	store *Store // nil = persistence off

	mu              sync.Mutex
	hostname        string
	state           State
	override        string // "", "auto", "primary", "member"
	claimedEpoch    int64  // persisted authority epoch (0 = not claimed)
	startedAt       time.Time
	lastPrimarySeen time.Time
	lastPrimary     *mdns.Peer
	peers           []mdns.Peer
	peersAt         time.Time
	annCollides     func() bool
	annStop         func()
	effectiveRole   string // role token of the live announce ("" = none)
	conflictRename  int
	now             func() time.Time
}

// New builds a Device (NOT started, NOT announcing). Loads persisted state
// from opts.DBPath (claimed epoch, role override) when a store is available.
// Persistence problems degrade to an in-memory device with a loud log — the
// appliance must keep running without mesh persistence.
func New(opts Options) (*Device, error) {
	opts.fill()
	d := &Device{opts: opts, logf: func(f string, a ...any) { log.Printf("mesh: "+f, a...) }}
	d.now = opts.Now

	if hn := strings.TrimSpace(opts.Hostname); hn != "" {
		d.hostname = hn
	} else if h, err := os.Hostname(); err == nil && h != "" {
		d.hostname = h
	}

	if opts.DBPath != "" {
		if s, err := OpenStore(opts.DBPath); err == nil {
			d.store = s
		} else {
			d.logf("persistence off (open failed): %v", err)
		}
	}
	if d.store != nil {
		if ep, ok, err := d.store.GetInt64(KeyClaimedEpoch); err == nil && ok && ep > 0 {
			d.claimedEpoch = ep
		}
		if ov, _ := d.store.Get(KeyRoleOverride); ov != "" {
			switch ov {
			case OverridePrimary, OverrideMember, OverrideAuto:
				d.override = ov
			default:
				d.logf("ignoring stored override %q", ov)
			}
		}
	}

	d.startedAt = d.now()
	d.state = StateIdle
	return d, nil
}

// Close releases the store (main.go defer pattern).
func (dev *Device) Close() {
	dev.Stop()
	dev.mu.Lock()
	defer dev.mu.Unlock()
	if dev.store != nil {
		dev.store.Close()
		dev.store = nil
	}
}

// Start runs the poll loop until ctx is done. Returns immediately. The first
// tick evaluates immediately so announce starts without delay; a fresh
// device announces role "idle" right away (peers can see us during grace).
func (dev *Device) Start(ctx context.Context) {
	go dev.run(ctx)
}

// Stop tears the announce down (goodbye packet). Idempotent; also invoked
// when the run context ends.
func (dev *Device) Stop() {
	dev.mu.Lock()
	defer dev.mu.Unlock()
	dev.teardownAnnounceLocked()
}

func (dev *Device) run(ctx context.Context) {
	t := time.NewTicker(dev.opts.PollEvery)
	defer t.Stop()
	for {
		_ = dev.Tick(ctx)
		select {
		case <-ctx.Done():
			dev.Stop()
			return
		case <-t.C:
		}
	}
}

// Tick polls peers once and evaluates the state machine. Exposed for
// integration harnesses; run() calls it on the cadence.
func (dev *Device) Tick(ctx context.Context) error {
	lctx, cancel := context.WithTimeout(ctx, dev.opts.Browse)
	defer cancel()
	peers, err := dev.opts.Lister.ListPeers(lctx)
	if err != nil {
		peers = nil // degraded discovery: decide from what we remember
	}
	dev.Evaluate(ctx, peers)
	return nil
}

// Evaluate is one state-machine step over the given fresh peer list
// (exported for tests). Mutates state, announcements and persistence as
// needed and returns the resulting state.
func (dev *Device) Evaluate(ctx context.Context, peers []mdns.Peer) State {
	dev.mu.Lock()
	defer dev.mu.Unlock()

	peers = dev.dropSelfEchoLocked(peers)
	dev.peers = peers
	dev.peersAt = dev.now()

	// Track the most senior visible primary (lowest epoch wins).
	best := dev.bestPrimaryLocked()
	if best != nil {
		p := *best
		dev.lastPrimary = &p
		dev.lastPrimarySeen = dev.now()
	}

	from := dev.state
	switch dev.decideLocked() {
	case StateTakeover:
		// Compound step: observe loss → harvest snapshot → land as primary.
		dev.doTakeoverLocked()
		dev.transitionLocked(from, StatePrimary)
	case StateIdle:
		if from != StateIdle {
			dev.transitionLocked(from, StateIdle)
		}
	case StateMember:
		if from != StateMember {
			dev.transitionLocked(from, StateMember)
		}
	case StatePrimary:
		if from != StatePrimary {
			dev.transitionLocked(from, StatePrimary)
		}
	}
	// Bootstrap announce: peers must see us while we decide (role idle).
	if dev.effectiveRole == "" {
		dev.reannounceLocked()
	}
	return dev.state
}

// seniorThan reports whether (ep, host, port) holds authority over
// (ep2, host2, port2): the SMALLER epoch wins (came first); a tie breaks
// lexicographically on (host, port). Equal host + equal port = our own
// announce browsed back — nobody is senior to us. Same hostname with
// different ports is a REAL second device and compares by epoch.
func seniorThan(ep int64, host string, port int, ep2 int64, host2 string, port2 int) bool {
	if host == host2 && port == port2 {
		return false
	}
	if ep != ep2 {
		return ep < ep2
	}
	if host != host2 {
		return host < host2
	}
	return port < port2
}

// dropSelfEchoLocked removes our OWN registration browsed back: the mdns
// proxy publishes under our announce host (not the machine's hostname), so
// FindPeers cannot exclude it for us. Dropping by announce host means a
// real second device with the SAME hostname stays invisible to us — the
// hostname-collision corner case (open question in docs/MESH.md); the
// Announce collides() watcher is the safety net there.
func (dev *Device) dropSelfEchoLocked(peers []mdns.Peer) []mdns.Peer {
	out := peers[:0:0]
	for _, p := range peers {
		h, _, _, _ := peerMeta(p)
		// Identity is (host, port), not host alone: two instances may
		// share a hostname (containers, drill rigs), and our own
		// announcement always echoes back on our own port.
		if h == dev.hostname && p.Port == dev.opts.Port {
			continue
		}
		out = append(out, p)
	}
	return out
}

// decideLocked is the decision core (called under mu; pure w.r.t. peers).
func (dev *Device) decideLocked() State {
	now := dev.now()
	best := dev.bestPrimaryLocked()
	bestHost, _, bestEpoch, bestOK := peerMetaPointer(best)

	switch dev.override {
	case OverridePrimary:
		// Operator override: always authority. Split-brain risk documented
		// in docs/MESH.md (two forced primaries both announce; state
		// conflicts resolve by last-writer-wins updatedAt client-side).
		return StatePrimary
	case OverrideMember:
		// Forced member: never claim, never takeover; announce display.
		return StateMember
	}

	switch dev.state {
	case StateIdle:
		if best != nil && bestOK {
			// Someone else already holds authority. We keep primary only
			// when our own (persisted) claim is senior to their announce.
			if dev.claimedEpoch > 0 && seniorThan(dev.claimedEpoch, dev.hostname, dev.opts.Port, bestEpoch, bestHost, best.Port) {
				return StatePrimary // we came first; the other will yield
			}
			return StateMember
		}
		if now.Sub(dev.startedAt) >= dev.opts.GraceWindow {
			return StatePrimary // grace over, nobody claimed → claim
		}
		return StateIdle

	case StateMember:
		if best == nil {
			if dev.lastPrimarySeen.IsZero() {
				return StateMember // no primary ever seen by this runner
			}
			if now.Sub(dev.lastPrimarySeen) > dev.opts.TakeoverAfter {
				return StateTakeover // PLAN §5: primary stale > 8 s
			}
			return StateMember
		}
		return StateMember

	case StatePrimary, StateTakeover:
		if best != nil && bestOK && dev.claimedEpoch > 0 &&
			seniorThan(bestEpoch, bestHost, best.Port, dev.claimedEpoch, dev.hostname, dev.opts.Port) {
			// Split-brain heal: a device with a senior (older) claim
			// appeared. It came first — yield to it.
			return StateMember
		}
		return StatePrimary
	}
	return dev.state
}

// transitionLocked lands a state change: claim (when primary), re-announce
// (when the advertised role changed), fire the change hook.
func (dev *Device) transitionLocked(from, to State) {
	dev.state = to
	if to == StatePrimary {
		dev.claimLocked()
	}
	newRole := dev.roleLocked()
	if newRole != dev.effectiveRole {
		dev.reannounceLocked()
	}
	dev.fireLocked(from, to)
}

// doTakeoverLocked promotes self after primary loss: best-effort snapshot
// harvest from the last known primary address, then the caller lands us in
// StatePrimary. Harvest failure never blocks the promotion (the show starts
// empty; peers resume via normal WS once they see our role flip).
func (dev *Device) doTakeoverLocked() {
	p := dev.harvestTargetLocked()
	if p == nil {
		dev.logf("takeover: no harvestable primary address — promoting blind")
		return
	}
	host, role, ep, _ := peerMeta(*p)
	_ = role
	dev.claimLocked()

	ctx, cancel := context.WithTimeout(context.Background(), harvestTimeout)
	defer cancel()
	raw, err := dev.opts.Source.Snapshot(ctx, peerBaseURL(*p), dev.opts.ShowID)
	if err != nil {
		dev.logf("takeover: harvest show %d from %s (%s, epoch %d) failed: %v — promoting anyway",
			dev.opts.ShowID, host, peerBaseURL(*p), ep, err)
		return
	}
	dev.logf("takeover: harvested show %d from %s (%d bytes)", dev.opts.ShowID, host, len(raw))
	if dev.opts.ApplySnapshot != nil {
		if err := dev.opts.ApplySnapshot(raw, host); err != nil {
			dev.logf("takeover: applying harvested snapshot: %v", err)
		}
	} else {
		dev.logf("takeover: no ApplySnapshot wired — harvested snapshot dropped (integration: main.go may ingest it)")
	}
}

// claimLocked assigns (once) and persists our authority epoch: epoch-ms at
// the moment of first claim. Already-persisted epochs are kept — a restart
// must not re-enter a race it already won/lost.
func (dev *Device) claimLocked() {
	if dev.claimedEpoch > 0 {
		return
	}
	dev.claimedEpoch = dev.now().UnixMilli()
	if dev.store != nil {
		if err := dev.store.SetInt64(KeyClaimedEpoch, dev.claimedEpoch); err != nil {
			dev.logf("persist claimed epoch: %v", err)
		}
	}
}

// roleLocked is the effective announced role (override-aware view of state).
func (dev *Device) roleLocked() string {
	switch dev.override {
	case OverridePrimary:
		return "primary"
	case OverrideMember:
		return "display"
	}
	return dev.state.AnnounceRole()
}

// fireLocked emits the change hook (never blocks the state machine).
func (dev *Device) fireLocked(from, to State) {
	if dev.opts.OnChange != nil {
		go func(fromP, toP State, role string, device string) {
			dev.opts.OnChange(Event{Device: device, From: fromP, To: toP, Role: role})
		}(from, to, dev.roleLocked(), dev.hostname)
	}
}

// reannounceLocked stops the current registration and re-registers with the
// current identity. A name conflict (another host claiming our instance —
// collides()) triggers the one-shot -2 auto-rename.
func (dev *Device) reannounceLocked() {
	dev.teardownAnnounceLocked()
	meta := dev.announceMetaLocked()
	collides, stop := dev.opts.Announcer.Register(dev.hostname, dev.opts.Port, meta)
	dev.annCollides = collides
	dev.annStop = stop
	dev.effectiveRole = meta.Role
	dev.logf("announcing %q role=%s epoch=%d port=%d", dev.hostname, meta.Role, meta.Epoch, dev.opts.Port)
	if collides != nil && collides() {
		dev.resolveCollisionLocked()
	}
}

// resolveCollisionLocked renames our announce once (pi-stage → pi-stage-2)
// and re-registers. v1 walks exactly one level; deeper conflicts stay
// logged (docs/MESH.md open questions). conflictRename is reset by Rename()
// (the operator's explicit rename re-enters cleanly).
func (dev *Device) resolveCollisionLocked() {
	if dev.conflictRename >= 1 {
		dev.logf("name conflict persists for %s after rename — leaving as-is", dev.hostname)
		return
	}
	dev.conflictRename = 1
	dev.hostname = strings.TrimSuffix(dev.hostname, "-2") + "-2"
	dev.logf("name conflict — renamed announce to %q", dev.hostname)
	dev.reannounceLocked()
}

// teardownAnnounceLocked unregisters the current announce (goodbye).
func (dev *Device) teardownAnnounceLocked() {
	if dev.annStop != nil {
		dev.annStop()
		dev.annStop = nil
	}
	dev.annCollides = nil
	dev.effectiveRole = ""
}

// announceMetaLocked builds our current TXT payload.
func (dev *Device) announceMetaLocked() mdns.ServiceMeta {
	return mdns.ServiceMeta{
		Host:  dev.hostname,
		Role:  dev.roleLocked(),
		Ver:   dev.opts.Version,
		Epoch: dev.claimedEpoch,
	}
}

// Status returns the current identity and the fresh (last-30-s) peer view.
// Handlers (routes/network.go) render from these.
func (dev *Device) Status() (Identity, []PeerView) {
	dev.mu.Lock()
	defer dev.mu.Unlock()
	return dev.identityLocked(), dev.peersViewLocked()
}

// identityLocked fills the public identity view.
func (dev *Device) identityLocked() Identity {
	meta := dev.announceMetaLocked()
	id := Identity{
		Hostname:   dev.hostname,
		DeviceName: dev.opts.DeviceName,
		Port:       dev.opts.Port,
		Version:    dev.opts.Version,
		State:      dev.state,
		Role:       meta.Role,
		Override:   dev.override,
		Epoch:      dev.claimedEpoch,
		TXT:        mdns.DecodeTXT(meta.EncodeTXT()),
	}
	if p := dev.lastPrimary; p != nil {
		id.PrimaryHost = p.Host
		id.PrimaryEpoch = peerMetaInt(*p)
		id.PrimaryAddr = peerBaseURL(*p)
		if !dev.lastPrimarySeen.IsZero() {
			id.LastPrimaryAt = dev.lastPrimarySeen.UnixMilli()
		}
	}
	return id
}

// peersViewLocked maps cached peers into PeerViews, dropping entries not
// seen within the 30 s freshness window, host-sorted.
func (dev *Device) peersViewLocked() []PeerView {
	now := dev.now()
	cut := now.Add(-PeersFreshFor)
	out := make([]PeerView, 0, len(dev.peers))
	for _, p := range dev.peers {
		if p.LastSeen.Before(cut) {
			continue
		}
		host, role, ep, ok := peerMeta(p)
		if host == "" {
			continue
		}
		out = append(out, PeerView{
			Host:    host,
			Addrs:   p.Addrs,
			Port:    p.Port,
			Role:    role,
			Ver:     p.TXT["ver"],
			Epoch:   ep,
			EpochOK: ok,
			AgeS:    int64(now.Sub(p.LastSeen) / time.Second),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// bestPrimaryLocked returns the most senior visible primary (epoch-ordered,
// hostname tiebreak), or nil.
func (dev *Device) bestPrimaryLocked() *mdns.Peer {
	var best *mdns.Peer
	for i := range dev.peers {
		h, role, ep, ok := peerMeta(dev.peers[i])
		if !ok || role != "primary" {
			continue
		}
		if best == nil || seniorThan(ep, h, dev.peers[i].Port, peerMetaInt(*best), best.Host, best.Port) {
			p := dev.peers[i]
			best = &p
		}
	}
	return best
}

// harvestTargetLocked picks the takeover GET target: the currently visible
// senior primary, else the last one ever seen (must have addresses).
func (dev *Device) harvestTargetLocked() *mdns.Peer {
	if p := dev.bestPrimaryLocked(); p != nil && len(p.Addrs) > 0 {
		return p
	}
	if dev.lastPrimary != nil && len(dev.lastPrimary.Addrs) > 0 {
		return dev.lastPrimary
	}
	return nil
}

// ---------------------------------------------------------------------------
// Peer/TXT decoding helpers

// peerMetaInt returns only a peer's epoch (for comparisons).
func peerMetaInt(p mdns.Peer) int64 {
	_, _, ep, _ := peerMeta(p)
	return ep
}

// peerMetaPointer is peerMeta over a possibly-nil pointer.
func peerMetaPointer(p *mdns.Peer) (string, string, int64, bool) {
	if p == nil {
		return "", "", 0, false
	}
	return peerMeta(*p)
}

// peerMeta decodes a peer's identity from its TXT map (host/role/epoch).
func peerMeta(p mdns.Peer) (host, role string, epoch int64, ok bool) {
	host = strings.ToLower(strings.TrimSpace(p.TXT["host"]))
	if host == "" {
		host = p.Host
	}
	role = strings.TrimSpace(p.TXT["role"])
	ep, err := strconv.ParseInt(strings.TrimSpace(p.TXT["epoch"]), 10, 64)
	if err != nil {
		return host, role, 0, false
	}
	return host, role, ep, true
}

// peerBaseURL renders the peer's first addressable endpoint.
func peerBaseURL(p mdns.Peer) string {
	port := p.Port
	if port <= 0 {
		port = 80
	}
	if len(p.Addrs) == 0 {
		return fmt.Sprintf("http://%s:%d", strings.ToLower(p.Host), port)
	}
	return fmt.Sprintf("http://%s:%d", p.Addrs[0], port)
}

// ---------------------------------------------------------------------------
// Operator surface (driven by routes/network.go)

// Override values for SetOverride / mesh_state.role_override.
const (
	OverrideAuto    = "auto"
	OverridePrimary = "primary"
	OverrideMember  = "member"
)

// SetOverride persists and applies the forced role:
//
//	"" / "auto"    — the normal state machine
//	"primary"      — always announce primary (never yields; split-brain risk
//	                 documented in docs/MESH.md)
//	"member"       — never claim, never takeover; announce display
//
// The change applies against the LAST cached peer list immediately (no new
// network round trip — deterministic for tests and instant for the UI); the
// next poll re-checks against fresh peers regardless.
func (dev *Device) SetOverride(v string) error {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		v = OverrideAuto
	}
	switch v {
	case OverrideAuto, OverridePrimary, OverrideMember:
	default:
		return fmt.Errorf("override must be auto, primary or member (got %q)", v)
	}
	dev.mu.Lock()
	dev.override = v
	if dev.store != nil {
		if err := dev.store.Set(KeyRoleOverride, v); err != nil {
			dev.logf("persist override: %v", err)
		}
	}
	ps := append([]mdns.Peer(nil), dev.peers...)
	dev.mu.Unlock()
	dev.logf("role override → %s", v)
	go func() {
		dev.Evaluate(context.Background(), ps)
	}()
	return nil
}

// ValidHostname validates an RFC1123-style single label for appliance
// renames: 2–63 chars, letters/digits/hyphens, no leading/trailing hyphen.
// Rename normalizes to lowercase before validation.
func ValidHostname(name string) bool {
	if len(name) < 2 || len(name) > 63 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-':
			if i == 0 || i == len(name)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// SetHostSetter swaps the host-apply mechanism (tests inject a fake; never
// touch a real system hostname from a test).
func (dev *Device) SetHostSetter(s HostSetter) {
	dev.mu.Lock()
	dev.opts.Setter = s
	dev.mu.Unlock()
}

// SetVersion updates the TXT ver (re-announces verbatim; role unchanged).
// Available for binary-integration builds that learn their version late.
func (dev *Device) SetVersion(v string) {
	dev.mu.Lock()
	defer dev.mu.Unlock()
	dev.opts.Version = strings.TrimSpace(v)
	if dev.effectiveRole != "" {
		dev.reannounceLocked()
	}
}

// Rename validates + applies a hostname rename and re-announces under the
// new name (mDNS identity = hostname per PLAN §1, <newname>.local works
// immediately). Called by POST /api/network/hostname.
func (dev *Device) Rename(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidHostname(name) {
		return fmt.Errorf("hostname must be 2–63 chars: letters, digits, hyphens; no leading/trailing hyphen (RFC1123 label)")
	}
	dev.mu.Lock()
	s := dev.opts.Setter
	if s != nil {
		if err := s.SetHost(name); err != nil {
			dev.mu.Unlock()
			return fmt.Errorf("applying hostname: %w", err)
		}
	}
	dev.hostname = name
	dev.conflictRename = 0
	dev.reannounceLocked()
	dev.mu.Unlock()
	return nil
}

// hostnamectlSetter applies via systemd hostnamectl, falling back to
// /etc/hostname + sethostname(2) when hostnamectl is missing or fails
// (containers without systemd). NEVER exercised in unit tests (system-wide
// side effect); manual verification only.
type hostctlSetter struct{}

func (hostctlSetter) SetHost(name string) error {
	if _, err := exec.LookPath("hostnamectl"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "hostnamectl", "set-hostname", name).CombinedOutput()
		if err == nil {
			return nil
		}
		log.Printf("mesh: hostnamectl set-hostname failed (%v: %s), falling back", err, strings.TrimSpace(string(out)))
	}
	if err := os.WriteFile("/etc/hostname", []byte(name+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing /etc/hostname: %w", err)
	}
	return syscall.Sethostname([]byte(name))
}

// ---------------------------------------------------------------------------
// Snapshot sources

// httpSnapshotSource is the production SnapshotSource: plain
// GET <base>/api/shows/<id> → the PROTOCOL snapshot JSON.
type httpSnapshotSource struct {
	client *http.Client
}

func (s httpSnapshotSource) Snapshot(ctx context.Context, base string, showID int64) (json.RawMessage, error) {
	u := fmt.Sprintf("%s/api/shows/%d", strings.TrimSuffix(base, "/"), showID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}
