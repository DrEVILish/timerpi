// audience.go — the audience interaction REST surface (Slido-style).
//
// AUDIENCE (unauthenticated, join by show code / QR): GET current item,
// vote, submit. Burst-safe by design: one HTTP round-trip per vote, a
// per-device guard on submissions, and the vote table's
// UNIQUE(poll_id,peer) makes double-tap a no-op replace. The device id is
// server-issued (audience_device.go); a body "peer" is ignored.
//
// OPERATOR (show-gated like every show mutation): create items, open
// voting, show results, delete, survey grouping.
package routes

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/skip2/go-qrcode"

	"timerpi/config"
	"timerpi/timerpi"
)

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
	ig.POST("/:pid/reset", d.apiPollReset)
	ig.GET("/export", d.apiPollExport)
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

// askGuard: a device may submit one contribution every 3 s; voteGuard:
// one vote per device per 300 ms (PLAN §11.5 rate guards). Votes are also
// idempotent by key (DB UNIQUE(poll_id, peer)).
var (
	askGuard  = &throttle{window: 3000, max: 20_000}
	voteGuard = &throttle{window: 300, max: 20_000}
)

// soak: per-show accept budget (requests/s) — beyond it the lane answers
// 429 and phones back off with jitter (PLAN §11.5).
const audSoakPerSec = 600

var soak = &windowLimiter{max: audSoakPerSec, window: time.Second, bound: 10_000}

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
	peer, ok := d.audiencePeer(c)
	if !ok {
		return
	}
	now := time.Now().UnixMilli()
	if !askGuard.take(peer, now) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "Sending too fast — wait a moment"})
		return
	}
	if !soak.allow(strconv.FormatInt(id, 10), time.UnixMilli(now)) { // per-room budget covers submissions too (RW4)
		askGuard.release(peer, now)
		c.Header("Retry-After", "1")
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "room is busy — try again in a moment"})
		return
	}
	p, err := d.Store.Submit(id, body.Item, body.Text, peer)
	if err != nil {
		askGuard.release(peer, now) // a refused submission never burns the window
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": strings.TrimPrefix(err.Error(), "timerpi: ")})
		return
	}
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
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad body"})
		return
	}
	if body.PollID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "pollId required"})
		return
	}
	peer, ok := d.audiencePeer(c)
	if !ok {
		return
	}
	now := time.Now().UnixMilli()
	if !voteGuard.take(peer, now) {
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "voting too fast"})
		return
	}
	if !soak.allow(strconv.FormatInt(id, 10), time.UnixMilli(now)) {
		voteGuard.release(peer, now)
		c.Header("Retry-After", "1")
		c.JSON(http.StatusTooManyRequests, gin.H{"ok": false, "error": "room is busy — try again in a moment"})
		return
	}
	if err := d.Store.Vote(id, body.PollID, peer, body.Choice); err != nil {
		voteGuard.release(peer, now) // a vote that fails validation never burns the window
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": strings.TrimPrefix(err.Error(), "timerpi: ")})
		return
	}
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
	if c.Request.TLS == nil && !strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	base := fmt.Sprintf("%s://%s", scheme, c.Request.Host)
	if cloud := config.CloudURL(); cloud != "" && !d.isCloud() {
		base = cloud // phones reach TimerPi only through the cloud (VENUE-CLOUD §1)
	}
	url := base + "/a/" + sh.Code
	png, err := qrcode.Encode(url, qrcode.Medium, 320)
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
	AutoApprove *bool    `json:"autoApprove"`
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
	opts, _ := json.Marshal(body.cleanOptions())
	p := timerpi.Poll{ShowID: id, Kind: body.Kind, Question: timerpi.ClipUTF8(body.Question, 200),
		Options: string(opts), Correct: -1, AutoApprove: body.AutoApprove != nil && *body.AutoApprove}
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
	// A PATCH changes only what it names (BUGLOG RS14): omitted fields keep
	// their stored values (an "approve automatically" toggle must not reset
	// the quiz answer, and vice versa).
	cur, err := d.Store.GetPoll(id, pid)
	if err != nil {
		d.pollResult(c, id, "edit", err)
		return
	}
	question := cur.Question
	if strings.TrimSpace(body.Question) != "" {
		question = timerpi.ClipUTF8(body.Question, 200)
	}
	correct := cur.Correct
	if body.Correct != nil {
		correct = *body.Correct
	}
	auto := cur.AutoApprove
	if body.AutoApprove != nil {
		auto = *body.AutoApprove
	}
	var opts []string
	if body.Options != nil {
		opts = body.cleanOptions()
	}
	d.pollResult(c, id, fmt.Sprintf("edit %d", pid), d.Store.UpdatePoll(id, pid, question, opts, correct, auto))
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
	if _, ok := d.audiencePeer(c); !ok { // issue the device cookie up front
		return
	}
	d.render(c, "audience", gin.H{
		"Page": "audience", "Show": gin.H{"Code": sh.Code, "Title": sh.Title},
		"DefaultTheme": config.DefaultTheme(),
	})
}

// POST /api/shows/:ident/polls/:pid/reset — clear an item's votes and
// submissions; the item and where it is shown stay.
func (d *Deps) apiPollReset(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	pid, ok := pollParam(c)
	if !ok {
		return
	}
	d.pollResult(c, id, fmt.Sprintf("clear %d", pid), d.Store.ResetPoll(id, pid))
}

// GET /api/shows/:ident/polls/export — every item's responses as CSV
// (one row per poll option, one per submission).
func (d *Deps) apiPollExport(c *gin.Context) {
	id, ok := d.requireShowGated(c)
	if !ok {
		return
	}
	rows, err := d.Store.ExportRows(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	title := "room"
	if sh, err := d.Store.GetShow(id); err == nil && sh.Title != "" {
		title = sh.Title
	}
	var buf bytes.Buffer
	buf.WriteString("\ufeff") // Excel reads the file as UTF-8
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"Item", "Type", "Question", "Response", "Count", "Status", "Time"})
	for _, r := range rows {
		ts := ""
		if r.Ts > 0 {
			ts = time.UnixMilli(r.Ts).Format("2006-01-02 15:04:05")
		}
		_ = w.Write([]string{strconv.FormatInt(r.ItemID, 10), r.Kind, csvCell(r.Question), csvCell(r.Response),
			strconv.FormatInt(r.Count, 10), r.Status, ts})
	}
	w.Flush()
	name := exportName(title) + "-audience-" + time.Now().Format("2006-01-02") + ".csv"
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

// csvCell keeps audience text from running as a spreadsheet formula.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// exportName is a file-name-safe version of the room title.
func exportName(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "room"
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}
