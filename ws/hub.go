// WebSocket hub per PLAN/PROTOCOL: one HTTP port, upgrades on /ws, joins
// {v:1,t:"join",role,show,peerId,joinedAt}, tracks peers per show, fans out
// the engine's snapshots as `state` frames plus oob fragments for the
// structural bits (never digits — clients tick those), relays WebRTC
// signaling, and runs the server-wide 250 ms Tick that drives zero
// crossings / alert edges / auto-advance.
//
// Lifecycle safety notes (PROTOCOL §hub + NOTES-timerpi): subscriber
// callbacks run on the engine's notify goroutine and must not block — the
// hub marshals frames once per emit and offers them non-blockingly into
// per-session buffered channels; a slow consumer's connection is dropped
// (its client reconnects and re-joins). No goroutine ever sends on a
// closed channel: session teardown is one kill() closing a done channel.
package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"timerpi/routes"
	"timerpi/timerpi"
	"timerpi/views"
)

const (
	writeTimeout    = 5 * time.Second  // data write deadline
	readDeadline    = 75 * time.Second // silent after this → drop (PROTOCOL keepalive)
	pingInterval    = 30 * time.Second // server control pings (client pings app-level every 20 s)
	tickInterval    = 250 * time.Millisecond
	seedInterval    = 5 * time.Second // discover shows created outside a hub join
	joinGrace       = 10 * time.Second
	maxSendBuffer   = 256 // frames per session; overflow drops the conn
	maxShowSessions = 512 // per-show cap against runaway openers
	// PLAN §11.5: the audience lane is its own budget — 1000+ phones must
	// never contend with the boards' 512.
	maxAudSessions = 4000
)

// Hub owns every live WS session and the per-show fanout + tickers.
type Hub struct {
	engines *timerpi.Engines
	// store is the DB (cue/message CRUD + show rename; injected from main).
	store *timerpi.DB
	// render renders a named fragment to HTML (views.Set.Fragment);
	// injected so tests can stub. nil → oob frames are skipped.
	render func(name string, data any) (string, error)
	// msgsFn reads the FULL message list for a show (the snapshot carries
	// shown-only); injected from routes. nil → message oob falls back to
	// the snapshot subset.
	msgsFn func(showID int64) ([]timerpi.Message, error)
	// pollsFn reads the show's on-air audience interaction (active poll
	// with counts); injected like msgsFn. nil → snapshot carries none.
	pollsFn func(showID int64) (*timerpi.PollView, error)
	// seeder returns all show ids, so the ticker drives engines no one has
	// joined this process lifetime (created over REST).
	seeder func() []int64
	// nowFn is the hub clock. Swapped only before goroutines start (tests).
	nowFn func() int64
	logf  func(format string, args ...any)

	upgr websocket.Upgrader

	mu     sync.Mutex
	byShow map[int64]*showHub
	stop   chan struct{}
	stopMu sync.Once
}

// showHub is the per-show fanout state.
type showHub struct {
	eng      *timerpi.Engine
	cancel   func()                // engine subscription (one per show)
	sessions map[*session]struct{} // live conns of this show
	aud      map[*session]struct{} // PLAN §11.5: audience lane (phones), outside the 512 cap
	sigs     showSignatures        // last rendered structures (oob diff)

	// bmu serializes broadcast for THIS show. engine.notify runs subscriber
	// callbacks inline on every mutating goroutine (ticker Tick, session
	// commands, REST Notify), so without it two goroutines race over sh.sigs
	// and read sh.sessions while register/unregister write it under h.mu —
	// unsynchronized map access that the runtime can kill the process for.
	bmu sync.Mutex
}

type showSignatures struct {
	cues     string // cue structure signature    → oob #cuelist + #tp-daybar
	msgs     string // message listing signature  → oob #messages-panel
	current  string // active/next cue text       → oob #tp-now
	schedule string // schedule-plan fingerprint  → schedule info frame
}

// NewHub wires the hub to the engine registry.
func NewHub(engines *timerpi.Engines, render func(string, any) (string, error)) *Hub {
	h := &Hub{
		engines: engines,
		render:  render,
		byShow:  map[int64]*showHub{},
		stop:    make(chan struct{}),
		nowFn:   func() int64 { return time.Now().UnixMilli() },
		logf:    log.Printf,
	}
	h.upgr = websocket.Upgrader{
		// Same origin policy as the HTTP surface (CSRF/DNS-rebind guard).
		CheckOrigin:     routes.SameOriginRequest,
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
	}
	return h
}

// SetRender injects the fragment renderer (main wiring: views.Set).
func (h *Hub) SetRender(fn func(string, any) (string, error)) { h.render = fn }

// SetStore injects the DB (cue/message CRUD, show rename).
func (h *Hub) SetStore(db *timerpi.DB) { h.store = db }

// SetMessagesFunc wires the full-message DB reader.
func (h *Hub) SetMessagesFunc(fn func(int64) ([]timerpi.Message, error)) { h.msgsFn = fn }

// SetPollsFunc wires the on-air audience-interaction reader. Synchronized:
// audience sessions read it from freshly spawned server goroutines (the
// race detector rightly flags the bare field).
func (h *Hub) SetPollsFunc(fn func(int64) (*timerpi.PollView, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pollsFn = fn
}

// pollsFnFor snapshots the reader under the hub lock.
func (h *Hub) pollsFnFor() func(int64) (*timerpi.PollView, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pollsFn
}

// SetSeeder wires the show-id lister (for the ticker's engine discovery).
func (h *Hub) SetSeeder(fn func() []int64) { h.seeder = fn }

// SetLogger swaps the logger (tests silence it).
func (h *Hub) SetLogger(fn func(string, ...any)) { h.logf = fn }

// SetNowFn swaps the hub clock (tests). Call before starting loops only.
func (h *Hub) SetNowFn(fn func() int64) { h.nowFn = fn }

// ---------------------------------------------------------------------------
// Gin wiring + loops.

// Forget drops a show's hub state after DELETE /api/shows/:id — the
// registry engine is gone; ticking a deleted show would just error-spam.
// Sessions of that show stay attached (their engine already errors on
// next use) but the fanout entry goes.
func (h *Hub) Forget(showID int64) {
	h.mu.Lock()
	sh, ok := h.byShow[showID]
	if ok {
		if sh.cancel != nil && len(sh.sessions) == 0 {
			sh.cancel()
			sh.cancel = nil
		}
		delete(h.byShow, showID)
	}
	h.mu.Unlock()
}

// Reload re-arms a show after a wholesale DB swap (mesh sync): fresh
// engine from the registry, fresh engine subscription for live sessions,
// one snapshot broadcast so everyone converges. No-op when nobody cares.
func (h *Hub) Reload(showID int64) error {
	eng, err := h.engines.Get(showID)
	if err != nil {
		return err
	}
	h.mu.Lock()
	sh, ok := h.byShow[showID]
	if !ok {
		sh = &showHub{sessions: map[*session]struct{}{}}
		h.byShow[showID] = sh
	}
	if sh.cancel != nil {
		sh.cancel()
		sh.cancel = nil
	}
	id := showID
	sh.cancel = eng.Subscribe(func(snap timerpi.Snapshot) { h.broadcast(id, snap) })
	sh.eng = eng
	live := len(sh.sessions)
	h.mu.Unlock()

	if live > 0 {
		snap, serr := eng.Snapshot()
		if serr != nil {
			return serr
		}
		sh.sigs = showSignatures{} // structure may have fully changed
		h.broadcast(showID, snap)
	}
	return nil
}

// Register mounts the /ws upgrade route. It runs inside the global
// middleware chain (OriginGuard's Host check applies too) and aborts the
// gin chain — after an upgrade (or a refused one) gin must not continue.
// gorilla's Upgrade writes its own HTTP error on handshake failure, so a
// refused upgrade needs no extra response here.
func (h *Hub) Register(r gin.IRouter) {
	r.GET("/ws", func(c *gin.Context) {
		if err := h.serve(c.Writer, c.Request); err != nil {
			h.logf("ws: upgrade refused: %v", err)
		}
		c.Abort()
	})
}

func (h *Hub) serve(w http.ResponseWriter, req *http.Request) error {
	// Capture the upgrade-request cookies BEFORE the hijack: the per-show
	// passphrase gate (showauth.go) validates tp_show_<CODE> against them.
	cookies := map[string]string{}
	for _, ck := range req.Cookies() {
		cookies[ck.Name] = ck.Value
	}
	conn, err := h.upgr.Upgrade(w, req, nil)
	if err != nil {
		return err
	}
	h.runSession(conn, cookies)
	return nil
}

// Start launches the ticker (+seed) and keepalive goroutines. Stop halts
// them.
func (h *Hub) Start() {
	go h.tickLoop()
	go h.pingLoop()
}

// Stop halts the background loops (sessions die with the HTTP server).
// Idempotent: shutdown paths may call it more than once.
func (h *Hub) Stop() {
	h.stopMu.Do(func() { close(h.stop) })
}

func (h *Hub) tickLoop() {
	tick := time.NewTicker(tickInterval)
	seed := time.NewTicker(seedInterval)
	defer tick.Stop()
	defer seed.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-tick.C:
			h.tickEngines()
		case <-seed.C:
			h.seedEngines()
		}
	}
}

// pingLoop sends a control ping to every session every 30 s (PROTOCOL) and
// drops sessions silent longer than the read deadline (no app frames AND
// no pongs). WriteControl is safe concurrent with data writes.
func (h *Hub) pingLoop() {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
			h.pingSessions()
		}
	}
}

func (h *Hub) pingSessions() {
	now := h.nowFn()
	h.mu.Lock()
	var all []*session
	for _, sh := range h.byShow {
		for s := range sh.sessions {
			all = append(all, s)
		}
		for s := range sh.aud {
			all = append(all, s)
		}
	}
	h.mu.Unlock()
	for _, s := range all {
		if s.activeAgeMs(now) > int64(readDeadline/time.Millisecond) {
			s.kill()
			continue
		}
		_ = s.conn.WriteControl(websocket.PingMessage, []byte("tp"), time.Now().Add(writeTimeout))
	}
}

// tickEngines advances every known engine; Tick emits only on change so
// the 250 ms cadence costs nothing while idle. One goroutine, one engine
// at a time: engines serialize internally.
func (h *Hub) tickEngines() {
	now := h.nowFn()
	h.mu.Lock()
	type live struct {
		id  int64
		eng *timerpi.Engine
	}
	engines := make([]live, 0, len(h.byShow))
	for id, sh := range h.byShow {
		// Every known engine ticks: joined shows AND seeded engines (a
		// running day keeps advancing with no spectators attached).
		engines = append(engines, live{id, sh.eng})
	}
	h.mu.Unlock()
	for _, e := range engines {
		if err := e.eng.Tick(now); err != nil {
			h.logf("ws: tick show %d: %v", e.id, err)
		}
	}
}

// seedEngines discovers shows created since boot (REST flows) so running
// days auto-advance even with no WS spectators.
func (h *Hub) seedEngines() {
	if h.seeder == nil {
		return
	}
	for _, id := range h.seeder() {
		h.mu.Lock()
		if _, ok := h.byShow[id]; ok {
			h.mu.Unlock()
			continue
		}
		h.mu.Unlock()
		// Create + hold an engine so the shared ticker drives it. The
		// subscription installs lazily on the first join (showHubLocked).
		if eng, err := h.engines.Get(id); err == nil {
			h.mu.Lock()
			if _, ok := h.byShow[id]; !ok {
				h.byShow[id] = &showHub{eng: eng, sessions: map[*session]struct{}{}, aud: map[*session]struct{}{}}
			}
			h.mu.Unlock()
		}
	}
}

// showHubLocked fetches (creating) the per-show state; h.mu held. The
// subscription is (re)installed lazily: seeded hubs have no engine
// subscription until the first join, and hubs whose last session left had
// theirs cancelled.
func (h *Hub) showHubLocked(showID int64, eng *timerpi.Engine) *showHub {
	sh, ok := h.byShow[showID]
	if !ok {
		sh = &showHub{eng: eng, sessions: map[*session]struct{}{}, aud: map[*session]struct{}{}}
		h.byShow[showID] = sh
	}
	if sh.cancel == nil {
		id := showID
		sh.cancel = eng.Subscribe(func(snap timerpi.Snapshot) { h.broadcast(id, snap) })
	}
	if sh.eng == nil {
		sh.eng = eng
	}
	return sh
}

func (h *Hub) getShowHub(showID int64) *showHub {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.byShow[showID]
}

// ---------------------------------------------------------------------------
// Fanout.

// broadcast turns an engine snapshot into the outbound frame batch for the
// show: oob fragments for structures that changed (cheap html/template
// renders; digits never), then the full state frame LAST (clients adopt
// the snapshot after their DOM is swapped).
func (h *Hub) broadcast(showID int64, snap timerpi.Snapshot) {
	// Audience-layer merge: the poll carried into EVERY frame (schedule
	// lead, oobs and state) below — one reader, none stale.
	if h.pollsFn != nil {
		if v, err := h.pollsFn(showID); err == nil {
			snap.Poll = v
		}
	}
	h.mu.Lock()
	sh := h.byShow[showID]
	n := 0
	if sh != nil {
		n = len(sh.sessions)
	}
	h.mu.Unlock()
	if sh == nil || n == 0 {
		return
	}
	sh.bmu.Lock()
	defer sh.bmu.Unlock()

	// Schedule info frame (CONTRACT-UI §5, REVIEW-3 D3): the pure
	// recomputed plan whenever the running order's structure or any
	// schedule anchor changed (import, cueAdd/Edit/Del/Move, daystart,
	// rate) — the client's needle / #tp-next-start refresh from it.
	scheduleReady, ok := h.scheduleFrame(sh, snap)
	var leads [][]byte // frames sent before the oobs/state (order matters)
	if ok {
		leads = append(leads, scheduleReady)
	}

	if h.render == nil {
		// No renderer (tests): schedule + state-only fanout.
		for _, b := range leads {
			h.fanout(showID, b)
		}
		h.fanout(showID, marshalFrame("t", "state", "snapshot", snap))
		return
	}
	data := views.ShowData(snap, h.nowFn(), "")
	if data == nil {
		// Never happens in production; the consumed schedule signature
		// above is harmless (the next snapshot re-diffs identically).
		return
	}

	var oobs [][]byte
	if sig := cuesSignature(snap.Cues); sig != sh.sigs.cues {
		for _, frag := range []string{"frag-cuelist", "frag-daybar"} {
			if html, err := h.render(frag, data); err == nil {
				oobs = append(oobs, oobFrame(oobTargetOf(frag), html))
			}
		}
		sh.sigs.cues = sig
	}
	if sig := messagesSignature(data.Messages); sig != sh.sigs.msgs {
		if h.msgsFn != nil {
			if msgs, err := h.msgsFn(showID); err == nil {
				data.SetMessages(msgs)
				sig = messagesSignature(data.Messages) // diff on the FULL list
			}
		}
		if html, err := h.render("frag-messages", data); err == nil {
			oobs = append(oobs, oobFrame("#messages-panel", html))
		}
		sh.sigs.msgs = sig
	}
	if sig := currentSignature(snap); sig != sh.sigs.current {
		if html, err := h.render("frag-current", data); err == nil {
			oobs = append(oobs, oobFrame("#tp-now", html))
		}
		sh.sigs.current = sig
	}

	state := marshalFrame("t", "state", "snapshot", snap)
	for _, b := range leads {
		h.fanout(showID, b)
	}
	for _, b := range oobs {
		h.fanout(showID, b)
	}
	h.fanout(showID, state)
}

// scheduleRowFrame is the lean schedule row for the `schedule` frame; the
// field names mirror the client's computeSchedule output
// (public/src/engine.js) and CONTRACT-UI §5's accepted shape.
type scheduleRowFrame struct {
	Pos     int64 `json:"pos"`
	StartMS int64 `json:"startMS"`
	EndMS   int64 `json:"endMS"`
	HoldMS  int64 `json:"holdMS"`
	Break   bool  `json:"isBreak"` // break kind OR a HoldMS buffer (client rule)
}

// scheduleFrame recomputes the pure schedule from the snapshot's cues and
// returns the `{"t":"schedule",rows,totalMS,dayStartTS}` frame bytes
// whenever its fingerprint changed since the last broadcast. The
// fingerprint covers the cue structure PLUS dayStartTS/rate — rate never
// changes rows/totals (schedule math is rate-free per PROTOCOL §Engine),
// but the frame doubles as the client needle/next-start refresh after a
// `rate` or `settings{ts}` jump (REVIEW-3 D3 fix-checklist #6). ok=false
// = unchanged, no frame.
func (h *Hub) scheduleFrame(sh *showHub, snap timerpi.Snapshot) (frame []byte, ok bool) {
	sig := scheduleSignature(snap)
	if sh.sigs.schedule == sig {
		return nil, false
	}
	sh.sigs.schedule = sig
	s := timerpi.ComputeSchedule(snap.Cues, snap.Runtime.DayStartTS, snap.Runtime.Rate)
	rows := make([]scheduleRowFrame, 0, len(s.Rows))
	for _, r := range s.Rows {
		rows = append(rows, scheduleRowFrame{
			Pos:     r.Pos,
			StartMS: r.StartMS,
			EndMS:   r.EndMS,
			HoldMS:  r.HoldMS,
			Break:   r.Break || r.HoldMS > 0,
		})
	}
	return marshalFrame("t", "schedule",
		"rows", rows, "totalMS", s.TotalMS, "dayStartTS", s.DayStartTS), true
}

// oobTargetOf maps a fragment name to its CONTRACT-UI oob swap target.
func oobTargetOf(frag string) string {
	switch frag {
	case "frag-cuelist":
		return "#cuelist"
	case "frag-daybar":
		return "#tp-daybar"
	case "frag-messages":
		return "#messages-panel"
	case "frag-current":
		return "#tp-now"
	case "frag-display":
		return "#d-stage"
	case "frag-share":
		return "#share-panel"
	}
	return ""
}

func oobFrame(target, html string) []byte {
	return marshalFrame("t", "oob", "html", html, "target", target)
}

// fanout offers a pre-marshaled frame to every session of the show. Non-
// blocking by construction (session.offer); the lock only snapshots the
// target list so session teardown never deadlocks the emitter.
func (h *Hub) fanout(showID int64, frame []byte) {
	h.mu.Lock()
	sh, ok := h.byShow[showID]
	if !ok {
		h.mu.Unlock()
		return
	}
	targets := make([]*session, 0, len(sh.sessions)+1)
	for s := range sh.sessions {
		targets = append(targets, s)
	}
	h.mu.Unlock()
	for _, s := range targets {
		s.offer(frame)
	}
}

// marker: peers frames live in session.go alongside the wire helpers.

// marshalFrame builds a JSON frame from key/value pairs (compile-safe at
// call sites; the return is a ready-to-write buffer shared by fanout).
func marshalFrame(kv ...any) []byte {
	m := make(map[string]any, len(kv)/2+1)
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok {
			m[k] = kv[i+1]
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

// peerView is one peers-frame entry.
type peerView struct {
	PeerID   string `json:"peerId"`
	Role     string `json:"role"`
	JoinedAt int64  `json:"joinedAt"`
	Screen   string `json:"screen,omitempty"`
}

// peerViews is the peers list; wire shape = [{peerId,role,joinedAt}] (+
// screen for named displays).
type peerViews []peerView

func (p peerViews) len() int { return len(p) }

// peersOf lists a show's sessions as sorted peer views. except nil =
// include everyone.
func (h *Hub) peersOf(showID int64, except *session) peerViews {
	h.mu.Lock()
	defer h.mu.Unlock()
	sh, ok := h.byShow[showID]
	if !ok {
		return nil
	}
	return peersFromLocked(sh, except)
}

// ---------------------------------------------------------------------------
// Session counts for /health.

// KickSession drops every live session of a show carrying peerID (operator
// "delete session"). kill() closes the done channel; the readLoop teardown
// unregisters + fans the refreshed peers list. Returns the count kicked.
func (h *Hub) KickSession(showID int64, peerID string) int {
	h.mu.Lock()
	var victims []*session
	if sh, ok := h.byShow[showID]; ok {
		for ses := range sh.sessions {
			if ses.id == peerID {
				victims = append(victims, ses)
			}
		}
	}
	h.mu.Unlock()
	for _, s := range victims {
		// Tell the client WHY the link died: mesh.js marks the session
		// deleted and stops reconnecting (otherwise the retry loop would
		// undo the operator's action in seconds).
		s.sendErr("session deleted (reload the page to rejoin)")
	}
	if len(victims) > 0 {
		time.Sleep(150 * time.Millisecond) // let writers drain the frame
		for _, s := range victims {
			s.kill()
		}
	}
	return len(victims)
}

// ScreenPeers lists [peerId, role] pairs per registered screen name — the
// per-session disconnect list for the panel/gallery.
func (h *Hub) ScreenPeers(showID int64) map[string][][2]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string][][2]string{}
	sh, ok := h.byShow[showID]
	if !ok {
		return out
	}
	for ses := range sh.sessions {
		if ses.screen == "" {
			continue
		}
		out[ses.screen] = append(out[ses.screen], [2]string{ses.id, ses.role})
	}
	return out
}

// ShowSessions counts every live session (any role) on one show — the
// super panel's per-room presence number (PLAN §11.1).
func (h *Hub) ShowSessions(showID int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if sh, ok := h.byShow[showID]; ok {
		return len(sh.sessions)
	}
	return 0
}

// ScreenSessions counts live sessions per screen name for one show
// (F1 presence for the screens panel; "" anonymous sessions excluded).
func (h *Hub) ScreenSessions(showID int64) map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]int{}
	sh, ok := h.byShow[showID]
	if !ok {
		return out
	}
	for ses := range sh.sessions {
		if ses.screen == "" {
			continue
		}
		out[ses.screen]++
	}
	return out
}

// SendToRole fans a frame to every live session of one role (F1: refresh
// the operator screens panel on registry changes). Returns the count.
func (h *Hub) SendToRole(showID int64, role string, frame []byte) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	sh, ok := h.byShow[showID]
	if !ok {
		return 0
	}
	n := 0
	for ses := range sh.sessions {
		if ses.role == role {
			ses.offer(frame)
			n++
		}
	}
	return n
}
func (h *Hub) Sessions() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, sh := range h.byShow {
		n += len(sh.sessions)
	}
	return n
}

// AudSessions counts live audience-lane sessions across shows (§11.5).
func (h *Hub) AudSessions() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, sh := range h.byShow {
		n += len(sh.aud)
	}
	return n
}

// BroadcastPoll pushes the on-air interaction as a SMALL delta frame to
// the audience lane and the board/control sessions (PLAN §11.5: a vote
// must never trigger a full-snapshot fanout). A nil poll ships as null —
// the section disappears by absence (§11.4).
func (h *Hub) BroadcastPoll(showID int64) {
	var pv *timerpi.PollView
	if fn := h.pollsFnFor(); fn != nil {
		if v, err := fn(showID); err == nil {
			pv = v
		}
	}
	frame := marshalFrame("v", 1, "t", "poll", "poll", pv, "ts", h.nowFn())
	h.mu.Lock()
	sh, ok := h.byShow[showID]
	if !ok {
		h.mu.Unlock()
		return
	}
	targets := make([]*session, 0, len(sh.sessions)+len(sh.aud))
	for s2 := range sh.sessions {
		targets = append(targets, s2)
	}
	for s2 := range sh.aud {
		targets = append(targets, s2)
	}
	h.mu.Unlock()
	for _, s2 := range targets {
		s2.offer(frame)
	}
}

// SessionsByRole counts live connections per role (health detail).
func (h *Hub) SessionsByRole() map[string]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]int{}
	for _, sh := range h.byShow {
		for s := range sh.sessions {
			out[s.role]++
		}
		for s := range sh.aud {
			out[s.role]++
		}
	}
	return out
}
