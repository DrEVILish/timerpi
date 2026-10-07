package venue

// link.go — the venue primary's link to the cloud (VENUE-CLOUD §6–§7,
// routes/link.go is the cloud's side). One outbound WebSocket: the box
// streams its event copy and the rooms' audience items up, and serves the
// requests and sockets the cloud tunnels down (phones' votes, and remote
// operators once the link is stable). Venue changes always win: the box
// never takes a copy back from the cloud. An event made here without
// internet is registered with the cloud once the cloud has answered
// steadily for routes.StableAfter (rule 6).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"timerpi/routes"
	"timerpi/timerpi"
)

// CopyEvery is how often the primary checks its event copy for changes.
var CopyEvery = 15 * time.Second

type linkState struct {
	mu       sync.Mutex
	conn     *websocket.Conn
	event    string
	wmu      sync.Mutex
	streams  map[int64]*websocket.Conn
	air      chan routes.LinkFrame // ordered, never blocks the hub (onAir)
	finalOK  chan struct{}
	lastCopy string          // hash of the last copy sent
	done     map[string]bool // events whose final copy reached the cloud
	cloudOK  time.Time       // the cloud has answered without a miss since
}

func (l *linkState) uploaded(event string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.done[event]
}

func (l *linkState) current() *websocket.Conn {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.conn
}

func (l *linkState) send(f routes.LinkFrame) error {
	c := l.current()
	if c == nil {
		return errors.New("no link")
	}
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	l.wmu.Lock()
	defer l.wmu.Unlock()
	_ = c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return c.WriteMessage(websocket.TextMessage, b)
}

// linkLoop keeps the link up while this box leads a paired event and a
// cloud is configured.
func (a *Agent) linkLoop(ctx context.Context) {
	if a.Hub != nil {
		a.Hub.SetAirHook(a.onAir)
	}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		p := a.Pairing()
		ev, have := a.Store.ResolveEvent(p.Event)
		want := p.Event != "" && have && ev.ReleasedAt == 0 && a.cloud() != "" && a.leading()
		if want && a.link.current() == nil {
			if err := a.dial(ctx, p, ev); err != nil {
				a.logf("venue: cloud link: %v", err)
			}
		}
		if !want {
			if c := a.link.current(); c != nil {
				_ = c.Close()
			}
		}
		select {
		case <-ctx.Done():
			if c := a.link.current(); c != nil {
				_ = c.Close()
			}
			return
		case <-t.C:
		}
	}
}

func wsURL(base string) string {
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://")
	}
	return base
}

func (a *Agent) dial(ctx context.Context, p Pairing, ev timerpi.Event) error {
	key, err := hex.DecodeString(p.MeshKey)
	if err != nil || len(key) == 0 {
		return errors.New("no mesh key")
	}
	hdr := http.Header{}
	hdr.Set(routes.LinkAuthHeader, routes.LinkAuth(key, "link", p.Event, time.Now()))
	u := wsURL(a.cloud()) + "/api/link?event=" + url.QueryEscape(p.Event)
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := d.DialContext(ctx, u, hdr)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return a.register(ctx, p, ev, key)
		}
		a.link.mu.Lock()
		a.link.cloudOK = time.Time{}
		a.link.mu.Unlock()
		return err
	}
	conn.SetReadLimit(32 << 20)
	a.link.mu.Lock()
	a.link.conn, a.link.event = conn, p.Event
	a.link.streams = map[int64]*websocket.Conn{}
	a.link.finalOK = make(chan struct{}, 1)
	a.link.lastCopy = ""
	air := make(chan routes.LinkFrame, 256)
	a.link.air = air
	a.link.mu.Unlock()
	a.logf("venue: cloud link up for event %s", p.Event)
	go func() { // air frames, in order
		for f := range air {
			if a.link.current() != conn {
				return
			}
			_ = a.link.send(f)
		}
	}()
	go a.linkRead(conn, p)
	go a.linkCopies(conn, ev.ID)
	for _, id := range a.roomIDs(ev.ID) { // the cloud learns what is on air now
		a.Hub.BroadcastPoll(id)
	}
	return nil
}

// register uploads an event the cloud doesn't know (made here offline),
// once the cloud has answered steadily for routes.StableAfter.
func (a *Agent) register(ctx context.Context, p Pairing, ev timerpi.Event, key []byte) error {
	a.link.mu.Lock()
	if a.link.cloudOK.IsZero() {
		a.link.cloudOK = time.Now()
	}
	since := a.link.cloudOK
	a.link.mu.Unlock()
	if time.Since(since) < routes.StableAfter {
		return fmt.Errorf("the cloud doesn't know event %s yet; registering once the link is stable", p.Event)
	}
	raw, err := a.Srv.ExportEvent(ev.ID)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"meshKey": p.MeshKey, "bundle": json.RawMessage(raw)})
	if !a.post(ctx, a.cloud()+"/api/link/register", body, map[string]string{routes.LinkAuthHeader: routes.LinkAuth(key, "register", p.Event, time.Now())}) {
		return errors.New("the cloud refused the event")
	}
	a.logf("venue: event %s registered with the cloud", p.Event)
	return nil
}

// linkCopies sends the event copy now and whenever it changes (every 15 s
// at most). ponytail: re-exports the whole event to compare; a change
// counter would be cheaper if events get large.
func (a *Agent) linkCopies(conn *websocket.Conn, eventID int64) {
	for {
		if a.link.current() != conn {
			return
		}
		if raw, err := a.Srv.ExportEvent(eventID); err == nil {
			sum := sha256.Sum256(stripStamp(raw))
			h := hex.EncodeToString(sum[:])
			a.link.mu.Lock()
			changed := h != a.link.lastCopy
			a.link.mu.Unlock()
			if changed && a.link.send(routes.LinkFrame{T: "bundle", Data: raw}) == nil {
				a.link.mu.Lock()
				a.link.lastCopy = h
				a.link.mu.Unlock()
			}
		}
		time.Sleep(CopyEvery)
	}
}

// stripStamp drops the export time so an unchanged event hashes the same.
func stripStamp(raw []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	delete(m, "exportedAt")
	b, _ := json.Marshal(m)
	return b
}

func (a *Agent) linkRead(conn *websocket.Conn, p Pairing) {
	defer func() {
		a.link.mu.Lock()
		if a.link.conn == conn {
			a.link.conn = nil
			close(a.link.air)
			a.link.air = nil
		}
		for _, s := range a.link.streams {
			_ = s.Close()
		}
		a.link.streams = map[int64]*websocket.Conn{}
		a.link.mu.Unlock()
		_ = conn.Close()
		a.logf("venue: cloud link down for event %s", p.Event)
	}()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var f routes.LinkFrame
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		switch f.T {
		case "req":
			go a.serveReq(p.Event, f)
		case "ws-open":
			go a.openStream(p.Event, f)
		case "ws":
			a.link.mu.Lock()
			s := a.link.streams[f.ID]
			a.link.mu.Unlock()
			if s != nil {
				_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
				_ = s.WriteMessage(websocket.TextMessage, []byte(f.Text))
			}
		case "ws-close":
			a.link.mu.Lock()
			s := a.link.streams[f.ID]
			delete(a.link.streams, f.ID)
			a.link.mu.Unlock()
			if s != nil {
				_ = s.Close()
			}
		case "final-ok":
			a.link.mu.Lock()
			ch := a.link.finalOK
			a.link.mu.Unlock()
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// serveReq runs a tunnelled request on this box's router.
func (a *Agent) serveReq(event string, f routes.LinkFrame) {
	res := routes.LinkFrame{T: "res", ID: f.ID}
	req, err := http.NewRequest(f.Method, fmt.Sprintf("http://127.0.0.1:%d%s", a.Port, f.Path), bytes.NewReader(f.Body))
	if err == nil {
		for k, v := range f.Header {
			req.Header.Set(k, v)
		}
		req.RemoteAddr = "127.0.0.1:0"
		req, err = a.Srv.LinkedRequest(req, event, f.As, f.Peer)
	}
	if err != nil {
		res.Status = http.StatusBadGateway
		res.Body = []byte(`{"ok":false}`)
	} else {
		rec := httptest.NewRecorder()
		a.Handler.ServeHTTP(rec, req)
		res.Status = rec.Code
		res.Header = map[string]string{}
		for k, v := range rec.Header() {
			if len(v) > 0 {
				res.Header[k] = v[0]
			}
		}
		res.Body = rec.Body.Bytes()
	}
	_ = a.link.send(res)
}

// openStream connects a tunnelled socket to this box's own hub.
func (a *Agent) openStream(event string, f routes.LinkFrame) {
	hdr := http.Header{}
	if ck := a.Srv.LinkedCookies(event, f.As); ck != "" {
		hdr.Set("Cookie", ck)
	}
	origin := fmt.Sprintf("http://127.0.0.1:%d", a.Port)
	hdr.Set("Origin", origin)
	local, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/ws", a.Port), hdr)
	if err != nil {
		_ = a.link.send(routes.LinkFrame{T: "ws-close", ID: f.ID})
		return
	}
	a.link.mu.Lock()
	a.link.streams[f.ID] = local
	a.link.mu.Unlock()
	if local.WriteMessage(websocket.TextMessage, []byte(f.Text)) != nil {
		_ = local.Close()
	}
	for {
		_, msg, err := local.ReadMessage()
		if err != nil {
			break
		}
		if a.link.send(routes.LinkFrame{T: "ws", ID: f.ID, Text: string(msg)}) != nil {
			break
		}
	}
	a.link.mu.Lock()
	_, open := a.link.streams[f.ID]
	delete(a.link.streams, f.ID)
	a.link.mu.Unlock()
	_ = local.Close()
	if open {
		_ = a.link.send(routes.LinkFrame{T: "ws-close", ID: f.ID})
	}
}

// onAir streams a room's audience item to the cloud (hub air hook).
func (a *Agent) onAir(showID int64, on timerpi.OnAir) {
	if a.link.current() == nil {
		return
	}
	sh, err := a.Store.GetShow(showID)
	if err != nil {
		return
	}
	a.link.mu.Lock()
	defer a.link.mu.Unlock()
	if a.link.air != nil {
		select {
		case a.link.air <- routes.LinkFrame{T: "air", Room: sh.Code, Poll: on.Audience}:
		default: // the cloud is far behind; the next change catches up
		}
	}
}

// sendFinal uploads the last copy at release and waits for the cloud.
func (a *Agent) sendFinal(ev timerpi.Event) error {
	if a.link.current() == nil {
		return errors.New("no link to the cloud")
	}
	raw, err := a.Srv.ExportEvent(ev.ID)
	if err != nil {
		return err
	}
	a.link.mu.Lock()
	ch := a.link.finalOK
	a.link.mu.Unlock()
	if err := a.link.send(routes.LinkFrame{T: "final", Data: raw}); err != nil {
		return err
	}
	select {
	case <-ch:
		a.link.mu.Lock()
		if a.link.done == nil {
			a.link.done = map[string]bool{}
		}
		a.link.done[ev.Code] = true
		a.link.mu.Unlock()
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("the cloud didn't confirm")
	}
}

func (a *Agent) roomIDs(eventID int64) []int64 {
	rooms, err := a.Store.ListRooms(eventID)
	if err != nil {
		return nil
	}
	ids := make([]int64, len(rooms))
	for i, r := range rooms {
		ids[i] = r.ID
	}
	return ids
}

// PeerURL picks a peer's base URL, IPv4 when it has one (link-local works
// without a router), else IPv6.
func PeerURL(addrs []string, port int) string {
	pick := ""
	for _, ad := range addrs {
		if ip := net.ParseIP(strings.TrimSpace(ad)); ip != nil {
			if ip.To4() != nil {
				pick = ip.String()
				break
			}
			if pick == "" {
				pick = "[" + ip.String() + "]"
			}
		}
	}
	if pick == "" {
		return ""
	}
	return "http://" + pick + ":" + strconv.Itoa(port)
}
