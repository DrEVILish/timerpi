// package ws — session.go: per-connection lifecycle (join handshake,
// registration, read/write pumps). One reader goroutine and one writer
// goroutine per connection; sessions never outlive their socket, and the
// engine's notify goroutine can never block on a session (buffered offer).
package ws

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"timerpi/routes"
	"timerpi/timerpi"
)

// session is one live WebSocket connection.
type session struct {
	hub         *Hub
	conn        *websocket.Conn
	send        chan []byte
	done        chan struct{}
	closeMu     sync.Once
	httpCookies map[string]string // upgrade-request cookies (per-show gate)
	id          string            // peerId (client-supplied or generated)
	role        string
	screen      string // F1: stable display name (?screen=), "" = anonymous
	showID      int64
	joinedAt    int64
	active      int64 // unix ms of last inbound activity (atomic)
}

// Session pipeline:
//
//	reader (runSession) → join frame → h.register → readLoop (hub.handle)
//	writer (writeLoop)  → drains send → kills self on write error
//	teardown → h.unregister → kill() (closes done; writer closes the socket)
func (h *Hub) runSession(conn *websocket.Conn, httpCookies map[string]string) {
	s := &session{
		hub:         h,
		conn:        conn,
		send:        make(chan []byte, maxSendBuffer),
		done:        make(chan struct{}),
		active:      h.nowFn(),
		httpCookies: httpCookies,
	}
	conn.SetPongHandler(func(string) error {
		atomic.StoreInt64(&s.active, h.nowFn())
		return s.conn.SetReadDeadline(time.Now().Add(readDeadline))
	})
	go s.writeLoop()

	if h.readJoin(s) && h.register(s) {
		if err := s.readLoop(); err != nil {
			h.logf("ws: session %s ended: %v", s.id, readErrString(err))
		}
	}
	h.unregister(s)
	s.kill()
}

// readJoin blocks until a well-formed join frame arrives (first frame on
// the connection). Returns true when the session is ready for the command
// loop; sends the err frame itself otherwise.
//
// Agent L (scope change, code-only addressing): the frame's `show` field
// accepts ONLY the 8-char share code STRING. A numeric payload (the old
// clients' convention) errors with the same "Unknown session code" copy
// the HTTP surface uses — the numeric id is an internal DB key, never an
// address. Resolution runs through timerpi.ResolveShowID (NormalizeCode
// typo maps included), then the session keeps the INTERNAL numeric id as
// before (fanout/registry keys).
func (h *Hub) readJoin(s *session) bool {
	s.conn.SetReadDeadline(time.Now().Add(joinGrace))
	_, raw, err := s.conn.ReadMessage()
	if err != nil {
		return false // pre-join silence: close quietly, no err frame
	}
	var j struct {
		V        int             `json:"v"`
		T        string          `json:"t"`
		Role     string          `json:"role"`
		Show     json.RawMessage `json:"show"`
		PeerID   string          `json:"peerId"`
		JoinedAt int64           `json:"joinedAt"`
		Screen   string          `json:"screen"`
	}
	if jerr := json.Unmarshal(raw, &j); jerr != nil || j.T != "join" {
		s.sendErr("first frame must be a join frame")
		return false
	}
	showID, code, rerr := resolveJoinShow(h.store, j.Show)
	if rerr != nil {
		s.sendErr(rerr.Error())
		return false
	}
	if j.Role == "" {
		j.Role = "display"
	}
	switch j.Role {
	case "controls", "display", "screen", "audience":
	default:
		s.sendErr("unknown role " + strconv.Quote(j.Role))
		return false
	}
	// Operator joins need moderator access to this room (or the event's
	// SuperOperator session) — the upgrade request's cookies carry it.
	// Screens and phones stay open; they are read-only (commands.go).
	if j.Role == "controls" && !routes.ModerateFromCookies(h.store, s.httpCookies, showID) {
		log.Printf("ws: controls join refused for show %s: no moderator session", code)
		s.sendErr("moderator access required (join the room from the home page)")
		return false
	}
	s.id = orGenID(j.PeerID)
	s.role = j.Role
	s.screen = timerpi.SanitizeScreenName(j.Screen)
	s.showID = showID
	s.joinedAt = j.JoinedAt
	if s.joinedAt <= 0 {
		s.joinedAt = h.nowFn()
	}
	return true
}

// resolveJoinShow decodes the join frame's `show` field: ONLY the share
// code string joins (Agent L scope change). Numbers and non-strings error
// with the client-facing copy; hub test/skeleton wiring without a store
// degrades to "cannot resolve" for the same reason. Smallest-diff shim on
// purpose — ws/*.go belongs to another fixer (Fix-2); this helper + the
// readJoin field type are the whole touch here.
func resolveJoinShow(store *timerpi.DB, raw json.RawMessage) (int64, string, error) {
	var number int64
	if len(raw) > 0 && json.Unmarshal(raw, &number) == nil {
		return 0, "", fmt.Errorf("Unknown session code — join with the 8-character share code")
	}
	var code string
	if err := json.Unmarshal(raw, &code); err != nil || strings.TrimSpace(code) == "" {
		return 0, "", fmt.Errorf("join needs a show code")
	}
	if store == nil {
		return 0, "", fmt.Errorf("show store unavailable")
	}
	id, ok := timerpi.ResolveShowID(store, code)
	if !ok {
		return 0, "", fmt.Errorf("Unknown session code — check the 4-4 code on the operator dashboard")
	}
	return id, code, nil
}

// orGenID fills an empty peerId (a provided one wins; mesh code treats ids
// as client identity).
func orGenID(peerID string) string {
	if peerID != "" {
		return peerID
	}
	var b [4]byte
	_, _ = randRead(b[:])
	return fmt.Sprintf("s-%x", b)
}

// register validates the show, attaches the session to its show hub, sends
// the joined frame, and announces the refreshed peers list to the rest.
func (h *Hub) register(s *session) bool {
	eng, err := h.engines.Get(s.showID)
	if err != nil {
		s.sendErr(fmt.Sprintf("unknown show %d", s.showID))
		return false
	}
	// F1 registry write BEFORE h.mu: the DB is single-connection, and a
	// write inside the hub lock lets one join stall fanout for every show.
	if s.screen != "" && h.store != nil {
		if uerr := h.store.UpsertScreen(s.showID, s.screen); uerr != nil {
			h.logf("ws: upsert screen %q: %v", s.screen, uerr)
		}
	}
	// PLAN §11.5: the audience lane — phones join their own bucket with
	// their own cap, skip the full snapshot entirely, and receive the
	// visible-interaction state only (never cue state).
	if s.role == "audience" {
		pollsFn := h.pollsFnFor() // before the lock — Mutex is not reentrant
		h.mu.Lock()
		sh := h.showHubLocked(s.showID, eng)
		if len(sh.aud) >= maxAudSessions {
			h.mu.Unlock()
			s.sendErr("audience capacity reached for this show")
			return false
		}
		sh.aud[s] = struct{}{}
		h.mu.Unlock()
		var on timerpi.OnAir
		if pollsFn != nil {
			on, _ = pollsFn(s.showID)
		}
		s.offer(pollFrame(on, false, h.nowFn()))
		h.logf("ws: audience joined show %d (%d on the lane)", s.showID, h.AudSessions())
		return true
	}

	snap, err := eng.Snapshot()
	if err != nil {
		// Only a missing show row errors here; phrase for the client's
		// /unknown show/i check.
		s.sendErr(fmt.Sprintf("unknown show %d", s.showID))
		return false
	}

	h.mu.Lock()
	sh := h.showHubLocked(s.showID, eng)
	if len(sh.sessions) >= maxShowSessions {
		h.mu.Unlock()
		s.sendErr("too many connections for this show")
		return false
	}
	sh.sessions[s] = struct{}{}
	others := peersFromLocked(sh, s)
	everyone := peersFromLocked(sh, nil)
	h.mu.Unlock()

	// Join reply first (client builds its peer table from it).
	s.sendFrame("t", "joined",
		"you", map[string]any{"peerId": s.id, "role": s.role, "joinedAt": s.joinedAt},
		"snapshot", snap,
		"peers", others.wire(),
	)
	if len(others) > 0 {
		h.fanout(s.showID, marshalFrame("t", "peers", "peers", everyone.wire()))
	}
	// F1: a named display adopts its operator-assigned config right on
	// join — theme + board assignment reach the reload without any extra
	// fetch, and a locked board navigates to its assigned layout.
	if s.screen != "" && h.store != nil {
		if scr, serr := h.store.GetScreenByName(s.showID, s.screen); serr == nil {
			if scr.Theme != "" {
				s.sendFrame("t", "display", "theme", scr.Theme)
			}
			if scr.BoardID > 0 {
				s.sendFrame("t", "screen-board", "boardId", scr.BoardID)
			}
		}
	}
	// PLAN §11.5 owner round: EVERY joining session (boards + operators too,
	// not just the audience lane) learns the on-air interaction immediately —
	// a results board reloading mid-vote must show the bars without waiting
	// for the next vote (results polls block NEW votes, so the delta would
	// never fire; owner-visible bug: reload ≠ bars).
	if fn := h.pollsFnFor(); fn != nil {
		if on, perr := fn(s.showID); perr == nil {
			s.offer(pollFrame(on, true, h.nowFn()))
		}
	}
	h.logf("ws: %s joined show %d as %s (%d connected)", s.id, s.showID, s.role, everyone.len())
	return true
}

// unregister removes the session; when the last client leaves, the engine
// subscription is cancelled but the engine keeps ticking.
func (h *Hub) unregister(s *session) {
	if s.showID <= 0 || s.id == "" {
		return
	}
	h.mu.Lock()
	sh, ok := h.byShow[s.showID]
	wasPresent := false
	if ok {
		if _, wasPresent = sh.sessions[s]; wasPresent {
			delete(sh.sessions, s)
		}
		if _, wasAud := sh.aud[s]; wasAud {
			delete(sh.aud, s)
		}
		if sh.cancel != nil && len(sh.sessions) == 0 && len(sh.aud) == 0 {
			sh.cancel()
			sh.cancel = nil
		}
	}
	h.mu.Unlock()
	if !ok {
		return
	}
	h.logf("ws: %s left show %d (%d connected after)", s.id, s.showID, h.Sessions())
	if wasPresent {
		if peers := h.peersOf(s.showID, nil); peers.len() > 0 {
			h.fanout(s.showID, marshalFrame("t", "peers", "peers", peers.wire()))
		}
	}
}

// peersFromLocked lists a showHub's sessions as sorted peer views.
// except nil = include everyone. Callers hold h.mu.
func peersFromLocked(sh *showHub, except *session) peerViews {
	peers := make(peerViews, 0, len(sh.sessions))
	for ses := range sh.sessions {
		if except != nil && ses == except {
			continue
		}
		peers = append(peers, peerView{PeerID: ses.id, Role: ses.role, JoinedAt: ses.joinedAt, Screen: ses.screen})
	}
	sortPeers(peers)
	return peers
}

// ScreenLive is one screen's live presence (hub view for the F1 panel).
type ScreenLive struct {
	Name      string
	Peers     int
	Roles     []string
	Connected bool
}

// liveScreens groups a show's live sessions by screen name ("" skipped —
// anonymous displays are peers, not managed screens). Callers hold h.mu.
func liveScreensLocked(sh *showHub) map[string]*ScreenLive {
	out := map[string]*ScreenLive{}
	for ses := range sh.sessions {
		if ses.screen == "" {
			continue
		}
		lv, ok := out[ses.screen]
		if !ok {
			lv = &ScreenLive{Name: ses.screen}
			out[ses.screen] = lv
		}
		lv.Peers++
		lv.Roles = append(lv.Roles, ses.role)
	}
	for _, lv := range out {
		lv.Connected = lv.Peers > 0
	}
	return out
}

// SendToScreen fans frames to every live session joined under a screen
// name (F1 targeted push: theme/board assignment). Returns the session
// count reached.
func (h *Hub) SendToScreen(showID int64, screen string, frames ...[]byte) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	sh, ok := h.byShow[showID]
	if !ok {
		return 0
	}
	n := 0
	for ses := range sh.sessions {
		if ses.screen != screen {
			continue
		}
		for _, b := range frames {
			ses.offer(b)
		}
		n++
	}
	return n
}

// ---------------------------------------------------------------------------
// Queue/transport.

// offer queues a frame: buffered, never blocking (engine notify goroutine
// included). A full buffer drops the connection — slow consumer; its
// client reconnects and re-joins.
func (s *session) offer(frame []byte) bool {
	select {
	case <-s.done:
		return false
	default:
	}
	select {
	case s.send <- frame:
		return true
	case <-s.done:
		return false
	default:
		s.kill()
		return false
	}
}

// sendFrame marshals+queues one frame from kv pairs.
func (s *session) sendFrame(kv ...any) {
	s.offer(marshalFrame(kv...))
}

// sendErr queues an err frame to this session only.
func (s *session) sendErr(msg string) {
	s.sendFrame("t", "err", "message", msg)
}

// writeLoop drains the queue onto the socket; done closing → socket close.
func (s *session) writeLoop() {
	for {
		select {
		case <-s.done:
			// Refusal paths (join gates) enqueue the err frame and kill()
			// immediately; this select raced done vs the queued frame and
			// sometimes closed the socket BEFORE the refusal reached the
			// client. Flush what's queued (bounded, then a short settle)
			// before closing — teardown is not allowed to eat the last frame.
			s.flushRemaining()
			_ = s.conn.Close()
			return
		case frame := <-s.send:
			_ = s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if err := s.conn.WriteMessage(websocket.TextMessage, frame); err != nil {
				s.kill()
			}
		}
	}
}

// kill tears the session down exactly once, from any goroutine.
func (s *session) kill() { s.closeMu.Do(func() { close(s.done) }) }

// flushRemaining drains queued writes for a short grace period after done
// fires, so a refusal/error enqueued just before teardown still reaches the
// client. Stops when the queue stays empty for one full grace window
// (steady state) or frames simply stop arriving.
func (s *session) flushRemaining() {
	const grace = 50 * time.Millisecond
	drain := time.NewTimer(grace)
	defer drain.Stop()
	for {
		select {
		case frame := <-s.send:
			_ = s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			_ = s.conn.WriteMessage(websocket.TextMessage, frame)
			drain.Reset(grace)
		case <-drain.C:
			return
		}
	}
}

// readLoop consumes frames until the connection retires. Every inbound
// frame resets the deadline: the client app-pings every 20 s (PROTOCOL),
// and control pongs count via the PongHandler.
func (s *session) readLoop() error {
	for {
		_ = s.conn.SetReadDeadline(time.Now().Add(readDeadline))
		_, raw, err := s.conn.ReadMessage()
		if err != nil {
			return err
		}
		atomic.StoreInt64(&s.active, s.hub.nowFn())
		s.hub.handle(s, raw)
	}
}

// touch records inbound activity for the ping loop's liveness check.
func (s *session) touch(now int64) { atomic.StoreInt64(&s.active, now) }

// activeAgeMs is the ms since the last inbound frame/pong.
func (s *session) activeAgeMs(now int64) int64 { return now - atomic.LoadInt64(&s.active) }

// readErrString folds routine close reasons into a quieter log line.
func readErrString(err error) string {
	if errors.Is(err, websocket.ErrCloseSent) {
		return "closed"
	}
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		return fmt.Sprintf("close %d", ce.Code)
	}
	return err.Error()
}
