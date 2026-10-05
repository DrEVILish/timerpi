// audience.go — the audience interaction REST surface (Slido-style).
//
// AUDIENCE (unauthenticated, join by show code / QR): GET current item,
// vote, submit. Burst-safe by design: one HTTP round-trip per vote (HTTP
// scales horizontally; WS is for the ~10 boards), a per-peer guard on
// submissions, and the vote table's UNIQUE(poll_id,peer) makes double-tap
// a no-op replace.
//
// OPERATOR (show-gated like every show mutation): create items, open
// voting, show results, delete, survey grouping.
package routes

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/config"
	"timerpi/timerpi"
)

// jsonMarshal saves an import alias in one place (polls REST).
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func registerAudienceRoutes(r gin.IRouter, d *Deps) {
	g := r.Group("/api/audience/:code")
	g.GET("", d.apiAudienceRead)
	g.POST("/ask", d.apiAudienceAsk)
	g.POST("/vote", d.apiAudienceVote)
	g.GET("/qr", d.apiAudienceQR)
	r.GET("/a/:code", d.audiencePage)

	// Moderator surface (PRODUCT §4.4).
	ig := r.Group("/api/shows/:ident/polls")
	ig.GET("", d.apiPollList)
	ig.POST("", d.apiPollCreate)
	ig.PATCH("/:pid", d.apiPollEdit)
	ig.POST("/:pid/show", d.apiPollShow)
	ig.POST("/:pid/results", d.apiPollResults)
	ig.POST("/:pid/hide", d.apiPollHide)
	ig.POST("/:pid/spotlight", d.apiPollSpotlight)
	ig.POST("/:pid/moderate", d.apiPollModerate)
	ig.POST("/:pid/state", d.apiPollSetState) // legacy single-verb transport
	ig.DELETE("/:pid", d.apiPollDelete)
}

// pollsChanged fans the change out: the on-air delta to phones + screens,
// and a refresh hint to the room's moderator panels.
func (d *Deps) pollsChanged(showID int64) {
	if d.Hub == nil {
		return
	}
	d.Hub.BroadcastPoll(showID)
	if b, err := json.Marshal(map[string]any{"t": "polls"}); err == nil {
		d.Hub.SendToRole(showID, "controls", b)
	}
}

// pollParam parses :pid.
func pollParam(c *gin.Context) (int64, bool) {
	pid, err := strconv.ParseInt(c.Param("pid"), 10, 64)
	if err != nil || pid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad item id"})
		return 0, false
	}
	return pid, true
}

// pollResult answers a moderator mutation uniformly.
func (d *Deps) pollResult(c *gin.Context, showID int64, action string, err error) {
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"ok": false, "error": strings.TrimPrefix(err.Error(), "timerpi: ")})
		return
	}
	d.logAction(showID, "poll", action)
	d.pollsChanged(showID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// askGuard: a peer may submit one contribution every 3 s (double-tap /
// double-tap churn). Votes rely on the DB UNIQUE instead — they're idempotent
// by key. ponytail: per-process map, resets on boot; per-IP LRU if abuse
// ever shows up at a real event.
var askGuard struct {
	sync.Mutex
	last map[string]int64
}

// voteGuard: one vote per peer per 300 ms (PLAN §11.5 rate guards).
var voteGuard struct {
	sync.Mutex
	last map[string]int64
}

// voteRecent/voteMark split the throttle so a vote that fails validation
// never burns the peer's window.
func voteRecent(peer string, now int64) bool {
	voteGuard.Lock()
	defer voteGuard.Unlock()
	if voteGuard.last == nil {
		voteGuard.last = map[string]int64{}
	}
	return now-voteGuard.last[peer] < 300
}

func voteMark(peer string, now int64) {
	voteGuard.Lock()
	defer voteGuard.Unlock()
	if voteGuard.last == nil {
		voteGuard.last = map[string]int64{}
	}
	voteGuard.last[peer] = now
	if len(voteGuard.last) > 20000 { // bounded (1000+ phones × restarts)
		voteGuard.last = map[string]int64{}
	}
}

// soak: per-show accept budget (requests/s) — beyond it the lane answers
// 429 and phones back off with jitter (PLAN §11.5). Overridable in tests.
var audSoakPerSec = 600

var soak struct {
	sync.Mutex
	cur map[int64]int
	sec int64
}

func soakAllowed(showID int64, now int64) bool {
	sec := now / 1000
	soak.Lock()
	defer soak.Unlock()
	if soak.cur == nil {
		soak.cur = map[int64]int{}
	}
	if soak.sec != sec {
		soak.sec = sec
		soak.cur = map[int64]int{}
	}
	soak.cur[showID]++
	return soak.cur[showID] <= audSoakPerSec
}

// askRecent / askMark split the submission throttle so a refused
// submission (nothing on air, empty text) never burns the device's window.
func askRecent(peer string, now int64) bool {
	askGuard.Lock()
	defer askGuard.Unlock()
	return askGuard.last != nil && now-askGuard.last[peer] < 3000
}

func askMark(peer string, now int64) {
	askGuard.Lock()
	defer askGuard.Unlock()
	if askGuard.last == nil || len(askGuard.last) > 5000 { // bounded
		askGuard.last = map[string]int64{}
	}
	askGuard.last[peer] = now
}

func (d *Deps) resolveAudienceCode(c *gin.Context) (int64, bool) {
	if d.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false})
		return 0, false
	}
	id, ok := timerpi.ResolveShowID(d.Store, c.Param("code"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "unknown session code"})
		return 0, false
	}
	return id, true
}

// GET /api/audience/:code — what this device sees right now.
func (d *Deps) apiAudienceRead(c *gin.Context) {
	id, ok := d.resolveAudienceCode(c)
	if !ok {
		return
	}
	out, err := d.Store.AudienceRead(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "data": out})
}

// POST /api/audience/:code/ask {item, text, peer} — a question / idea /
// cloud word for the item on the audience target. Lands pending unless the
// item auto-approves.
func (d *Deps) apiAudienceAsk(c *gin.Context) {
	id, ok := d.resolveAudienceCode(c)
	if !ok {
		return
	}
	var body struct {
		Item   int64  `json:"item"`
		Parent int64  `json:"parent"` // older clients
		Text   string `json:"text"`
		Peer   string `json:"peer"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "text required"})
		return
	}
	if body.Item == 0 {
		body.Item = body.Parent
	}
	if body.Item == 0 {
		if on, err := d.Store.ActivePoll(id); err == nil && on != nil {
			body.Item = on.ID
		}
	}
	now := time.Now().UnixMilli()
	if askRecent(body.Peer, now) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "Sending too fast — wait a moment"})
		return
	}
	p, err := d.Store.Submit(id, body.Item, body.Text, body.Peer)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": strings.TrimPrefix(err.Error(), "timerpi: ")})
		return
	}
	askMark(body.Peer, now)
	d.pollsChanged(id)
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": p.ID, "approved": p.State == timerpi.StateOpen})
}

// POST /api/audience/:code/vote {pollId, choice, peer} — vote / upvote.
func (d *Deps) apiAudienceVote(c *gin.Context) {
	id, ok := d.resolveAudienceCode(c)
	if !ok {
		return
	}
	var body struct {
		PollID int64  `json:"pollId"`
		Choice string `json:"choice"`
		Peer   string `json:"peer"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	if body.PollID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "pollId required"})
		return
	}
	now := time.Now().UnixMilli()
	if voteRecent(body.Peer, now) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "voting too fast"})
		return
	}
	if !soakAllowed(id, now) {
		c.Header("Retry-After", "1")
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "room is busy — try again in a moment"})
		return
	}
	if err := d.Store.Vote(id, body.PollID, body.Peer, body.Choice); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": strings.TrimPrefix(err.Error(), "timerpi: ")})
		return
	}
	voteMark(body.Peer, now)
	// PLAN §11.5: poll-only delta to the audience lane + boards — a vote
	// never triggers the full-snapshot mutation fanout.
	if d.Hub != nil {
		d.Hub.BroadcastPoll(id)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /api/audience/:code/qr — PNG of the audience join URL for this
// server (scheme+host honored, so proxied domains work).
func (d *Deps) apiAudienceQR(c *gin.Context) {
	id, ok := d.resolveAudienceCode(c)
	if !ok {
		return
	}
	sh, err := d.Store.GetShow(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false})
		return
	}
	scheme := "https"
	if c.Request.TLS == nil {
		scheme = "http"
	}
	url := fmt.Sprintf("%s://%s/a/%s", scheme, c.Request.Host, sh.Code)
	png, err := qrPNG(url, 320)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false})
		return
	}
	c.Data(http.StatusOK, "image/png", png)
}

// ---------------------------------------------------------------------------
// Operator endpoints

// GET /api/shows/:ident/polls — the moderator list: every item with
// counts, all entries (pending included), air state and pending count.
func (d *Deps) apiPollList(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	items, err := d.Store.ModeratorItems(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "items": items})
}

type pollBody struct {
	Kind        string   `json:"kind"`
	Question    string   `json:"question"`
	Options     []string `json:"options"`
	Correct     *int64   `json:"correct"`
	AutoApprove bool     `json:"autoApprove"`
}

func (b pollBody) cleanOptions() []string {
	out := []string{}
	for _, o := range b.Options {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, timerpi.ClipUTF8(o, 120))
		}
	}
	return out
}

// POST /api/shows/:ident/polls — create an item (always off air).
func (d *Deps) apiPollCreate(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var body pollBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	opts, _ := jsonMarshal(body.cleanOptions())
	p := timerpi.Poll{ShowID: id, Kind: body.Kind, Question: timerpi.ClipUTF8(body.Question, 200),
		Options: string(opts), Correct: -1, AutoApprove: body.AutoApprove}
	if body.Correct != nil {
		p.Correct = *body.Correct
	}
	created, err := d.Store.CreatePoll(p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": strings.TrimPrefix(err.Error(), "timerpi: ")})
		return
	}
	d.logAction(id, "pollCreate", fmt.Sprintf("%s %q", created.Kind, created.Question))
	d.pollsChanged(id)
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": created.ID})
}

// PATCH /api/shows/:ident/polls/:pid — edit text/options/answer/auto-approve.
func (d *Deps) apiPollEdit(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, ok := pollParam(c)
	if !ok {
		return
	}
	var body pollBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	correct := int64(-1)
	if body.Correct != nil {
		correct = *body.Correct
	}
	var opts []string
	if body.Options != nil {
		opts = body.cleanOptions()
	}
	d.pollResult(c, id, fmt.Sprintf("edit %d", pid), d.Store.UpdatePoll(id, pid, timerpi.ClipUTF8(body.Question, 200), opts, correct, body.AutoApprove))
}

// POST /api/shows/:ident/polls/:pid/show {target: audience|presenter, on}
func (d *Deps) apiPollShow(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, ok := pollParam(c)
	if !ok {
		return
	}
	var body struct {
		Target string `json:"target"`
		On     *bool  `json:"on"`
	}
	_ = c.ShouldBindJSON(&body)
	on := body.On == nil || *body.On
	d.pollResult(c, id, fmt.Sprintf("show %d %s %v", pid, body.Target, on), d.Store.ShowTo(id, pid, body.Target, on))
}

// POST /api/shows/:ident/polls/:pid/results {on}
func (d *Deps) apiPollResults(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, ok := pollParam(c)
	if !ok {
		return
	}
	var body struct {
		On *bool `json:"on"`
	}
	_ = c.ShouldBindJSON(&body)
	on := body.On == nil || *body.On
	d.pollResult(c, id, fmt.Sprintf("results %d %v", pid, on), d.Store.SetResults(id, pid, on))
}

// POST /api/shows/:ident/polls/:pid/hide — off every target.
func (d *Deps) apiPollHide(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, ok := pollParam(c)
	if !ok {
		return
	}
	d.pollResult(c, id, fmt.Sprintf("hide %d", pid), d.Store.HidePoll(id, pid))
}

// POST /api/shows/:ident/polls/:pid/spotlight {entry} — 0 clears.
func (d *Deps) apiPollSpotlight(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, ok := pollParam(c)
	if !ok {
		return
	}
	var body struct {
		Entry int64 `json:"entry"`
	}
	_ = c.ShouldBindJSON(&body)
	d.pollResult(c, id, fmt.Sprintf("spotlight %d %d", pid, body.Entry), d.Store.Spotlight(id, pid, body.Entry))
}

// POST /api/shows/:ident/polls/:pid/moderate {status} — :pid is an entry:
// pending (hidden) | approved (open) | answered | dismissed.
func (d *Deps) apiPollModerate(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, ok := pollParam(c)
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	_ = c.ShouldBindJSON(&body)
	status := map[string]string{"pending": timerpi.StateHidden, "approved": timerpi.StateOpen,
		"answered": timerpi.StateAnswered, "dismissed": timerpi.StateDismissed}[body.Status]
	if status == "" {
		status = body.Status // raw states are accepted too
	}
	d.pollResult(c, id, fmt.Sprintf("moderate %d %s", pid, body.Status), d.Store.Moderate(id, pid, status))
}

// POST /api/shows/:ident/polls/:pid/state {state} — legacy single verb.
func (d *Deps) apiPollSetState(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, ok := pollParam(c)
	if !ok {
		return
	}
	var body struct {
		State string `json:"state"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.State == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "state required"})
		return
	}
	d.pollResult(c, id, fmt.Sprintf("state %d %s", pid, body.State), d.Store.SetPollState(id, pid, body.State))
}

// DELETE /api/shows/:ident/polls/:pid — an item (with its entries) or one entry.
func (d *Deps) apiPollDelete(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, ok := pollParam(c)
	if !ok {
		return
	}
	d.pollResult(c, id, fmt.Sprintf("delete %d", pid), d.Store.DeletePoll(id, pid))
}

// audiencePage is GET /a/:code — the audience page (no login: the QR leads
// straight in; only the show passphrase gate applies if locked).
func (d *Deps) audiencePage(c *gin.Context) {
	if d.Store == nil {
		pageNotFound(c)
		return
	}
	id, ok := timerpi.ResolveShowID(d.Store, c.Param("code"))
	if !ok {
		pageUnknownCode(c)
		return
	}
	sh, err := d.Store.GetShow(id)
	if err != nil {
		pageError(c, err)
		return
	}
	d.render(c, "audience", gin.H{
		"Page": "audience", "Show": gin.H{"Code": sh.Code, "Title": sh.Title},
		"DefaultTheme": config.DefaultTheme(),
	})
}
