package timerpi

// polls.go — the audience interaction layer (PRODUCT §4.4), one table for
// every kind: poll, quiz, Q&A, word cloud, ideas.
//
// Top-level items (parent = 0) are what the moderator creates. Two push
// targets decide WHERE an item is on air:
//
//	to_audience   phones + the room's audience displays ("Show to Audience")
//	to_presenter  the room's presenter displays / DSM ("Show to Presenter")
//
// state is the phase: hidden (on no target) → open (voting / asking) →
// results (voting closed, results revealed wherever it is shown). At most
// one item per target per room is on air: pushing an item to a target
// takes it off that target for every other item.
//
// Audience contributions (Q&A questions, cloud words, ideas) are CHILD rows
// (parent = the item) with a moderation status in state:
//
//	hidden    pending moderation
//	open      approved (on the wall / in the cloud)
//	answered  approved and answered (Q&A; stays visible, greyed)
//	dismissed removed from the wall (kept for the record)
//
// A Q&A item can spotlight one approved question (spot = child id).
// Votes are one row per (item, device); upvotes on a question are votes on
// the child row with choice "1".

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Interaction kinds.
const (
	KindPoll      = "poll"
	KindQA        = "qa"
	KindWordCloud = "wordcloud"
	KindIdeas     = "ideas"
	KindQuiz      = "quiz"
)

// Item phases and child moderation states.
const (
	StateHidden    = "hidden"
	StateOpen      = "open"
	StateResults   = "results"
	StateAnswered  = "answered"
	StateDismissed = "dismissed"
)

// Push targets.
const (
	TargetAudience  = "audience"
	TargetPresenter = "presenter"
)

var pollKinds = map[string]bool{
	KindPoll: true, KindQA: true, KindWordCloud: true, KindIdeas: true, KindQuiz: true,
}

// takesSubmissions: kinds whose audience sends free text (children).
func takesSubmissions(kind string) bool {
	return kind == KindQA || kind == KindWordCloud || kind == KindIdeas
}

func (d *DB) migratePolls() error {
	cols := []struct{ name, ddl string }{
		{"to_audience", "INTEGER NOT NULL DEFAULT 0"},
		{"to_presenter", "INTEGER NOT NULL DEFAULT 0"},
		{"spot", "INTEGER NOT NULL DEFAULT 0"},
		{"auto_approve", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, nc := range cols {
		_, err := d.Exec(fmt.Sprintf(`ALTER TABLE polls ADD COLUMN %s %s;`, nc.name, nc.ddl))
		if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("timerpi: adding polls.%s: %w", nc.name, err)
		}
	}
	// Legacy rows: an item that was on air (open/results) under the old
	// single-target model was on the audience target.
	_, err := d.Exec(`UPDATE polls SET to_audience = 1 WHERE parent = 0 AND state IN ('open','results') AND to_audience = 0 AND to_presenter = 0`)
	if err != nil {
		return err
	}
	// Survey was never usable and is not a product kind: drop leftovers.
	_, err = d.Exec(`DELETE FROM polls WHERE kind = 'survey'`)
	return err
}

// Vote is one device's recorded choice (bundle round-trip).
type Vote struct {
	PollID int64  `db:"poll_id" json:"-"`
	Peer   string `db:"peer"    json:"peer"`
	Choice string `db:"choice"  json:"choice"`
	Ts     int64  `db:"ts"      json:"ts"`
}

// Poll is one interaction row (item or child).
type Poll struct {
	ID          int64  `db:"id"           json:"id"`
	ShowID      int64  `db:"show_id"      json:"-"`
	Kind        string `db:"kind"         json:"kind"`
	Question    string `db:"question"     json:"question"`
	Options     string `db:"options"      json:"-"` // JSON array (poll/quiz)
	Correct     int64  `db:"correct"      json:"-"` // quiz: correct option index; -1 none
	State       string `db:"state"        json:"state"`
	Parent      int64  `db:"parent"       json:"-"` // >0 = child row of that item
	Author      string `db:"author"       json:"-"` // submitting device (moderation only)
	Ts          int64  `db:"ts"           json:"ts"`
	Updated     int64  `db:"updated"      json:"-"`
	ToAudience  bool   `db:"to_audience"  json:"toAudience"`
	ToPresenter bool   `db:"to_presenter" json:"toPresenter"`
	Spot        int64  `db:"spot"         json:"-"`
	AutoApprove bool   `db:"auto_approve" json:"autoApprove"`
}

const pollCols = `id, show_id, kind, question, options, correct, state, parent, author, ts, updated,
	to_audience, to_presenter, spot, auto_approve`

// PollOptions decodes the options array (never nil).
func (p Poll) PollOptions() []string {
	var out []string
	_ = json.Unmarshal([]byte(p.Options), &out)
	if out == nil {
		out = []string{}
	}
	return out
}

// PollView is the wire shape for phones, screens and the moderator panel.
type PollView struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	Question    string     `json:"question"`
	Options     []string   `json:"options"`
	Correct     int64      `json:"correct"` // quiz: revealed only in results (-1 otherwise)
	State       string     `json:"state"`
	ToAudience  bool       `json:"toAudience,omitempty"`
	ToPresenter bool       `json:"toPresenter,omitempty"`
	AutoApprove bool       `json:"autoApprove,omitempty"`
	Counts      []int64    `json:"counts,omitempty"` // per option (poll/quiz)
	Total       int64      `json:"total"`            // distinct voters / submissions
	Upvotes     int64      `json:"upvotes,omitempty"`
	Parent      int64      `json:"parent,omitempty"`
	Children    []PollView `json:"children,omitempty"`  // wall / cloud entries
	Spotlight   *PollView  `json:"spotlight,omitempty"` // Q&A: the question in focus
	Pending     int        `json:"pending,omitempty"`   // moderator view: submissions waiting
	More        int        `json:"more,omitempty"`      // entries left out of a trimmed public view
}

// MaxPublicEntries caps the entries a phone or screen frame carries.
const MaxPublicEntries = 50

// Trimmed returns a copy of v carrying at most max entries: the most
// upvoted (newest first on ties), kept in their original order, with More
// counting the rest. The spotlight always stays. Every phone used to get
// every entry up to four times a second — tens of MB/s at 1,000 phones
// (BUGLOG RW53).
func (v *PollView) Trimmed(max int) *PollView {
	if v == nil || len(v.Children) <= max {
		return v
	}
	idx := make([]int, len(v.Children))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ca, cb := v.Children[idx[a]], v.Children[idx[b]]
		if ca.Upvotes != cb.Upvotes {
			return ca.Upvotes > cb.Upvotes
		}
		return ca.ID > cb.ID
	})
	keep := make(map[int]bool, max)
	for _, i := range idx[:max] {
		keep[i] = true
	}
	out := *v
	out.Children = make([]PollView, 0, max+1)
	for i, c := range v.Children {
		if keep[i] || (v.Spotlight != nil && c.ID == v.Spotlight.ID) {
			out.Children = append(out.Children, c)
		}
	}
	out.More = len(v.Children) - len(out.Children)
	return &out
}

// OnAir is what is showing in one room right now, per target.
type OnAir struct {
	Audience  *PollView `json:"audience"`
	Presenter *PollView `json:"presenter"`
}

func (d *DB) normalizePoll(p *Poll) error {
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	if !pollKinds[p.Kind] && !(p.Parent > 0 && p.Kind == "submission") {
		return fmt.Errorf("timerpi: interaction kind %q invalid", p.Kind)
	}
	p.Question = strings.TrimSpace(p.Question)
	if p.Question == "" {
		if p.Parent > 0 {
			return fmt.Errorf("timerpi: submission text required")
		}
		return fmt.Errorf("timerpi: question or prompt required")
	}
	if p.Options == "" || p.Options == "null" {
		p.Options = "[]"
	}
	if p.Parent == 0 && (p.Kind == KindPoll || p.Kind == KindQuiz) {
		if len(p.PollOptions()) < 2 {
			return fmt.Errorf("timerpi: a poll needs at least two options")
		}
	}
	if p.Kind == KindQuiz && p.Parent == 0 {
		if p.Correct < 0 || p.Correct >= int64(len(p.PollOptions())) {
			return fmt.Errorf("timerpi: pick the correct answer for the quiz")
		}
	} else if p.Parent == 0 {
		p.Correct = -1
	}
	return nil
}

// CreatePoll inserts a top-level item (always created hidden, off air).
func (d *DB) CreatePoll(p Poll) (Poll, error) {
	p.Parent = 0
	if err := d.normalizePoll(&p); err != nil {
		return Poll{}, err
	}
	now := nowMS()
	p.Ts, p.Updated, p.State = now, now, StateHidden
	p.ToAudience, p.ToPresenter, p.Spot = false, false, 0
	return d.insertPoll(p)
}

func (d *DB) insertPoll(p Poll) (Poll, error) {
	defer d.airDirty(p.ShowID)
	res, err := d.Exec(`INSERT INTO polls (show_id, kind, question, options, correct, state, parent, author, ts, updated,
		to_audience, to_presenter, spot, auto_approve)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ShowID, p.Kind, p.Question, p.Options, p.Correct, p.State, p.Parent, p.Author, p.Ts, p.Updated,
		b2i(p.ToAudience), b2i(p.ToPresenter), p.Spot, b2i(p.AutoApprove))
	if err != nil {
		return Poll{}, fmt.Errorf("timerpi: create poll: %w", err)
	}
	p.ID, _ = res.LastInsertId()
	return p, nil
}

// UpdatePoll edits an item's text/options/correct/auto-approve (not its
// air state). Editing an item that has votes keeps the votes.
func (d *DB) UpdatePoll(showID, id int64, question string, options []string, correct int64, autoApprove bool) error {
	defer d.airDirty(showID)
	p, err := d.GetPoll(showID, id)
	if err != nil {
		return err
	}
	if p.Parent != 0 {
		return fmt.Errorf("timerpi: submissions are moderated, not edited")
	}
	p.Question = question
	if options != nil {
		b, _ := json.Marshal(options)
		// Votes are stored by option position: changing the options under
		// existing votes moves them to other answers (BUGLOG RW21).
		if string(b) != p.Options {
			var n int64
			if err := d.Get(&n, `SELECT COUNT(*) FROM votes WHERE poll_id = ?`, id); err != nil {
				return err
			}
			if n > 0 {
				return fmt.Errorf("timerpi: this item already has votes; its answers can't change (duplicate it instead)")
			}
		}
		p.Options = string(b)
	}
	p.Correct = correct
	p.AutoApprove = autoApprove
	if err := d.normalizePoll(&p); err != nil {
		return err
	}
	_, err = d.Exec(`UPDATE polls SET question = ?, options = ?, correct = ?, auto_approve = ?, updated = ? WHERE id = ?`,
		p.Question, p.Options, p.Correct, b2i(p.AutoApprove), nowMS(), id)
	return err
}

// GetPoll fetches one row of the show.
func (d *DB) GetPoll(showID, id int64) (Poll, error) {
	var p Poll
	err := d.Get(&p, `SELECT `+pollCols+` FROM polls WHERE show_id = ? AND id = ?`, showID, id)
	if err != nil {
		return Poll{}, fmt.Errorf("timerpi: get poll: %w", err)
	}
	return p, nil
}

// ListPolls returns every row of the show (items and children), newest
// first.
func (d *DB) ListPolls(showID int64) ([]Poll, error) {
	var out []Poll
	err := d.Select(&out, `SELECT `+pollCols+` FROM polls WHERE show_id = ? ORDER BY updated DESC, id DESC`, showID)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list polls: %w", err)
	}
	if out == nil {
		out = []Poll{}
	}
	return out, nil
}

// ListItems returns the show's top-level items in creation order.
func (d *DB) ListItems(showID int64) ([]Poll, error) {
	var out []Poll
	err := d.Select(&out, `SELECT `+pollCols+` FROM polls WHERE show_id = ? AND parent = 0 ORDER BY id`, showID)
	if out == nil {
		out = []Poll{}
	}
	return out, err
}

func targetCol(target string) (string, error) {
	switch target {
	case TargetAudience:
		return "to_audience", nil
	case TargetPresenter:
		return "to_presenter", nil
	}
	return "", fmt.Errorf("timerpi: unknown target %q", target)
}

// ShowTo puts an item on (on=true) or takes it off one target. Putting it
// on takes every other item of the room off that target. An item on no
// target is hidden; an item coming on air from hidden opens for voting.
func (d *DB) ShowTo(showID, id int64, target string, on bool) error {
	defer d.airDirty(showID)
	col, err := targetCol(target)
	if err != nil {
		return err
	}
	p, err := d.GetPoll(showID, id)
	if err != nil {
		return err
	}
	if p.Parent != 0 {
		return fmt.Errorf("timerpi: only items go on air (submissions are moderated)")
	}
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowMS()
	if on {
		if _, err := tx.Exec(`UPDATE polls SET `+col+` = 0, updated = ? WHERE show_id = ? AND parent = 0 AND id != ? AND `+col+` = 1`, now, showID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE polls SET `+col+` = 1, state = CASE WHEN state = ? THEN ? ELSE state END, updated = ? WHERE id = ?`,
			StateHidden, StateOpen, now, id); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`UPDATE polls SET `+col+` = 0, updated = ? WHERE id = ?`, now, id); err != nil {
			return err
		}
	}
	// Anything now on no target is hidden.
	if _, err := tx.Exec(`UPDATE polls SET state = ?, spot = 0 WHERE show_id = ? AND parent = 0 AND to_audience = 0 AND to_presenter = 0 AND state != ?`,
		StateHidden, showID, StateHidden); err != nil {
		return err
	}
	return tx.Commit()
}

// SetResults reveals (on) or re-closes (off) an item's results wherever it
// is shown. Results close voting.
func (d *DB) SetResults(showID, id int64, on bool) error {
	defer d.airDirty(showID)
	p, err := d.GetPoll(showID, id)
	if err != nil {
		return err
	}
	if p.Parent != 0 {
		return fmt.Errorf("timerpi: submissions have no results")
	}
	if !p.ToAudience && !p.ToPresenter {
		return fmt.Errorf("timerpi: show the item first, then its results")
	}
	state := StateOpen
	if on {
		state = StateResults
	}
	_, err = d.Exec(`UPDATE polls SET state = ?, updated = ? WHERE id = ?`, state, nowMS(), id)
	return err
}

// HidePoll takes an item off every target.
func (d *DB) HidePoll(showID, id int64) error {
	defer d.airDirty(showID)
	p, err := d.GetPoll(showID, id)
	if err != nil {
		return err
	}
	if p.Parent != 0 {
		return fmt.Errorf("timerpi: use moderation for submissions")
	}
	_, err = d.Exec(`UPDATE polls SET to_audience = 0, to_presenter = 0, state = ?, spot = 0, updated = ? WHERE id = ?`,
		StateHidden, nowMS(), id)
	return err
}

// SetPollState is the legacy single-verb transport kept for automation
// (and the old REST shape): hidden → off every target; open/results → on
// the audience target (plus results).
func (d *DB) SetPollState(showID, id int64, state string) error {
	switch state {
	case StateHidden:
		return d.HidePoll(showID, id)
	case StateOpen, StateResults:
		p, err := d.GetPoll(showID, id)
		if err != nil {
			return err
		}
		if p.Parent != 0 {
			return d.Moderate(showID, id, StateOpen)
		}
		if !p.ToAudience && !p.ToPresenter {
			if err := d.ShowTo(showID, id, TargetAudience, true); err != nil {
				return err
			}
		}
		return d.SetResults(showID, id, state == StateResults)
	}
	return fmt.Errorf("timerpi: poll state %q invalid", state)
}

// Moderate sets a submission's status: hidden (pending), open (approved),
// answered, dismissed. Approving a cloud word approves every identical
// word under the same item (one decision per word, not per submitter).
func (d *DB) Moderate(showID, childID int64, status string) error {
	defer d.airDirty(showID)
	switch status {
	case StateHidden, StateOpen, StateAnswered, StateDismissed:
	default:
		return fmt.Errorf("timerpi: moderation status %q invalid", status)
	}
	c, err := d.GetPoll(showID, childID)
	if err != nil {
		return err
	}
	if c.Parent == 0 {
		return fmt.Errorf("timerpi: not a submission")
	}
	parent, err := d.GetPoll(showID, c.Parent)
	if err != nil {
		return err
	}
	now := nowMS()
	if parent.Kind == KindWordCloud {
		// Every copy of the word, matched exactly like the views group
		// them (Go's Unicode lowercase; SQLite lower() is ASCII-only, so
		// "Été" and "été" used to be split, BUGLOG RW20).
		ids, serr := d.sameWordIDs(c.Parent, c.Question)
		if serr != nil {
			return serr
		}
		for _, sid := range ids {
			if _, err = d.Exec(`UPDATE polls SET state = ?, updated = ? WHERE id = ?`, status, now, sid); err != nil {
				return err
			}
		}
	} else {
		_, err = d.Exec(`UPDATE polls SET state = ?, updated = ? WHERE id = ?`, status, now, childID)
	}
	if err != nil {
		return err
	}
	// A spotlighted question that leaves the wall leaves the spotlight.
	if status != StateOpen {
		_, err = d.Exec(`UPDATE polls SET spot = 0 WHERE id = ? AND spot = ?`, c.Parent, childID)
	}
	return err
}

// Spotlight puts one approved question of a Q&A item in focus (0 clears).
func (d *DB) Spotlight(showID, itemID, childID int64) error {
	defer d.airDirty(showID)
	item, err := d.GetPoll(showID, itemID)
	if err != nil {
		return err
	}
	if item.Parent != 0 || item.Kind != KindQA {
		return fmt.Errorf("timerpi: only a Q&A item has a spotlight")
	}
	if childID > 0 {
		c, err := d.GetPoll(showID, childID)
		if err != nil {
			return err
		}
		if c.Parent != itemID {
			return fmt.Errorf("timerpi: that question belongs to another item")
		}
		if c.State == StateHidden || c.State == StateDismissed {
			if err := d.Moderate(showID, childID, StateOpen); err != nil {
				return err
			}
		}
	}
	_, err = d.Exec(`UPDATE polls SET spot = ?, updated = ? WHERE id = ?`, childID, nowMS(), itemID)
	return err
}

// DeletePoll removes one row (votes and children cascade).
func (d *DB) DeletePoll(showID, id int64) error {
	defer d.airDirty(showID)
	res, err := d.Exec(`DELETE FROM polls WHERE show_id = ? AND id = ?`, showID, id)
	if err != nil {
		return fmt.Errorf("timerpi: delete poll: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	_, _ = d.Exec(`DELETE FROM polls WHERE show_id = ? AND parent = ?`, showID, id)
	return nil
}

// Vote records one device's choice on an open poll/quiz (replacing its old
// vote — devices may change their mind until results), or an upvote
// (choice "1") on an approved Q&A question / idea.
func (d *DB) Vote(showID, pollID int64, peer, choice string) error {
	defer d.airDirty(showID)
	if strings.TrimSpace(peer) == "" {
		return fmt.Errorf("timerpi: vote needs a device id")
	}
	p, err := d.GetPoll(showID, pollID)
	if err != nil {
		return err
	}
	if p.Parent > 0 {
		parent, err := d.GetPoll(showID, p.Parent)
		if err != nil {
			return err
		}
		if !parent.ToAudience || parent.State != StateOpen {
			return fmt.Errorf("timerpi: voting is not open")
		}
		if p.State != StateOpen {
			return fmt.Errorf("timerpi: only approved entries can be upvoted")
		}
		choice = "1"
	} else {
		if !p.ToAudience || p.State != StateOpen {
			return fmt.Errorf("timerpi: voting is not open")
		}
		if p.Kind != KindPoll && p.Kind != KindQuiz {
			return fmt.Errorf("timerpi: this item takes submissions, not votes")
		}
		idx, perr := atoi64(choice)
		if perr != nil || idx < 0 || idx >= int64(len(p.PollOptions())) {
			return fmt.Errorf("timerpi: choice out of range")
		}
	}
	if _, err := d.Exec(`INSERT INTO votes (poll_id, peer, choice, ts) VALUES (?, ?, ?, ?)
		ON CONFLICT (poll_id, peer) DO UPDATE SET choice = excluded.choice, ts = excluded.ts`,
		pollID, peer, choice, nowMS()); err != nil {
		return fmt.Errorf("timerpi: vote: %w", err)
	}
	return nil
}

// Submit records an audience contribution to an item that is open on the
// audience target and takes submissions. It lands pending unless the item
// auto-approves.
func (d *DB) Submit(showID, itemID int64, text, peer string) (Poll, error) {
	if peer == "" {
		return Poll{}, fmt.Errorf("timerpi: submit needs a device id")
	}
	item, err := d.GetPoll(showID, itemID)
	if err != nil {
		return Poll{}, err
	}
	if item.Parent != 0 || !takesSubmissions(item.Kind) || !item.ToAudience || item.State != StateOpen {
		return Poll{}, fmt.Errorf("timerpi: this item is not taking submissions")
	}
	limit := 280
	if item.Kind == KindWordCloud {
		limit = 32
		text = strings.Join(strings.Fields(text), " ")
	}
	text = ClipUTF8(strings.TrimSpace(text), limit)
	if text == "" {
		return Poll{}, fmt.Errorf("timerpi: submission text required")
	}
	state := StateHidden
	if item.AutoApprove {
		state = StateOpen
	}
	if item.Kind == KindWordCloud {
		// A word the moderator already approved (or dismissed) keeps that
		// decision for every later copy (BUGLOG RW19).
		if st, ok := d.decidedWordState(itemID, text); ok {
			state = st
		}
	}
	now := nowMS()
	return d.insertPoll(Poll{ShowID: showID, Kind: "submission", Question: text, Options: "[]", Correct: -1,
		State: state, Parent: itemID, Author: peer, Ts: now, Updated: now})
}

// wordKey is how word-cloud words are matched and grouped.
func wordKey(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// sameWordIDs lists the item's submissions equal to word (wordKey).
func (d *DB) sameWordIDs(itemID int64, word string) ([]int64, error) {
	var rows []struct {
		ID       int64  `db:"id"`
		Question string `db:"question"`
	}
	if err := d.Select(&rows, `SELECT id, question FROM polls WHERE parent = ?`, itemID); err != nil {
		return nil, err
	}
	key := wordKey(word)
	var ids []int64
	for _, r := range rows {
		if wordKey(r.Question) == key {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}

// decidedWordState is the moderator's decision on an earlier copy of the
// word: approved wins over dismissed; ok=false when none was decided.
func (d *DB) decidedWordState(itemID int64, word string) (string, bool) {
	var rows []struct {
		Question string `db:"question"`
		State    string `db:"state"`
	}
	if err := d.Select(&rows, `SELECT question, state FROM polls WHERE parent = ? AND state IN (?, ?)`,
		itemID, StateOpen, StateDismissed); err != nil {
		return "", false
	}
	key, found := wordKey(word), ""
	for _, r := range rows {
		if wordKey(r.Question) != key {
			continue
		}
		if r.State == StateOpen {
			return StateOpen, true
		}
		found = r.State
	}
	return found, found != ""
}

// ---------------------------------------------------------------------------
// Views

// itemView builds the view of one top-level item. moderator=true adds
// pending entries and keeps the quiz answer visible.
func (d *DB) itemView(p Poll, moderator bool) PollView {
	v := PollView{ID: p.ID, Kind: p.Kind, Question: p.Question, Options: p.PollOptions(),
		State: p.State, ToAudience: p.ToAudience, ToPresenter: p.ToPresenter, AutoApprove: p.AutoApprove, Correct: -1}
	if p.Kind == KindQuiz && (moderator || p.State == StateResults) {
		v.Correct = p.Correct
	}
	switch p.Kind {
	case KindPoll, KindQuiz:
		var rows []struct {
			Choice string `db:"choice"`
			N      int64  `db:"n"`
		}
		_ = d.Select(&rows, `SELECT choice, COUNT(*) n FROM votes WHERE poll_id = ? GROUP BY choice`, p.ID)
		v.Counts = make([]int64, len(v.Options))
		for _, r := range rows {
			v.Total += r.N
			if idx, err := atoi64(r.Choice); err == nil && idx >= 0 && idx < int64(len(v.Counts)) {
				v.Counts[idx] += r.N
			}
		}
		// Phones and screens see the vote total while voting runs, but the
		// per-option tally only once results are shown (BUGLOG RW17): the
		// frame used to carry it, so anyone reading it saw the crowd's
		// (or the quiz's) answer before the reveal.
		if !moderator && p.State != StateResults {
			v.Counts = nil
		}
	default:
		v.Children = d.childViews(p, moderator)
		for _, c := range v.Children {
			if c.State == StateHidden {
				v.Pending++
			}
			if p.Spot > 0 && c.ID == p.Spot {
				cc := c
				v.Spotlight = &cc
			}
		}
		var total int64
		if moderator {
			_ = d.Get(&total, `SELECT COUNT(*) FROM polls WHERE parent = ?`, p.ID)
		} else {
			// Phones and screens count what they can see: pending and
			// dismissed entries must not show up in the number either
			// (BUGLOG RS12).
			_ = d.Get(&total, `SELECT COUNT(*) FROM polls WHERE parent = ? AND state IN (?, ?)`, p.ID, StateOpen, StateAnswered)
		}
		v.Total = total
	}
	return v
}

// childViews lists an item's entries. Public views carry approved and
// answered entries only; word clouds aggregate identical words.
func (d *DB) childViews(p Poll, moderator bool) []PollView {
	var rows []struct {
		ID       int64  `db:"id"`
		Question string `db:"question"`
		State    string `db:"state"`
		Ts       int64  `db:"ts"`
		Upvotes  int64  `db:"n"`
	}
	_ = d.Select(&rows, `SELECT p.id, p.question, p.state, p.ts, COUNT(v.id) n
		FROM polls p LEFT JOIN votes v ON v.poll_id = p.id
		WHERE p.parent = ? GROUP BY p.id ORDER BY p.id`, p.ID)
	out := []PollView{}
	if p.Kind == KindWordCloud {
		type agg struct {
			view PollView
			n    int64
		}
		words := map[string]*agg{}
		var order []string
		for _, r := range rows {
			if !moderator && r.State != StateOpen {
				continue
			}
			key := wordKey(r.Question) + "|" + r.State
			if a, ok := words[key]; ok {
				a.n++
				continue
			}
			words[key] = &agg{view: PollView{ID: r.ID, Kind: "word", Question: r.Question, State: r.State}, n: 1}
			order = append(order, key)
		}
		for _, k := range order {
			a := words[k]
			a.view.Upvotes = a.n // a word's weight = how many people sent it
			out = append(out, a.view)
		}
	} else {
		for _, r := range rows {
			if !moderator && r.State != StateOpen && r.State != StateAnswered {
				continue
			}
			if r.State == StateDismissed && !moderator {
				continue
			}
			out = append(out, PollView{ID: r.ID, Kind: "entry", Question: r.Question, State: r.State, Upvotes: r.Upvotes})
		}
	}
	// Loudest first; answered questions sink below open ones.
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].State == StateAnswered, out[j].State == StateAnswered
		if ai != aj {
			return !ai
		}
		return out[i].Upvotes > out[j].Upvotes
	})
	return out
}

// OnAirNow returns what is showing on each target in the room. It is
// cached per room and rebuilt only after a poll write (airDirty), so a
// reconnect storm of phones and the hub's 250 ms broadcasts no longer
// rebuild it from the database every time (BUGLOG RW54). If a rebuild
// fails, the last good value is served instead of "nothing on air", so a
// busy database never drops the vote off every phone (RW18).
func (d *DB) OnAirNow(showID int64) (OnAir, error) {
	d.air.mu.Lock()
	e := d.air.m[showID]
	if e != nil && e.valid {
		v := e.v
		d.air.mu.Unlock()
		return v, nil
	}
	gen := d.air.gen[showID]
	d.air.mu.Unlock()

	v, err := d.onAirQuery(showID)

	d.air.mu.Lock()
	defer d.air.mu.Unlock()
	if err != nil {
		if e != nil && e.have {
			return e.v, nil // stale but real beats a false "nothing on air"
		}
		return v, err
	}
	if d.air.gen[showID] == gen { // no write landed while we read
		if d.air.m == nil {
			d.air.m = map[int64]*airEntry{}
		}
		d.air.m[showID] = &airEntry{v: v, valid: true, have: true}
	}
	return v, nil
}

// airDirty marks a room's on-air cache stale after a poll write.
func (d *DB) airDirty(showID int64) {
	d.air.mu.Lock()
	defer d.air.mu.Unlock()
	if d.air.gen == nil {
		d.air.gen = map[int64]uint64{}
	}
	d.air.gen[showID]++
	if e := d.air.m[showID]; e != nil {
		e.valid = false
	}
}

// onAirQuery builds the on-air view from the database.
func (d *DB) onAirQuery(showID int64) (OnAir, error) {
	var out OnAir
	var items []Poll
	err := d.Select(&items, `SELECT `+pollCols+` FROM polls WHERE show_id = ? AND parent = 0 AND (to_audience = 1 OR to_presenter = 1)`, showID)
	if err != nil {
		return out, err
	}
	for _, p := range items {
		v := d.itemView(p, false)
		if p.ToAudience {
			vv := v
			out.Audience = &vv
		}
		if p.ToPresenter {
			vv := v
			out.Presenter = &vv
		}
	}
	return out, nil
}

// ActivePoll is the audience-target item (phones, audience displays).
func (d *DB) ActivePoll(showID int64) (*PollView, error) {
	on, err := d.OnAirNow(showID)
	return on.Audience, err
}

// ModeratorItems is the moderator panel's list: every item with counts,
// all entries (pending included) and air state.
func (d *DB) ModeratorItems(showID int64) ([]PollView, error) {
	items, err := d.ListItems(showID)
	if err != nil {
		return nil, err
	}
	out := make([]PollView, 0, len(items))
	for _, p := range items {
		out = append(out, d.itemView(p, true))
	}
	return out, nil
}

// PollCounts is the moderator-grade view of one row (kept for callers that
// want counts for a single item).
func (d *DB) PollCounts(p Poll) PollView {
	if p.Parent != 0 {
		return PollView{ID: p.ID, Kind: p.Kind, Question: p.Question, State: p.State, Parent: p.Parent, Correct: -1}
	}
	return d.itemView(p, true)
}

// AudienceVisible is what an audience device sees: only the item on the
// audience target (hidden items are absent, not concealed).
type AudienceVisible struct {
	Poll *PollView `json:"poll,omitempty"`
}

// AudienceRead assembles the audience wire view for one room.
func (d *DB) AudienceRead(showID int64) (*AudienceVisible, error) {
	active, err := d.ActivePoll(showID)
	if err != nil {
		return nil, err
	}
	return &AudienceVisible{Poll: active.Trimmed(MaxPublicEntries)}, nil // RW53
}

func atoi64(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}
