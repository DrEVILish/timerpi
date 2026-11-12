package timerpi

import (
	"fmt"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Operator action log (E4: who did what — multi-operator accountability +
// post-show review). Append-only, capped per show; the dashboard renders
// the tail live via the frag-actions oob.

// ClipUTF8 truncates s to at most n BYTES without splitting a multi-byte
// rune (byte-slicing stored text produced invalid UTF-8 / U+FFFD tails).
// MaxNameLen caps event and room names in characters: they used to be
// only trimmed, so an 8 MiB name was stored (BUGLOG RS7); 80 keeps them
// on one header line.
const MaxNameLen = 80

// Operator text caps (characters), applied where cues and messages are
// stored, whichever path wrote them (HTTP, WS, sync, import).
const (
	MaxCueLabelLen   = 200
	MaxCueSpeakerLen = 120
	MaxCueNotesLen   = 2000
	MaxMessageLen    = 280
)

// ClipRunes truncates s to at most n characters (runes).
func ClipRunes(s string, n int) string {
	i := 0
	for p := range s {
		if i == n {
			return s[:p]
		}
		i++
	}
	return s
}

// capCueText clips a cue's free text to the operator caps.
func capCueText(c Cue) Cue {
	c.Label = ClipRunes(c.Label, MaxCueLabelLen)
	c.Speaker = ClipRunes(c.Speaker, MaxCueSpeakerLen)
	c.Notes = ClipRunes(c.Notes, MaxCueNotesLen)
	return c
}

func ClipUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// MaxActionsPerShow bounds the per-show log tail (oldest rows pruned on
// insert; the dashboard only ever shows the recent past).
const MaxActionsPerShow = 200

// Action is one logged operator/automation mutation.
type Action struct {
	ID     int64  `db:"id"      json:"id"`
	ShowID int64  `db:"show_id" json:"showId,omitempty"`
	TS     int64  `db:"ts"      json:"ts"`
	Actor  string `db:"actor"   json:"actor"`
	Action string `db:"action"  json:"action"`
	Detail string `db:"detail"  json:"detail"`
}

// LogAction appends one entry (actor: "controls:<peer>" over WS, "api" over
// REST) and prunes past the per-show cap. It never returns an error:
// logging must not break the operation it records.
func (d *DB) LogAction(showID int64, actor, action, detail string) {
	detail = ClipUTF8(detail, 160)
	if _, err := d.Exec(`INSERT INTO actions (show_id, ts, actor, action, detail) VALUES (?, ?, ?, ?, ?)`,
		showID, nowMS(), actor, action, detail); err != nil {
		return
	}
	_, _ = d.Exec(`DELETE FROM actions WHERE show_id = ? AND id NOT IN (
		SELECT id FROM actions WHERE show_id = ? ORDER BY id DESC LIMIT ?)`,
		showID, showID, MaxActionsPerShow)
}

// ListActions returns the newest-first tail (limit clamped to [1, 200]).
func (d *DB) ListActions(showID int64, limit int) ([]Action, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxActionsPerShow {
		limit = MaxActionsPerShow
	}
	var out []Action
	err := d.Select(&out, `SELECT id, show_id, ts, actor, action, detail FROM actions
		WHERE show_id = ? ORDER BY id DESC LIMIT ?`, showID, limit)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list actions %d: %w", showID, err)
	}
	if out == nil {
		out = []Action{}
	}
	return out, nil
}

// LastActionID is the oob-diff cursor for frag-actions (0 when none).
// A DB error is returned, not reported as "no actions" (BUGLOG RS25).
func (d *DB) LastActionID(showID int64) (int64, error) {
	var id int64
	err := d.Get(&id, `SELECT COALESCE(MAX(id), 0) FROM actions WHERE show_id = ?`, showID)
	return id, err
}

// ---------------------------------------------------------------------------
// Client error reports (F4: window.onerror / unhandledrejection batched
// back to the server for debugging). Same capped-tail shape as the action
// log; inspection is the JSON tail below plus the journal (every report
// also logs one line — journal survives even DB trouble).
const MaxClientErrorsPerShow = 200

// ClientError is one browser-reported failure.
type ClientError struct {
	ID      int64  `db:"id"      json:"id"`
	ShowID  int64  `db:"show_id" json:"showId,omitempty"`
	TS      int64  `db:"ts"      json:"ts"`
	Kind    string `db:"kind"    json:"kind"`
	Message string `db:"message" json:"message"`
	Source  string `db:"source"  json:"source"`
}

// LogClientError appends one report (truncated) and prunes past the cap.
// Like LogAction it never fails the caller.
func (d *DB) LogClientError(showID int64, kind, message, source string) {
	d.LogClientErrors(showID, []ClientError{{Kind: kind, Message: message, Source: source}})
}

// LogClientErrors appends a batch of reports in one transaction with one
// prune (BUGLOG RW16: it used to be an insert plus a prune per entry, all
// on the connection the timers need). Never fails the caller.
func (d *DB) LogClientErrors(showID int64, entries []ClientError) {
	if len(entries) == 0 {
		return
	}
	tx, err := d.Beginx()
	if err != nil {
		return
	}
	defer tx.Rollback()
	now := nowMS()
	for _, e := range entries {
		if _, err := tx.Exec(`INSERT INTO client_errors (show_id, ts, kind, message, source) VALUES (?, ?, ?, ?, ?)`,
			showID, now, ClipUTF8(e.Kind, 40), ClipUTF8(e.Message, 500), ClipUTF8(e.Source, 160)); err != nil {
			return
		}
	}
	if _, err := tx.Exec(`DELETE FROM client_errors WHERE show_id = ? AND id NOT IN (
		SELECT id FROM client_errors WHERE show_id = ? ORDER BY id DESC LIMIT ?)`,
		showID, showID, MaxClientErrorsPerShow); err != nil {
		return
	}
	_ = tx.Commit()
}

// ListClientErrors returns the newest-first tail (limit clamped [1, 200]).
func (d *DB) ListClientErrors(showID int64, limit int) ([]ClientError, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > MaxClientErrorsPerShow {
		limit = MaxClientErrorsPerShow
	}
	var out []ClientError
	err := d.Select(&out, `SELECT id, show_id, ts, kind, message, source FROM client_errors
		WHERE show_id = ? ORDER BY id DESC LIMIT ?`, showID, limit)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list client errors %d: %w", showID, err)
	}
	if out == nil {
		out = []ClientError{}
	}
	return out, nil
}
