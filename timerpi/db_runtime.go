package timerpi

import (
	"database/sql"
	"fmt"
)

// ---------------------------------------------------------------------------
// Settings (global key/value: layout, theme, hostname…)

// GetSetting returns the value ("" when unset); missing keys are not errors.
func (d *DB) GetSetting(key string) (string, error) {
	var v string
	err := d.Get(&v, `SELECT value FROM settings WHERE key = ?`, key)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetSetting upserts a setting.
func (d *DB) SetSetting(key, value string) error {
	_, err := d.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// AllSettings returns every setting as a map.
func (d *DB) AllSettings() (map[string]string, error) {
	rows := []struct {
		Key   string `db:"key"`
		Value string `db:"value"`
	}{}
	if err := d.Select(&rows, `SELECT key, value FROM settings`); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Runtime (engine state, one row per show)

// LoadRuntime returns the stored runtime; found=false when the show has
// never run (caller gets the zero runtime with Rate 1).
func (d *DB) LoadRuntime(showID int64) (Runtime, bool, error) {
	var rt Runtime
	err := d.Get(&rt, `SELECT * FROM runtime_state WHERE show_id = ?`, showID)
	if err == sql.ErrNoRows {
		return Runtime{ShowID: showID, Rate: DefaultRate}, false, nil
	}
	if err != nil {
		return Runtime{}, false, fmt.Errorf("timerpi: load runtime %d: %w", showID, err)
	}
	return rt, true, nil
}

// SaveRuntime upserts the engine state and stamps updated_at (part of the
// snapshot's updatedAt max — every engine mutation is visible to clients).
func (d *DB) SaveRuntime(rt Runtime) error {
	_, err := d.Exec(`INSERT INTO runtime_state
		(show_id, active_pos, prev_pos, next_pos, paused, running, end_action,
		 anchor_ts, rate, paused_elapsed_ms, day_start_ts, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(show_id) DO UPDATE SET
			active_pos = excluded.active_pos, prev_pos = excluded.prev_pos,
			next_pos = excluded.next_pos, paused = excluded.paused,
			running = excluded.running, end_action = excluded.end_action,
			anchor_ts = excluded.anchor_ts, rate = excluded.rate,
			paused_elapsed_ms = excluded.paused_elapsed_ms,
			day_start_ts = excluded.day_start_ts, updated_at = excluded.updated_at`,
		rt.ShowID, rt.ActivePos, rt.PrevPos, rt.NextPos, b2i(rt.Paused), b2i(rt.Running),
		rt.EndAction, rt.AnchorTS, rt.Rate, rt.PausedElapsedMS, rt.DayStartTS, nowMS())
	return err
}

// UpdatedStamp is the max updated_at across the show's rows (shows, cues,
// messages, runtime). The snapshot's updatedAt is built from this.
func (d *DB) UpdatedStamp(showID int64) int64 {
	var max sql.NullInt64
	err := d.Get(&max, `SELECT MAX(ts) FROM (
		SELECT MAX(updated_at) AS ts FROM shows WHERE id = ?
		UNION ALL SELECT MAX(updated_at) FROM cues WHERE show_id = ?
		UNION ALL SELECT MAX(updated_at) FROM messages WHERE show_id = ?
		UNION ALL SELECT MAX(updated_at) FROM runtime_state WHERE show_id = ?)`,
		showID, showID, showID, showID)
	if err != nil || !max.Valid {
		return 0
	}
	return max.Int64
}

// b2i stores Go bools as SQLite 0/1.
func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// §11.9 full-fidelity show bundle: raw restore primitives carrying state
// the create-path discards (moderation state, authorship, vote dedupe key,
// screen registry rows) so an exported event round-trips completely.

// CreatePollRaw inserts a poll row verbatim (state/ts/author honored) —
// the export/import path only.
func (d *DB) CreatePollRaw(p Poll) (Poll, error) {
	if !pollKinds[p.Kind] && !(p.Parent > 0 && p.Kind == "submission") {
		// Older bundles stored children with their parent's kind.
		if p.Parent > 0 && pollKinds[p.Kind] {
			p.Kind = "submission"
		} else {
			return Poll{}, fmt.Errorf("timerpi: poll kind %q invalid", p.Kind)
		}
	}
	switch p.State {
	case StateHidden, StateOpen, StateResults, StateAnswered, StateDismissed:
	default:
		p.State = StateHidden
	}
	if p.Options == "" {
		p.Options = "[]"
	}
	ts := p.Ts
	if ts <= 0 {
		ts = nowMS()
	}
	p.Ts, p.Updated = ts, ts
	return d.insertPoll(p)
}

// SetPollSpotRaw restores a Q&A spotlight pointer (import path only).
func (d *DB) SetPollSpotRaw(itemID, childID int64) error {
	defer d.airDirtyPoll(itemID)
	_, err := d.Exec(`UPDATE polls SET spot = ? WHERE id = ?`, childID, itemID)
	return err
}

// airDirtyPoll marks the on-air cache of the poll's room stale.
func (d *DB) airDirtyPoll(pollID int64) {
	var showID int64
	if err := d.Get(&showID, `SELECT show_id FROM polls WHERE id = ?`, pollID); err == nil {
		d.airDirty(showID)
	}
}

// VoteRaw restores one vote row verbatim (dedupe by (poll_id,peer) — the
// UNIQUE absorbs duplicates from a double import).
func (d *DB) VoteRaw(pollID int64, peer, choice string, ts int64) error {
	defer d.airDirtyPoll(pollID)
	if peer == "" {
		return fmt.Errorf("timerpi: vote needs a device id")
	}
	_, err := d.Exec(`INSERT INTO votes (poll_id, peer, choice, ts) VALUES (?, ?, ?, ?)
		ON CONFLICT (poll_id, peer) DO UPDATE SET choice = ?, ts = ?`,
		pollID, peer, choice, ts, choice, ts)
	if err != nil {
		return fmt.Errorf("timerpi: vote raw: %w", err)
	}
	return nil
}

// ListVotes exports a poll's vote rows (peer token hash + choice + ts).
func (d *DB) ListVotes(pollID int64) ([]Vote, error) {
	var out []Vote
	err := d.Select(&out, `SELECT poll_id, peer, choice, ts FROM votes WHERE poll_id = ? ORDER BY id`, pollID)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list votes: %w", err)
	}
	return out, nil
}
