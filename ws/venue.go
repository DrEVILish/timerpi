package ws

// venue.go — hooks for the cloud ↔ venue link (VENUE-CLOUD §6, STATUS
// N15/N16). The hub itself stays unaware of the link:
//
//   - air:     a venue's primary hears every on-air change and streams the
//     audience item to the cloud.
//   - tunnel:  the cloud hands an operator's or screen's socket for an event
//     running at a venue to the link right after its join frame.
//   - gate:    the cloud's copy of a venue event refuses commands while the
//     link isn't stable (the venue's copy wins; changes are paused).

import (
	"sync"

	"github.com/gorilla/websocket"

	"timerpi/timerpi"
)

// Tunnel decides whether a joined socket goes over the link; when it does,
// take is called with the socket (it owns conn afterwards), the raw join
// frame and the upgrade request's cookies.
type Tunnel func(showID int64, role string) (take func(cookies map[string]string, join []byte, conn *websocket.Conn), ok bool)

type venueHooks struct {
	mu     sync.Mutex
	air    func(showID int64, on timerpi.OnAir)
	tunnel Tunnel
	gate   func(showID int64) error
}

// SetAirHook is called with every computed on-air state (before fanout).
func (h *Hub) SetAirHook(fn func(showID int64, on timerpi.OnAir)) {
	h.venue.mu.Lock()
	h.venue.air = fn
	h.venue.mu.Unlock()
}

// SetTunnel installs the cloud's socket hand-off.
func (h *Hub) SetTunnel(fn Tunnel) {
	h.venue.mu.Lock()
	h.venue.tunnel = fn
	h.venue.mu.Unlock()
}

// SetCommandGate installs the cloud's command gate.
func (h *Hub) SetCommandGate(fn func(showID int64) error) {
	h.venue.mu.Lock()
	h.venue.gate = fn
	h.venue.mu.Unlock()
}

func (h *Hub) hooks() venueHooks {
	h.venue.mu.Lock()
	defer h.venue.mu.Unlock()
	return venueHooks{air: h.venue.air, tunnel: h.venue.tunnel, gate: h.venue.gate}
}

// tunnelled hands the session's socket to the link when the cloud's tunnel
// wants it. The writer stops without closing the socket first.
func (h *Hub) tunnelled(s *session) bool {
	tun := h.hooks().tunnel
	if tun == nil || s.role == "audience" {
		return false
	}
	take, ok := tun(s.showID, s.role)
	if !ok {
		return false
	}
	close(s.handoff)
	<-s.writerExit
	take(s.httpCookies, s.joinRaw, s.conn)
	return true
}
