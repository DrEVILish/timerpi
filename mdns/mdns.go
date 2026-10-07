// Package mdns publishes and discovers TimerPi instances on the LAN with
// mDNS/DNS-SD (service type _timerpi._tcp).
//
// Contract (PLAN.md §5 — device mesh): every device announces its
// hostname as the service instance, TXT carries host/role/ver/epoch, and
// the first device that is up holds authority (`role: primary`).
// routes/network.go and the mesh agent build further on top of this
// package.
//
// The appliance must never crash over mDNS: containers and restricted
// hosts often lack multicast. Announce/FindPeers degrade to no-ops with a
// log line (RFC-style "best effort presence") instead of failing startup.
package mdns

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/grandcat/zeroconf"
)

// ServiceType is the DNS-SD type every TimerPi instance publishes.
const ServiceType = "_timerpi._tcp"

// Domain is the mDNS browsing domain ("local." on a LAN).
const Domain = "local."

// Text keys (fixed contract; PLAN.md §5).
const (
	txtHost  = "host"
	txtRole  = "role"
	txtVer   = "ver"
	txtEpoch = "epoch"
	txtBoot  = "boot"  // per-boot random id: tells our own echo from a same-named box
	txtProto = "proto" // protocol major (VENUE-CLOUD §9)
	txtEvent = "event" // paired box's event code (VENUE-CLOUD §5)
	txtSig   = "sig"   // announcement signature (mesh key)
)

// ServiceMeta is the message carried in the TXT record pair:
//
//	host  — machine hostname ("<hostname>.local" identity, PLAN.md §1)
//	role  — primary | display | idle (mesh role)
//	ver   — short application version
//	epoch — device "generation" (e.g. boot epoch ms); bump on re-announce
type ServiceMeta struct {
	Host  string
	Role  string
	Ver   string
	Epoch int64
	Boot  string // per-boot random id ("" = not sent)
	Proto int    // protocol major (0 = not sent; v2 boxes never send it)
	Event string // the event a paired box belongs to ("" = unpaired)
	Sig   string // HMAC over the announcement with the event mesh key
}

// String renders the meta for logs.
func (m ServiceMeta) String() string {
	return fmt.Sprintf("{host=%s role=%s ver=%s epoch=%d}", m.Host, m.Role, m.Ver, m.Epoch)
}

// EncodeTXT returns the zeroconf TXT payload ("k=v" strings) for the meta.
//
// The order is fixed (host, role, ver, epoch) so two callers render
// identical bytes for identical data — same-host integration tests and
// snapshot comparisons stay deterministic.
func (m ServiceMeta) EncodeTXT() []string {
	out := []string{
		txtHost + "=" + m.Host,
		txtRole + "=" + m.Role,
		txtVer + "=" + m.Ver,
		txtEpoch + "=" + strconv.FormatInt(m.Epoch, 10),
	}
	if m.Boot != "" {
		out = append(out, txtBoot+"="+m.Boot)
	}
	if m.Proto > 0 {
		out = append(out, txtProto+"="+strconv.Itoa(m.Proto))
	}
	if m.Event != "" {
		out = append(out, txtEvent+"="+m.Event, txtSig+"="+m.Sig)
	}
	return out
}

// DecodeTXT flattens a zeroconf TXT payload ("k=v" strings) into a map.
// Lines without "=" are anchored under the empty key and ignored by
// MetaFromTXT; values keep arbitrary text (spaces allowed, as mDNS does).
func DecodeTXT(lines []string) map[string]string {
	kv := make(map[string]string, len(lines))
	for _, l := range lines {
		if i := strings.IndexByte(l, '='); i >= 0 {
			kv[l[:i]] = l[i+1:]
		} else {
			kv[l] = ""
		}
	}
	return kv
}

// MetaFromTXT rebuilds the ServiceMeta encoded with EncodeTXT. ok is false
// when the payload lacks a parsable epoch key.
func MetaFromTXT(lines []string) (ServiceMeta, bool) {
	return MetaFromMap(DecodeTXT(lines))
}

// MetaFromMap is MetaFromTXT for an already decoded TXT map.
func MetaFromMap(kv map[string]string) (ServiceMeta, bool) {
	meta := ServiceMeta{
		Host: kv[txtHost],
		Role: kv[txtRole],
		Ver:  kv[txtVer],
		Boot: kv[txtBoot],
	}
	meta.Proto, _ = strconv.Atoi(kv[txtProto])
	meta.Event, meta.Sig = kv[txtEvent], kv[txtSig]
	epoch, err := strconv.ParseInt(kv[txtEpoch], 10, 64)
	if err != nil {
		return meta, false
	}
	meta.Epoch = epoch
	return meta, true
}

// Peer is one discovered TimerPi appliance on the LAN.
type Peer struct {
	// Host is the remote machine hostname, domain suffix stripped
	// ("<hostname>.local" → "hostname"). Dedup key.
	Host string
	// Addrs are the peer's interface addresses ("192.168.1.20", …).
	Addrs []string
	// Port is the TimerPi HTTP/WS port.
	Port int
	// TXT is the raw decoded TXT record (host/role/ver/epoch, …).
	TXT map[string]string
	// LastSeen is the most recent advertisement in this browse window.
	LastSeen time.Time
}

// Announce publishes name as a _timerpi._tcp instance on this machine.
//
// name is the service instance name (use the machine hostname per
// PLAN.md); port is the HTTP/WS port; meta holds the TXT payload.
//
// It never fails the caller: on an unavailable mDNS stack (container, no
// multicast capable interface) it logs the problem and returns no-op
// closures — collides reports false, stop does nothing.
//
// collides() reports whether another host on the LAN claims the same
// instance name (name conflict). zeroconf v1.0.0 does not surface probe
// conflicts, so a lightweight watcher browses the network and flags the
// case "same instance, different source hostname". Inspect it at your
// leisure (mesh agent: rename + re-announce when true).
//
// stop stops announcing (goodbye packet) and must be called on shutdown.
func Announce(name string, port int, meta ServiceMeta) (collides func() bool, stop func()) {
	return AnnounceOnInterfaces(name, port, meta, nil)
}

// AnnounceOnInterfaces is Announce restricted to the given interfaces
// (nil = zeroconf's multicast-capable-default; a loopback-only list is
// what the unit tests use). Same never-fail contract.
func AnnounceOnInterfaces(name string, port int, meta ServiceMeta, ifaces []net.Interface) (collides func() bool, stop func()) {
	noopCollides := func() bool { return false }
	noopStop := func() {}

	if name == "" {
		if h, err := os.Hostname(); err == nil {
			name = h
		}
	}
	if meta.Host == "" {
		if h, herr := os.Hostname(); herr == nil {
			meta.Host = h
		}
	}

	text := meta.EncodeTXT()

	ips, ok := localIPs(ifaces)
	if !ok || len(ips) == 0 {
		// No addressable interface = no multicast stack (typical in
		// openvz/docker-lite containers): degrade to a no-op announcer.
		log.Printf("mdns: no announce-capable interface (continuing without mDNS)")
		return noopCollides, noopStop
	}
	srv, err := zeroconf.RegisterProxy(name, ServiceType, Domain, port, sanitizeHostName(meta.Host), ips, text, ifaces)
	if err != nil {
		log.Printf("mdns: announcing %q rejected (continuing without mDNS): %v", name, err)
		return noopCollides, noopStop
	}
	log.Printf("mdns: announcing %q (%s) on port %d", name, ServiceType, port)

	self := normalizeHost(meta.Host)
	// Snapshot the watch knobs once: they are package globals the tests
	// retune via defer-restore, so the goroutine must not re-read them
	// after the test's restore may already have run (-race caught that).
	watchIv, watchLs := watchInterval, watchListen
	var collision atomic.Bool
	done := make(chan struct{})
	var once sync.Once

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Stop the in-flight browse window when stop() closes done.
		go func() {
			<-done
			cancel()
		}()

		ticker := time.NewTicker(watchIv)
		defer ticker.Stop()
		for {
			// First pass runs immediately, then every watchInterval:
			// announcements from a different host claiming our name.
			if entries := collectEntries(ctx, ifaces, watchLs); entries != nil {
				if lookForCollision(entries, name, self) && collision.CompareAndSwap(false, true) {
					log.Printf("mdns: NAME CONFLICT: %q is also announced by another host — consider renaming", name)
				}
			}
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	stopFn := func() {
		once.Do(func() {
			close(done)
			srv.Shutdown()
			log.Printf("mdns: stopped announcing %q", name)
		})
	}
	return collision.Load, stopFn
}

// Watch tuning; test hooks override these (@see mdns_test.go).
var (
	watchInterval = 15 * time.Second
	watchListen   = 2 * time.Second
)

// sanitizeHostName strips a FQDN decoration zeroconf would re-append
// (zeroconf v1.0.0 compares against the raw Domain suffix, so a passing
// "pi.local." would become "pi.local.local.").
func sanitizeHostName(hostname string) string {
	h := strings.Trim(strings.TrimSpace(hostname), ".")
	h = strings.TrimSuffix(h, ".local")
	return strings.TrimSuffix(h, ".")
}

// localIPs collects announce addresses: unicast addresses of the given
// interfaces (nil = every up multicast-capable one). Loopbacks are only
// included when they were explicitly picked (unit tests run on `lo`).
// ok is false when the environment has no usable interface at all.
func localIPs(ifaces []net.Interface) (ips []string, ok bool) {
	auto := len(ifaces) == 0
	if auto {
		list, err := net.Interfaces()
		if err != nil {
			return nil, false
		}
		for _, ifi := range list {
			if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 {
				continue
			}
			ifaces = append(ifaces, ifi)
		}
	}
	for i := range ifaces {
		addrs, err := ifaces[i].Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, isIPNet := a.(*net.IPNet)
			if !isIPNet || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			if auto && ipn.IP.IsLoopback() {
				continue
			}
			if ip := ipn.IP.String(); !slices.Contains(ips, ip) {
				ips = append(ips, ip)
			}
		}
	}
	return ips, true
}

// lookForCollision reports whether raw browse entries contain our
// instance name announced from a DIFFERENT source host (pure, testable
// core of the collision watcher).
func lookForCollision(entries []*zeroconf.ServiceEntry, instance, self string) bool {
	for _, e := range entries {
		if e == nil || e.Instance != instance {
			continue
		}
		if h := normalizeHost(e.HostName); h != "" && h != self {
			return true
		}
	}
	return false
}

// FindPeers browses the LAN for other TimerPi appliances for the given
// window and returns peers deduplicated by host, last seen sorted.
// Own-hostname entries are excluded. Errors (no mDNS stack) degrade to an
// empty list plus a log — an appliance without discovery keeps running.
func FindPeers(ctx context.Context, timeout time.Duration) ([]Peer, error) {
	return FindPeersOnInterfaces(ctx, timeout, nil, "")
}

// FindPeersOnInterfaces is FindPeers restricted to explicit interfaces
// (tests: loopback) and an explicit excluded hostname ("" = this host).
func FindPeersOnInterfaces(ctx context.Context, timeout time.Duration, ifaces []net.Interface, excludeHost string) ([]Peer, error) {
	entries := collectEntries(ctx, ifaces, timeout)
	return buildPeers(entries, excludeHost), nil
}

// collectEntries browses ServiceType for window and drains every entry
// zeroconf hands back before the deadline. It never blocks past window
// (zeroconf also closes the channel at ctx expiry; we belt-and-brace with
// our own drain cap). A nil result means "browse stack unusable".
func collectEntries(parent context.Context, ifaces []net.Interface, window time.Duration) []*zeroconf.ServiceEntry {
	if window <= 0 {
		window = watchListen
	}
	ctx, cancel := context.WithTimeout(parent, window)
	defer cancel()

	ch := make(chan *zeroconf.ServiceEntry, 256)
	var opts []zeroconf.ClientOption
	opts = append(opts, zeroconf.SelectIPTraffic(zeroconf.IPv4))
	if len(ifaces) > 0 {
		opts = append(opts, zeroconf.SelectIfaces(ifaces))
	}
	res, err := zeroconf.NewResolver(opts...)
	if err != nil {
		log.Printf("mdns: resolver unavailable (continuing without discovery): %v", err)
		return nil
	}
	if err := res.Browse(ctx, ServiceType, Domain, ch); err != nil {
		log.Printf("mdns: browse failed (continuing without discovery): %v", err)
		return nil
	}

	var mu sync.Mutex // out is appended by the drain goroutine while the
	// parent may already be snapshotting after ctx fires — synchronize it.
	var out []*zeroconf.ServiceEntry
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for e := range ch {
			mu.Lock()
			out = append(out, e)
			mu.Unlock()
		}
	}()
	select {
	case <-drained:
	case <-ctx.Done():
		// Deadline reached: leave residual entries behind; the bounded
		// channel makes the producer block, not crash.
	}
	mu.Lock()
	snapshot := append([]*zeroconf.ServiceEntry(nil), out...)
	mu.Unlock()
	return snapshot
}

// buildPeers dedupes raw entries into Peers (pure, testable).
func buildPeers(entries []*zeroconf.ServiceEntry, excludeHost string) []Peer {
	exclude := normalizeHost(excludeHost)
	if exclude == "" {
		if h, err := os.Hostname(); err == nil {
			exclude = normalizeHost(h)
		}
	}

	type key struct {
		host string
		port int // same hostname may run several instances (drill rigs,
		// containers); identity is (host, port), never host alone.
	}
	order := make([]key, 0, len(entries))
	byHost := make(map[key]*Peer, len(entries))
	now := time.Now()

	for _, e := range entries {
		if e == nil {
			continue
		}
		host := normalizeHost(e.HostName)
		if host == "" {
			host = normalizeHost(e.Instance)
		}
		if host == "" || host == exclude {
			continue
		}
		k := key{host, e.Port}
		p, ok := byHost[k]
		if !ok {
			p = &Peer{Host: host, Port: e.Port, TXT: DecodeTXT(e.Text), LastSeen: now}
			for _, ip := range e.AddrIPv4 {
				p.Addrs = append(p.Addrs, ip.String())
			}
			for _, ip := range e.AddrIPv6 {
				p.Addrs = append(p.Addrs, ip.String())
			}
			byHost[k] = p
			order = append(order, k)
		} else {
			p.LastSeen = now
			p.Port = e.Port
			p.TXT = DecodeTXT(e.Text)
			for _, ip := range slices.Concat(e.AddrIPv4, e.AddrIPv6) {
				if a := ip.String(); !slices.Contains(p.Addrs, a) {
					p.Addrs = append(p.Addrs, a)
				}
			}
		}
	}

	peers := make([]Peer, 0, len(order))
	for _, k := range order {
		sort.SliceStable(byHost[k].Addrs, func(i, j int) bool { return byHost[k].Addrs[i] < byHost[k].Addrs[j] })
		peers = append(peers, *byHost[k])
	}
	return peers
}

// normalizeHost lowercases and strips the mDNS domain/trailing dots:
// "Pi-Studio.local." → "pi-studio".
func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if strings.HasSuffix(h, ".") {
		h = strings.TrimSuffix(h, ".")
	}
	if strings.HasSuffix(h, ".local") {
		h = strings.TrimSuffix(h, ".local")
	}
	return strings.TrimSuffix(h, ".")
}

// AliasName is the name a venue's primary box answers to (VENUE-CLOUD §5):
// http://timerpi.local. No box may be named "timerpi" itself.
const AliasName = "timerpi"

// Alias publishes <name>.local for this machine's addresses (an HTTP
// service record carries the A/AAAA records). Never fails the caller: no
// multicast = a no-op stop.
func Alias(name string, port int) (stop func()) {
	ips, ok := localIPs(nil)
	if !ok || len(ips) == 0 {
		return func() {}
	}
	srv, err := zeroconf.RegisterProxy("TimerPi", "_http._tcp", Domain, port, name, ips, []string{"path=/"}, nil)
	if err != nil {
		log.Printf("mdns: alias %s.local rejected: %v", name, err)
		return func() {}
	}
	log.Printf("mdns: answering as %s.local", name)
	return srv.Shutdown
}
