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
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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

	ig := r.Group("/api/shows/:ident/polls")
	ig.GET("", d.apiPollList)
	ig.POST("", d.apiPollCreate)
	ig.POST("/:pid/state", d.apiPollSetState)
	ig.DELETE("/:pid", d.apiPollDelete)
}

// askGuard: a peer may submit one contribution every 3 s (double-tap /
// double-tap churn). Votes rely on the DB UNIQUE instead — they're idempotent
// by key. ponytail: per-process map, resets on boot; per-IP LRU if abuse
// ever shows up at a real event.
var askGuard struct {
	sync.Mutex
	last map[string]int64
}

func askAllowed(peer string, now int64) bool {
	askGuard.Lock()
	defer askGuard.Unlock()
	if askGuard.last == nil {
		askGuard.last = map[string]int64{}
	}
	if t := askGuard.last[peer]; now-t < 3000 {
		return false
	}
	askGuard.last[peer] = now
	if len(askGuard.last) > 5000 { // bounded
		askGuard.last = map[string]int64{}
	}
	return true
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
	// Locked shows gate their audience surface too (the page already does
	// via showGateByShowID); without this the REST lane would bypass the
	// passphrase the page itself demands.
	if !showGateByShowID(c, d, id) {
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

// POST /api/audience/:code/ask {kind, text, parent?} — submit a question /
// idea / cloud word (lands hidden: moderation by silence).
func (d *Deps) apiAudienceAsk(c *gin.Context) {
	id, ok := d.resolveAudienceCode(c)
	if !ok {
		return
	}
	var body struct {
		Kind   string `json:"kind"`
		Text   string `json:"text"`
		Parent int64  `json:"parent"`
		Peer   string `json:"peer"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "text required"})
		return
	}
	if body.Kind == "" {
		body.Kind = timerpi.KindQA // plain questions is the default verb
	}
	if body.Kind != timerpi.KindQA && body.Kind != timerpi.KindIdeas && body.Kind != timerpi.KindWordCloud {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unsupported submission kind"})
		return
	}
	if !askAllowed(body.Peer, time.Now().UnixMilli()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "sending too fast"})
		return
	}
	p, err := d.Store.Submit(id, body.Kind, body.Text, body.Peer, body.Parent)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": p.ID})
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
	if err := d.Store.Vote(id, body.PollID, body.Peer, body.Choice); err != nil {
		status := http.StatusBadRequest
		c.JSON(status, gin.H{"ok": false, "error": err.Error()})
		return
	}
	// Live counts on the board: one fanout per vote burst member; SQLite
	// absorbs the write, shows stay live.
	if eng, err := d.engineFor(id); err == nil {
		_ = eng.Notify()
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

func (d *Deps) apiPollList(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	ps, err := d.Store.ListPolls(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	views := make([]gin.H, 0, len(ps))
	for _, p := range ps {
		v := d.Store.PollCounts(p)
		views = append(views, gin.H{
			"id": p.ID, "kind": p.Kind, "question": p.Question,
			"options": v.Options, "state": p.State, "parent": p.Parent,
			"counts": v.Counts, "total": v.Total, "upvotes": v.Upvotes,
			"correct": p.Correct, "ts": p.Ts,
		})
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "polls": views})
}

func (d *Deps) apiPollCreate(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	var body struct {
		Kind     string   `json:"kind"`
		Question string   `json:"question"`
		Options  []string `json:"options"`
		Correct  *int64   `json:"correct"`
		Parent   int64    `json:"parent"`
		State    string   `json:"state"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	opts := "[]"
	if len(body.Options) > 0 {
		if b, err := jsonMarshal(body.Options); err == nil {
			opts = string(b)
		}
	}
	p := timerpi.Poll{ShowID: id, Kind: body.Kind, Question: body.Question,
		Options: opts, State: body.State, Parent: body.Parent}
	if body.Correct != nil {
		p.Correct = *body.Correct
	}
	created, err := d.Store.CreatePoll(p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.logAction(id, "pollCreate", fmt.Sprintf("%s %q", created.Kind, created.Question))
	if eng, gerr := d.engineFor(id); gerr == nil {
		_ = eng.Notify()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "id": created.ID})
}

func (d *Deps) apiPollSetState(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, perr := strconv.ParseInt(c.Param("pid"), 10, 64)
	if perr != nil || pid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad poll id"})
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
	if err := d.Store.SetPollState(id, pid, body.State); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.logAction(id, "pollState", fmt.Sprintf("%d → %s", pid, body.State))
	if eng, gerr := d.engineFor(id); gerr == nil {
		_ = eng.Notify()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "state": body.State})
}

func (d *Deps) apiPollDelete(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, perr := strconv.ParseInt(c.Param("pid"), 10, 64)
	if perr != nil || pid <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad poll id"})
		return
	}
	if err := d.Store.DeletePoll(id, pid); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	d.logAction(id, "pollDelete", fmt.Sprintf("%d", pid))
	if eng, gerr := d.engineFor(id); gerr == nil {
		_ = eng.Notify()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
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
	if !showGateByShowID(c, d, id) {
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
