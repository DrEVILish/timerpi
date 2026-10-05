package timerpi

// polls.go — the audience interaction layer (Slido-style, per-show):
// one table carries all six kinds — Live Poll, Live Q&A, Word Cloud,
// Ideas, Quiz, Survey. Free-text contributions (questions/ideas/cloud
// words/ideas) are rows themselves (moderated: state hidden until the
// operator shows them); votes are one row per (poll, device), replaced on
// change, so counts always read true.
//
// State machine per poll row: hidden → open (audience sees it / can vote)
// → results (board graphs). One open/results row per show: opening one
// closes the others of that show (the message show-now pattern).

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	KindSurvey    = "survey"
)

// Poll states.
const (
	StateHidden  = "hidden"
	StateOpen    = "open"    // visible + votable + board shows it live
	StateResults = "results" // board shows the result graph
)

var pollKinds = map[string]bool{
	KindPoll: true, KindQA: true, KindWordCloud: true,
	KindIdeas: true, KindQuiz: true, KindSurvey: true,
}

// Poll is one interaction item.
type Poll struct {
	ID       int64  `db:"id"       json:"id"`
	ShowID   int64  `db:"show_id"  json:"-"`
	Kind     string `db:"kind"     json:"kind"`
	Question string `db:"question" json:"question"`
	Options  string `db:"options"  json:"-"` // JSON array (poll/quiz); [] = free-form
	Correct  int64  `db:"correct"  json:"-"` // quiz: index of the correct option; -1 none
	State    string `db:"state"    json:"state"`
	Parent   int64  `db:"parent"   json:"-"` // survey grouping: >0 = member of that survey row
	Author   string `db:"author"   json:"-"` // submitting device peer (moderation only)
	Ts       int64  `db:"ts"       json:"ts"`
	Updated  int64  `db:"updated"  json:"-"`
}

// PollOptions decodes the options array (never nil).
func (p Poll) PollOptions() []string {
	var out []string
	_ = json.Unmarshal([]byte(p.Options), &out)
	return out
}

// PollView is the audience/board wire shape (counts included when visible).
type PollView struct {
	ID       int64      `json:"id"`
	Kind     string     `json:"kind"`
	Question string     `json:"question"`
	Options  []string   `json:"options"`
	Correct  int64      `json:"correct,omitempty"`
	State    string     `json:"state"`
	Counts   []int64    `json:"counts,omitempty"` // per option (poll/quiz)
	Total    int64      `json:"total,omitempty"`  // distinct voters
	Upvotes  int64      `json:"upvotes,omitempty"`
	Parent   int64      `json:"parent,omitempty"`
	Children []PollView `json:"children,omitempty"` // wordcloud/ideas: approved words (PLAN §11.2)
}

func (d *DB) normalizePoll(p *Poll) error {
	p.Kind = strings.ToLower(strings.TrimSpace(p.Kind))
	if !pollKinds[p.Kind] {
		return fmt.Errorf("timerpi: poll kind %q invalid", p.Kind)
	}
	p.Question = strings.TrimSpace(p.Question)
	if p.Kind == KindPoll || p.Kind == KindQuiz {
		if p.Question == "" {
			return fmt.Errorf("timerpi: poll question required")
		}
	}
	if p.Correct < -1 {
		p.Correct = -1
	}
	switch p.Kind {
	case KindPoll, KindQuiz:
		if p.Options == "" || p.Options == "[]" || p.Options == "null" {
			return fmt.Errorf("timerpi: poll options required")
		}
	case KindQA, KindWordCloud, KindIdeas:
		if p.Question == "" {
			return fmt.Errorf("timerpi: submission text required")
		}
	}
	switch p.State {
	case StateHidden, StateOpen, StateResults:
	default:
		p.State = StateHidden
	}
	return nil
}

// CreatePoll inserts one interaction item (operator or audience-submit).
func (d *DB) CreatePoll(p Poll) (Poll, error) {
	if err := d.normalizePoll(&p); err != nil {
		return Poll{}, err
	}
	now := nowMS()
	p.Ts, p.Updated, p.State = now, now, StateHidden
	res, err := d.Exec(`INSERT INTO polls (show_id, kind, question, options, correct, state, parent, author, ts, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ShowID, p.Kind, p.Question, p.Options, p.Correct, p.State, p.Parent, p.Author, p.Ts, p.Updated)
	if err != nil {
		return Poll{}, fmt.Errorf("timerpi: create poll: %w", err)
	}
	p.ID, _ = res.LastInsertId()
	return p, nil
}

// GetPoll fetches one row.
func (d *DB) GetPoll(showID, id int64) (Poll, error) {
	var p Poll
	err := d.Get(&p, `SELECT id, show_id, kind, question, options, correct, state, parent, author, ts, updated
		FROM polls WHERE show_id = ? AND id = ?`, showID, id)
	if err != nil {
		return Poll{}, fmt.Errorf("timerpi: get poll: %w", err)
	}
	return p, nil
}

// ListPolls returns the show's items, newest first (operator panel).
func (d *DB) ListPolls(showID int64) ([]Poll, error) {
	var out []Poll
	err := d.Select(&out, `SELECT id, show_id, kind, question, options, correct, state, parent, author, ts, updated
		FROM polls WHERE show_id = ? ORDER BY updated DESC, id DESC`, showID)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list polls: %w", err)
	}
	if out == nil {
		out = []Poll{}
	}
	return out, nil
}

// SetPollState moves one row, fanning state. Single-focus applies to
// TOP-LEVEL items only (one open/results per show — board + audience keep
// one focus). Child rows (wordcloud/ideas submissions) accumulate: the
// operator approves words one by one while their cloud stays on air
// (PLAN §11.2 — found by the children test, the flat rule made clouds
// impossible).
func (d *DB) SetPollState(showID, id int64, state string) error {
	switch state {
	case StateHidden, StateOpen, StateResults:
	default:
		return fmt.Errorf("timerpi: poll state %q invalid", state)
	}
	p, err := d.GetPoll(showID, id)
	if err != nil {
		return err
	}
	if state != StateHidden && p.Parent == 0 {
		if _, err := d.Exec(`UPDATE polls SET state = ?, updated = ? WHERE show_id = ? AND state IN (?, ?) AND parent = 0`,
			StateHidden, nowMS(), showID, StateOpen, StateResults); err != nil {
			return fmt.Errorf("timerpi: close polls: %w", err)
		}
	}
	if _, err := d.Exec(`UPDATE polls SET state = ?, updated = ? WHERE id = ?`, state, nowMS(), id); err != nil {
		return fmt.Errorf("timerpi: set poll state: %w", err)
	}
	return nil
}

// DeletePoll removes one row (votes cascade).
func (d *DB) DeletePoll(showID, id int64) error {
	res, err := d.Exec(`DELETE FROM polls WHERE show_id = ? AND id = ?`, showID, id)
	if err != nil {
		return fmt.Errorf("timerpi: delete poll: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// Vote records one device's choice on a poll/quiz item (replaces its old
// vote — audience may change their mind until results are shown). choice
// is the option INDEX ("2"). Question upvotes ride the same table with
// choice "1" on the qa row.
func (d *DB) Vote(showID, pollID int64, peer, choice string) error {
	if strings.TrimSpace(peer) == "" {
		return fmt.Errorf("timerpi: vote needs a device id")
	}
	p, err := d.GetPoll(showID, pollID)
	if err != nil {
		return err
	}
	if p.State != StateOpen {
		return fmt.Errorf("timerpi: voting is not open")
	}
	if p.Kind == KindPoll || p.Kind == KindQuiz {
		opts := p.PollOptions()
		n := int64(len(opts))
		idx, perr := atoi64(choice)
		if perr != nil || idx < 0 || idx >= n {
			return fmt.Errorf("timerpi: choice out of range")
		}
	}
	if _, err := d.Exec(`INSERT INTO votes (poll_id, peer, choice, ts) VALUES (?, ?, ?, ?)
		ON CONFLICT (poll_id, peer) DO UPDATE SET choice = ?, ts = ?`,
		pollID, peer, choice, nowMS(), choice, nowMS()); err != nil {
		return fmt.Errorf("timerpi: vote: %w", err)
	}
	return nil
}

// Submit records an audience free-text contribution (qa question / idea /
// wordcloud word): a new moderated poll row authored by the device.
func (d *DB) Submit(showID int64, kind string, text, peer string, parent int64) (Poll, error) {
	if peer == "" {
		return Poll{}, fmt.Errorf("timerpi: submit needs a device id")
	}
	if len(text) > 280 { // a question, not an essay
		text = text[:280]
	}
	return d.CreatePoll(Poll{ShowID: showID, Kind: kind, Question: text, Parent: parent, Author: peer})
}

// PollCounts computes the per-option tallies + distinct voters for a
// visible item (open or results). qa rows count upvotes instead.
func (d *DB) PollCounts(p Poll) PollView {
	v := PollView{ID: p.ID, Kind: p.Kind, Question: p.Question,
		Options: p.PollOptions(), State: p.State, Correct: p.Correct, Parent: p.Parent}
	var rows []struct {
		Choice string `db:"choice"`
		N      int64  `db:"n"`
	}
	_ = d.Select(&rows, `SELECT choice, COUNT(*) n FROM votes WHERE poll_id = ? GROUP BY choice`, p.ID)
	if p.Kind == KindQA || p.Kind == KindIdeas {
		for _, r := range rows {
			v.Upvotes += r.N
		}
		return v
	}
	var counts []int64
	var have map[int64]int64
	var total int64
	for _, r := range rows {
		total += r.N
		if idx, err := atoi64(r.Choice); err == nil {
			if have == nil {
				have = map[int64]int64{}
			}
			have[idx] += r.N
		}
	}
	counts = make([]int64, len(v.Options))
	for i := range counts {
		counts[i] = have[int64(i)]
	}
	v.Counts, v.Total = counts, total
	return v
}

// ActivePoll returns the show's on-air item (state open/results) with
// counts — the board/Audience reads this out of the snapshot.
func (d *DB) ActivePoll(showID int64) (*PollView, error) {
	var p Poll
	// Top-level items only: a just-approved word must not steal the on-air
	// focus from its cloud (children surface through the parent's payload).
	err := d.Get(&p, `SELECT id, show_id, kind, question, options, correct, state, parent, author, ts, updated
		FROM polls WHERE show_id = ? AND state IN (?, ?) AND parent = 0 ORDER BY updated DESC LIMIT 1`,
		showID, StateOpen, StateResults)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	v := d.PollCounts(p)
	// Word clouds / idea walls carry their approved words as children
	// (PLAN §11.2): rows parented to this item that the operator opened.
	if p.Kind == KindWordCloud || p.Kind == KindIdeas {
		v.Children, _ = d.visibleChildren(showID, p.ID)
	}
	return &v, nil
}

// visibleChildren returns the open child rows of one item with their
// upvote counts, loudest first (word tiles / idea cards).
func (d *DB) visibleChildren(showID, parent int64) ([]PollView, error) {
	var rows []struct {
		ID       int64  `db:"id"`
		Question string `db:"question"`
		Upvotes  int64  `db:"n"`
	}
	err := d.Select(&rows, `SELECT p.id, p.question, COUNT(v.id) n
		FROM polls p LEFT JOIN votes v ON v.poll_id = p.id AND v.choice = '1'
		WHERE p.show_id = ? AND p.parent = ? AND p.state = ?
		GROUP BY p.id, p.question ORDER BY n DESC, p.id`, showID, parent, StateOpen)
	if err != nil {
		return nil, err
	}
	out := make([]PollView, 0, len(rows))
	for _, r := range rows {
		out = append(out, PollView{ID: r.ID, Kind: "word", Question: r.Question, Upvotes: r.Upvotes})
	}
	return out, nil
}

// ListOpenSurvey returns the survey members in author order when their
// survey header row is open (audience walks them as one flow).
func (d *DB) ListOpenSurvey(showID, surveyID int64) ([]PollView, error) {
	var rows []Poll
	err := d.Select(&rows, `SELECT id, show_id, kind, question, options, correct, state, parent, author, ts, updated
		FROM polls WHERE parent = ? ORDER BY id`, surveyID)
	if err != nil {
		return nil, err
	}
	out := make([]PollView, 0, len(rows))
	for _, p := range rows {
		if p.State != StateHidden {
			out = append(out, d.PollCounts(p))
		}
	}
	return out, nil
}

// AudienceVisible is what an audience device sees on GET: the active item
// (votable) plus — when a survey flow is on — its member list.
type AudienceVisible struct {
	Poll   *PollView  `json:"poll,omitempty"`
	Survey []PollView `json:"survey,omitempty"`
	Asking bool       `json:"asking,omitempty"` // qa/ideas/cloud intake open?
}

// AudienceRead assembles the audience wire view for one show.
func (d *DB) AudienceRead(showID int64) (*AudienceVisible, error) {
	active, err := d.ActivePoll(showID)
	if err != nil {
		return nil, err
	}
	out := AudienceVisible{Poll: active}
	if active != nil && active.Kind == KindSurvey {
		sv, err := d.ListOpenSurvey(showID, active.ID)
		if err == nil {
			out.Survey = sv
		}
	} else if active == nil {
		// No focus item: is there a survey any member still open? (results
		// walk-off) — cheap: any rows with parent>0 visible.
		var survey Poll
		serr := d.Get(&survey, `SELECT id FROM polls WHERE show_id = ? AND state = ? AND kind = ? LIMIT 1`,
			showID, StateOpen, KindSurvey)
		if serr == nil {
			sv, e2 := d.ListOpenSurvey(showID, survey.ID)
			if e2 == nil && len(sv) > 0 {
				sv0 := d.PollCounts(survey)
				sv0.State = survey.State
				out.Poll = &sv0
				out.Survey = sv
			}
		}
	}
	// Intake surfaces stay live whenever nothing else dominates.
	out.Asking = true
	return &out, nil
}

func atoi64(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}
