// link.go — the cloud's side of the venue link (VENUE-CLOUD §6–§7, STATUS
// N15/N16), plus the event-copy endpoint boxes use among themselves.
//
// During a show the venue's primary box holds the event and keeps one
// outbound WebSocket to the cloud (nothing to open on the venue router):
//
//	venue → cloud  {t:"bundle", data}  the event copy (the venue's copy wins)
//	               {t:"air", room, poll} the room's audience item, live
//	               {t:"res", id, …}    answers to tunnelled requests
//	               {t:"ws"|"ws-close", sid, …} tunnelled sockets
//	               {t:"final", data}   release: last copy, the cloud is home again
//	cloud → venue  {t:"req", id, …}    phones' votes and questions (always);
//	               operators' requests (only on a stable link)
//	               {t:"ws-open"|"ws"|"ws-close", sid, …} operators' and screens' sockets
//
// Sync rules (owner, 2026-10-06): the venue's copy replaces the cloud's, so
// venue changes always win; the cloud never edits a venue event's copy
// itself. A remote Event Technician or moderator works on the venue through
// the tunnel, and only once the link has been up without a drop for
// StableAfter (rule 5); before that, and while the link is down, the cloud
// shows its copy read-only (rule 4: a flapping link only uploads). Phones
// keep voting whenever the link is up; with it down they see "audience
// paused".
//
//	GET  /api/link?event=CODE        venue primary (WebSocket; X-TimerPi-Auth)
//	POST /api/link/register          venue: an event made offline (rule 6)
//	GET  /api/link/bundle?event=CODE boxes: the event copy (X-TimerPi-Auth)
package routes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"timerpi/timerpi"
)

// StableAfter: a link up this long without a drop is stable (rule 5).
var StableAfter = 2 * time.Minute

// LinkAuthHeader carries "<unix seconds>.<hex HMAC>" with the event mesh key
// over "<purpose>|<event code>|<unix seconds>".
const LinkAuthHeader = "X-TimerPi-Auth"

// LinkAuth signs a link request (venue side).
func LinkAuth(key []byte, purpose, event string, now time.Time) string {
	ts := strconv.FormatInt(now.Unix(), 10)
	return ts + "." + linkSig(key, purpose, event, ts)
}

func linkSig(key []byte, purpose, event, ts string) string {
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "timerpi-link/1|%s|%s|%s", purpose, event, ts)
	return hex.EncodeToString(mac.Sum(nil))
}

// linkAuthOK checks a request signed with keyHex (5 minutes of clock skew).
func linkAuthOK(keyHex, purpose, event, header string) bool {
	key, err := hex.DecodeString(keyHex)
	if err != nil || len(key) == 0 {
		return false
	}
	ts, sig, ok := strings.Cut(header, ".")
	if !ok {
		return false
	}
	at, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || time.Since(time.Unix(at, 0)).Abs() > 5*time.Minute {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(linkSig(key, purpose, event, ts)))
}

// LinkFrame is one message on the link.
type LinkFrame struct {
	T      string            `json:"t"`
	ID     int64             `json:"id,omitempty"` // request id / stream id
	Room   string            `json:"room,omitempty"`
	Poll   *timerpi.PollView `json:"poll,omitempty"`
	Data   json.RawMessage   `json:"data,omitempty"` // event copy
	Method string            `json:"method,omitempty"`
	Path   string            `json:"path,omitempty"`
	Header map[string]string `json:"header,omitempty"`
	Body   []byte            `json:"body,omitempty"`
	Status int               `json:"status,omitempty"`
	As     string            `json:"as,omitempty"`   // who the cloud vouches for: "super" | "rooms:A,B"
	Peer   string            `json:"peer,omitempty"` // a phone's device id (audience)
	Text   string            `json:"text,omitempty"` // tunnelled socket frame
}

// ------------------------------------------------------------- registry --

type linkRegistry struct {
	mu      sync.Mutex
	byEvent map[int64]*venueLink
}

type venueLink struct {
	eventID int64
	conn    *websocket.Conn
	wmu     sync.Mutex
	since   time.Time
	done    chan struct{}

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan LinkFrame
	streams map[int64]*websocket.Conn
	air     map[int64]*timerpi.PollView // cloud room id → audience item
}

func (d *Deps) linkRegistry() *linkRegistry {
	d.linksOnce.Do(func() { d.links = &linkRegistry{byEvent: map[int64]*venueLink{}} })
	return d.links
}

func (d *Deps) linkFor(eventID int64) *venueLink {
	r := d.linkRegistry()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byEvent[eventID]
}

func (l *venueLink) stable() bool { return l != nil && time.Since(l.since) >= StableAfter }

func (l *venueLink) send(f LinkFrame) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	l.wmu.Lock()
	defer l.wmu.Unlock()
	_ = l.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return l.conn.WriteMessage(websocket.TextMessage, b)
}

// request sends a tunnelled request and waits for its answer.
func (l *venueLink) request(ctx context.Context, f LinkFrame) (LinkFrame, error) {
	l.mu.Lock()
	l.nextID++
	f.ID = l.nextID
	ch := make(chan LinkFrame, 1)
	l.pending[f.ID] = ch
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.pending, f.ID)
		l.mu.Unlock()
	}()
	if err := l.send(f); err != nil {
		return LinkFrame{}, err
	}
	select {
	case res := <-ch:
		return res, nil
	case <-l.done:
		return LinkFrame{}, errors.New("the venue link dropped")
	case <-ctx.Done():
		return LinkFrame{}, ctx.Err()
	}
}

// ------------------------------------------------------------ endpoints --

var linkUpgrader = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096,
	CheckOrigin: func(*http.Request) bool { return true }} // boxes, not browsers; signed

func registerLink(r gin.IRouter, d *Deps) {
	r.GET("/api/link/bundle", d.apiLinkBundle)
	r.POST("/api/link/register", d.apiLinkRegister)
	r.GET("/api/link", d.apiLink)
}

// GET /api/link/bundle?event=CODE — the event copy for a box of the event.
func (d *Deps) apiLinkBundle(c *gin.Context) {
	ev, ok := d.Store.ResolveEvent(c.Query("event"))
	if !ok || !linkAuthOK(ev.MeshKey, "bundle", ev.Code, c.GetHeader(LinkAuthHeader)) {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "not a box of this event"})
		return
	}
	raw, err := d.ExportEvent(ev.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

// POST /api/link/register {meshKey, bundle} — a venue uploads an event it
// made without internet (rule 6). Signed with the key it claims; an event
// the cloud already knows under another key is refused.
func (d *Deps) apiLinkRegister(c *gin.Context) {
	if !d.isCloud() {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "not the cloud"})
		return
	}
	var body struct {
		MeshKey string          `json:"meshKey"`
		Bundle  json.RawMessage `json:"bundle"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Bundle) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "send {meshKey, bundle}"})
		return
	}
	var head struct {
		Event struct {
			Code string `json:"code"`
		} `json:"event"`
	}
	_ = json.Unmarshal(body.Bundle, &head)
	code := timerpi.NormalizeCode(head.Event.Code)
	if !linkAuthOK(body.MeshKey, "register", code, c.GetHeader(LinkAuthHeader)) {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "bad signature"})
		return
	}
	if ev, ok := d.Store.ResolveEvent(code); ok && ev.MeshKey != "" && !hmac.Equal([]byte(ev.MeshKey), []byte(body.MeshKey)) {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "the cloud has another event with this code"})
		return
	}
	ev, err := d.ImportEvent(body.Bundle)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	_ = d.Store.SetEventMeshKey(ev.ID, body.MeshKey)
	_ = d.Store.SetEventHome(ev.ID, "venue")
	log.Printf("routes: venue registered event %s", ev.Code)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/link?event=CODE — the venue primary's WebSocket.
func (d *Deps) apiLink(c *gin.Context) {
	if !d.isCloud() {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "not the cloud"})
		return
	}
	ev, ok := d.Store.ResolveEvent(c.Query("event"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown event"})
		return
	}
	if !linkAuthOK(ev.MeshKey, "link", ev.Code, c.GetHeader(LinkAuthHeader)) {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "not this event's venue"})
		return
	}
	conn, err := linkUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	c.Abort()
	_ = conn.NetConn().SetDeadline(time.Time{})
	conn.SetReadLimit(64 << 20) // event copies carry the venue map
	l := &venueLink{eventID: ev.ID, conn: conn, since: time.Now(), done: make(chan struct{}),
		pending: map[int64]chan LinkFrame{}, streams: map[int64]*websocket.Conn{}, air: map[int64]*timerpi.PollView{}}
	reg := d.linkRegistry()
	reg.mu.Lock()
	old := reg.byEvent[ev.ID]
	reg.byEvent[ev.ID] = l
	reg.mu.Unlock()
	if old != nil {
		_ = old.conn.Close()
	}
	_ = d.Store.SetEventHome(ev.ID, "venue")
	log.Printf("routes: venue link up for event %s", ev.Code)
	d.linkRead(l)
	reg.mu.Lock()
	if reg.byEvent[ev.ID] == l {
		delete(reg.byEvent, ev.ID)
	}
	reg.mu.Unlock()
	close(l.done)
	l.mu.Lock()
	for _, s := range l.streams {
		_ = s.Close()
	}
	l.mu.Unlock()
	_ = conn.Close()
	log.Printf("routes: venue link down for event %s", ev.Code)
	d.broadcastEventAir(ev.ID) // phones: audience paused
}

// linkRead handles the venue's frames until the socket drops.
func (d *Deps) linkRead(l *venueLink) {
	l.conn.SetPongHandler(func(string) error { return l.conn.SetReadDeadline(time.Now().Add(75 * time.Second)) })
	_ = l.conn.SetReadDeadline(time.Now().Add(75 * time.Second))
	go func() { // keepalive
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-l.done:
				return
			case <-t.C:
				l.wmu.Lock()
				_ = l.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				l.wmu.Unlock()
			}
		}
	}()
	for {
		_, raw, err := l.conn.ReadMessage()
		if err != nil {
			return
		}
		var f LinkFrame
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		switch f.T {
		case "bundle", "final":
			ev, err := d.ImportEvent(f.Data)
			if err != nil {
				log.Printf("routes: venue copy: %v", err)
				continue
			}
			if f.T == "final" {
				// Release (end + 4 h): the cloud keeps the final copy and is
				// the event's home again.
				_ = d.Store.SetEventHome(ev.ID, "")
				_ = d.Store.MarkEventReleased(ev.ID, time.Now())
				_ = l.send(LinkFrame{T: "final-ok"})
				log.Printf("routes: event %s released by its venue; final copy stored", ev.Code)
			}
		case "air":
			if sh, ok := d.Store.ResolveShowInEvent(l.eventID, f.Room); ok {
				l.mu.Lock()
				l.air[sh.ID] = f.Poll
				l.mu.Unlock()
				if d.Hub != nil {
					d.Hub.BroadcastPoll(sh.ID)
				}
			}
		case "res":
			l.mu.Lock()
			ch := l.pending[f.ID]
			l.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		case "ws":
			l.mu.Lock()
			s := l.streams[f.ID]
			l.mu.Unlock()
			if s != nil {
				_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if s.WriteMessage(websocket.TextMessage, []byte(f.Text)) != nil {
					_ = s.Close()
				}
			}
		case "ws-close":
			l.mu.Lock()
			s := l.streams[f.ID]
			delete(l.streams, f.ID)
			l.mu.Unlock()
			if s != nil {
				_ = s.Close()
			}
		}
	}
}

// broadcastEventAir re-sends every room's audience frame (the link changed).
func (d *Deps) broadcastEventAir(eventID int64) {
	if d.Hub == nil {
		return
	}
	for _, id := range d.eventRoomIDs(eventID) {
		d.Hub.BroadcastPoll(id)
	}
}

// ------------------------------------------------- cloud: venue events --

// venueEventOf returns the event of a room when it runs at a venue.
func (d *Deps) venueEventOf(showID int64) (timerpi.Event, bool) {
	if !d.isCloud() || d.Store == nil {
		return timerpi.Event{}, false
	}
	sh, err := d.Store.GetShow(showID)
	if err != nil {
		return timerpi.Event{}, false
	}
	ev, err := d.Store.GetEvent(sh.EventID)
	if err != nil || ev.Home != "venue" {
		return timerpi.Event{}, false
	}
	return ev, true
}

// WrapPolls is the cloud's on-air reader: a venue event's audience item is
// whatever its venue last sent; with the link down phones see "paused".
func (d *Deps) WrapPolls(base func(int64) (timerpi.OnAir, error)) func(int64) (timerpi.OnAir, error) {
	return func(showID int64) (timerpi.OnAir, error) {
		ev, ok := d.venueEventOf(showID)
		if !ok {
			return base(showID)
		}
		l := d.linkFor(ev.ID)
		if l == nil {
			return timerpi.OnAir{Paused: true}, nil
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		return timerpi.OnAir{Audience: l.air[showID]}, nil
	}
}

// CommandGate refuses commands on the cloud's copy of a venue event (the
// stable-link path never reaches the cloud hub: it is tunnelled).
func (d *Deps) CommandGate(showID int64) error {
	if _, ok := d.venueEventOf(showID); ok {
		return errors.New("This event is running at the venue. Changes are paused until the link to the venue is back.")
	}
	return nil
}

// linkAs is who the cloud vouches for on a tunnelled request: the event's
// Event Technician, or moderator of some of its rooms ("" = nobody).
func (d *Deps) linkAs(cookies map[string]string, ev timerpi.Event) string {
	if SuperFromCookies(d.Store, cookies, ev) {
		return "super"
	}
	var rooms []string
	if list, err := d.Store.ListRooms(ev.ID); err == nil {
		for _, r := range list {
			if ModerateFromCookies(d.Store, cookies, r.ID) {
				rooms = append(rooms, r.Code)
			}
		}
	}
	if len(rooms) == 0 {
		return ""
	}
	return "rooms:" + strings.Join(rooms, ",")
}

// Tunnel is the cloud hub's socket hand-off for venue events on a stable
// link (operators and screens; phones stay on the cloud's audience lane).
func (d *Deps) Tunnel(showID int64, role string) (func(map[string]string, []byte, *websocket.Conn), bool) {
	ev, ok := d.venueEventOf(showID)
	if !ok {
		return nil, false
	}
	l := d.linkFor(ev.ID)
	if !l.stable() {
		return nil, false
	}
	return func(cookies map[string]string, join []byte, conn *websocket.Conn) {
		l.mu.Lock()
		l.nextID++
		sid := l.nextID
		l.streams[sid] = conn
		l.mu.Unlock()
		if l.send(LinkFrame{T: "ws-open", ID: sid, As: d.linkAs(cookies, ev), Text: string(join)}) != nil {
			_ = conn.Close()
			return
		}
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}
			if l.send(LinkFrame{T: "ws", ID: sid, Text: string(msg)}) != nil {
				break
			}
		}
		l.mu.Lock()
		delete(l.streams, sid)
		l.mu.Unlock()
		_ = l.send(LinkFrame{T: "ws-close", ID: sid})
		_ = conn.Close()
	}, true
}

// venueGate is the cloud's middleware for events running at a venue: phones'
// votes and questions go to the venue, operators work on the venue through
// the tunnel once the link is stable, and otherwise the cloud's copy is
// read-only.
func (d *Deps) venueGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !d.isCloud() || d.Store == nil {
			c.Next()
			return
		}
		ev, kind, ok := d.venueTarget(c)
		if !ok {
			c.Next()
			return
		}
		l := d.linkFor(ev.ID)
		switch kind {
		case "audience-read":
			id := d.audienceRoomID(c)
			if id == 0 {
				c.Next()
				return
			}
			on, _ := d.WrapPolls(nil)(id)
			c.JSON(http.StatusOK, gin.H{"ok": true, "data": gin.H{"poll": on.Audience.Trimmed(timerpi.MaxPublicEntries)}, "paused": on.Paused})
			c.Abort()
			return
		case "audience-write":
			if l == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "paused": true, "error": "Audience paused — the room is reconnecting"})
				c.Abort()
				return
			}
			peer, ok := d.audiencePeer(c)
			if !ok {
				c.Abort()
				return
			}
			d.tunnelHTTP(c, l, "", peer)
			return
		case "local":
			c.Next() // sign-in pages and the audience page: the cloud's copy
			return
		}
		// Operator surface.
		if l.stable() {
			d.tunnelHTTP(c, l, d.linkAs(cookieMap(c.Request), ev), "")
			return
		}
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
			c.Next() // read-only copy
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "This event is running at the venue. Changes are paused until the link to the venue is back."})
		c.Abort()
	}
}

// venueTarget classifies a request for an event running at a venue.
func (d *Deps) venueTarget(c *gin.Context) (timerpi.Event, string, bool) {
	parts := strings.Split(strings.Trim(c.Request.URL.Path, "/"), "/")
	byRoom := func(code string) (timerpi.Event, bool) {
		if id, ok := timerpi.ResolveShowID(d.Store, code); ok {
			return d.venueEventOf(id)
		}
		return timerpi.Event{}, false
	}
	byEvent := func(code string) (timerpi.Event, bool) {
		ev, ok := d.Store.ResolveEvent(code)
		return ev, ok && ev.Home == "venue"
	}
	switch {
	case len(parts) >= 2 && parts[0] == "a":
		ev, ok := byRoom(parts[1])
		return ev, "local", ok
	case len(parts) >= 3 && parts[0] == "api" && parts[1] == "audience":
		ev, ok := byRoom(parts[2])
		switch {
		case len(parts) == 3 && c.Request.Method == http.MethodGet:
			return ev, "audience-read", ok
		case len(parts) == 4 && (parts[3] == "vote" || parts[3] == "ask"):
			return ev, "audience-write", ok
		}
		return ev, "local", ok // the QR image
	case len(parts) >= 2 && (parts[0] == "c" || parts[0] == "screens"):
		ev, ok := byRoom(parts[1])
		return ev, "operator", ok
	case len(parts) >= 3 && parts[0] == "api" && parts[1] == "shows":
		ev, ok := byRoom(parts[2])
		return ev, "operator", ok
	case len(parts) >= 2 && parts[0] == "e":
		ev, ok := byEvent(parts[1])
		if len(parts) >= 3 && (parts[2] == "leave" || parts[2] == "phone") {
			return ev, "local", ok // the phone sign-in sets this server's own session
		}
		return ev, "operator", ok
	case len(parts) >= 3 && parts[0] == "api" && parts[1] == "events":
		ev, ok := byEvent(parts[2])
		lobby := len(parts) == 3 && c.Request.Method == http.MethodGet
		login := len(parts) >= 4 && (parts[3] == "login" || (parts[3] == "rooms" && len(parts) == 6 && parts[5] == "login"))
		if lobby || login {
			return ev, "local", ok // lobby and sign-in use the cloud's copy
		}
		return ev, "operator", ok
	}
	return timerpi.Event{}, "", false
}

func (d *Deps) audienceRoomID(c *gin.Context) int64 {
	parts := strings.Split(strings.Trim(c.Request.URL.Path, "/"), "/")
	if len(parts) >= 3 {
		if id, ok := timerpi.ResolveShowID(d.Store, parts[2]); ok {
			return id
		}
	}
	return 0
}

// tunnelHTTP sends the request to the venue and writes its answer.
func (d *Deps) tunnelHTTP(c *gin.Context, l *venueLink, as, peer string) {
	defer c.Abort()
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 32<<20))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false})
		return
	}
	hdr := map[string]string{}
	// Origin/Referer stay behind: the cloud checked them; the venue serves
	// the request in-process under its own host.
	for _, k := range []string{"Content-Type", "Accept", "HX-Request", "HX-Target", "HX-Trigger", "User-Agent"} {
		if v := c.GetHeader(k); v != "" {
			hdr[k] = v
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	res, err := l.request(ctx, LinkFrame{T: "req", Method: c.Request.Method, Path: c.Request.URL.RequestURI(), Header: hdr, Body: body, As: as, Peer: peer})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "The venue didn't answer: " + err.Error()})
		return
	}
	for k, v := range res.Header {
		if !strings.EqualFold(k, "Set-Cookie") && !strings.EqualFold(k, "Content-Length") {
			c.Header(k, v)
		}
	}
	if res.Status == 0 {
		res.Status = http.StatusBadGateway
	}
	c.Data(res.Status, res.Header["Content-Type"], res.Body)
}

// ------------------------------------------------- venue: linked requests --

type linkedKey struct{}

// linkedPeer is a phone's device id vouched for by the cloud.
func linkedPeer(c *gin.Context) (string, bool) {
	p, ok := c.Request.Context().Value(linkedKey{}).(string)
	return p, ok && p != ""
}

// LinkedRequest prepares a request that came over the link for this (the
// venue's) server: the cloud's cookies are dropped and replaced by sessions
// this server issues for whoever the cloud vouched for; a phone's device id
// rides the context.
func (d *Deps) LinkedRequest(r *http.Request, eventCode, as, peer string) (*http.Request, error) {
	ev, ok := d.Store.ResolveEvent(eventCode)
	if !ok {
		return nil, errors.New("unknown event")
	}
	r.Header.Del("Cookie")
	secret := d.Store.SessionSecret()
	switch {
	case as == "super":
		r.AddCookie(&http.Cookie{Name: superCookieName(ev.Code), Value: superToken(secret, ev)})
	case strings.HasPrefix(as, "rooms:"):
		for _, code := range strings.Split(strings.TrimPrefix(as, "rooms:"), ",") {
			if sh, ok := d.Store.ResolveShowInEvent(ev.ID, code); ok {
				r.AddCookie(&http.Cookie{Name: roomCookieName(sh.Code), Value: roomToken(secret, ev, sh)})
			}
		}
	}
	if peer != "" {
		r = r.WithContext(context.WithValue(r.Context(), linkedKey{}, peer))
	}
	return r, nil
}

// LinkedCookies is the Cookie header for a tunnelled socket (as above).
func (d *Deps) LinkedCookies(eventCode, as string) string {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	if r2, err := d.LinkedRequest(r, eventCode, as, ""); err == nil {
		return r2.Header.Get("Cookie")
	}
	return ""
}

// DeleteEventLocal removes an event from this box (a released or deleted
// event's mirror), with its rooms' engines and sockets.
func (d *Deps) DeleteEventLocal(eventID int64) error {
	rooms, err := d.Store.ListRooms(eventID)
	if err != nil {
		return err
	}
	if err := d.Store.DeleteEvent(eventID); err != nil {
		return err
	}
	for _, r := range rooms {
		d.forgetRoom(r.ID)
	}
	return nil
}
