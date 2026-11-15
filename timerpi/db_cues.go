package timerpi

import (
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// ---------------------------------------------------------------------------
// Cues
//
// Cues are keyed by (show_id, Pos) for engine/UI ops; ID is the stable
// identity used by reorder. Pos is kept contiguous 1..N after every mutation
// via applyOrder (two-phase renumber: negative temp values first, so the
// UNIQUE(show_id,pos) constraint can never trip on a mid-shift collision).

// ListCues returns the show's cues ordered by Pos.
func (d *DB) ListCues(showID int64) ([]Cue, error) {
	var cues []Cue
	err := d.Select(&cues, `SELECT * FROM cues WHERE show_id = ? ORDER BY pos ASC, id ASC`, showID)
	return cues, err
}

// GetCue fetches one cue by position; sql.ErrNoRows when missing.
func (d *DB) GetCue(showID, pos int64) (Cue, error) {
	var c Cue
	err := d.Get(&c, `SELECT * FROM cues WHERE show_id = ? AND pos = ?`, showID, pos)
	if err != nil && err != sql.ErrNoRows {
		err = fmt.Errorf("timerpi: get cue %d.%d: %w", showID, pos, err)
	}
	return c, err
}

// CreateCue inserts a cue. c.Pos == 0 appends at the end; c.Pos = k inserts
// at position k and shifts the rest down. Returns the stored cue.
func (d *DB) CreateCue(showID int64, c Cue) (Cue, error) {
	defer d.cuesChanged(showID)
	c.ShowID = showID
	c.Normalize()
	if err := c.Validate(); err != nil {
		return Cue{}, err
	}
	tx, err := d.Beginx()
	if err != nil {
		return Cue{}, err
	}
	defer tx.Rollback()
	now := nowMS()
	res, err := insertCue(tx, showID, 0, c, now)
	if err != nil {
		return Cue{}, fmt.Errorf("timerpi: create cue: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Cue{}, err
	}
	if _, err := tx.Exec(`UPDATE cues SET pos = -1 WHERE id = ?`, id); err != nil {
		return Cue{}, err
	}
	order, err := cueIDsOrdered(tx, showID) // includes the new row (temp pos -1)
	if err != nil {
		return Cue{}, err
	}
	order = removeID(order, id)
	if c.Pos <= 0 {
		order = append(order, id) // append at the end
	} else {
		order = insertID(order, id, int(c.Pos)) // insert at 1-based position k
	}
	if err := applyOrder(tx, showID, order); err != nil {
		return Cue{}, err
	}
	if err := tx.Commit(); err != nil {
		return Cue{}, err
	}
	return d.GetCue(showID, posOf(order, id))
}

// UpdateCue replaces an existing cue's fields, addressed by c.ID (>0) or,
// failing that, by c.Pos. Pos is not changed here (use MoveCue) unless the
// caller explicitly wants a positional update; the stored Pos wins for
// identity when both are set.
func (d *DB) UpdateCue(showID int64, c Cue) (Cue, error) {
	defer d.cuesChanged(showID)
	c.ShowID = showID
	c.Normalize()
	if err := c.Validate(); err != nil {
		return Cue{}, err
	}
	var cur Cue
	var err error
	if c.ID > 0 {
		cur, err = d.getCueByID(showID, c.ID)
	} else {
		cur, err = d.GetCue(showID, c.Pos)
	}
	if err != nil {
		return Cue{}, err
	}
	c = capCueText(c)
	_, err = d.Exec(`UPDATE cues SET
		label = ?, duration_ms = ?, kind = ?, tags = ?, speaker = ?, hold_ms = ?,
		timer_kind = ?, alert1_ms = ?, alert2_ms = ?, alert_color1 = ?, alert_color2 = ?,
		end_action = ?, autocontinue = ?, notes = ?, color = ?, start_at = ?, location = ?,
		alert_flash1 = ?, alert_flash2 = ?, updated_at = ?
		WHERE id = ?`,
		c.Label, c.DurationMS, c.Kind, c.Tags, c.Speaker, c.HoldMS,
		c.TimerKind, c.Alert1MS, c.Alert2MS, c.AlertColor1, c.AlertColor2,
		c.EndAction, b2i(c.AutoContinue), c.Notes, c.Color, c.StartAt, c.Location,
		b2i(c.AlertFlash1), b2i(c.AlertFlash2), nowMS(), cur.ID)
	if err != nil {
		return Cue{}, fmt.Errorf("timerpi: update cue %d: %w", cur.ID, err)
	}
	return d.GetCue(showID, cur.Pos)
}

// insertCue is the one cue INSERT (BUGLOG RS35: it was copy-pasted five
// times and none of the copies wrote day, so clone, duplicate and replace
// reset every session to day 1). Day < 1 is stored as 1.
func insertCue(tx *sqlx.Tx, showID, pos int64, c Cue, stamp int64) (sql.Result, error) {
	day := c.Day
	if day < 1 {
		day = 1
	}
	c = capCueText(c)
	return tx.Exec(`INSERT INTO cues
		(show_id, pos, label, duration_ms, kind, tags, speaker, hold_ms,
		 timer_kind, alert1_ms, alert2_ms, alert_color1, alert_color2,
		 end_action, autocontinue, notes, color, start_at, day, location, alert_flash1, alert_flash2, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		showID, pos, c.Label, c.DurationMS, c.Kind, c.Tags, c.Speaker, c.HoldMS,
		c.TimerKind, c.Alert1MS, c.Alert2MS, c.AlertColor1, c.AlertColor2,
		c.EndAction, b2i(c.AutoContinue), c.Notes, c.Color, c.StartAt, day, c.Location,
		b2i(c.AlertFlash1), b2i(c.AlertFlash2), stamp)
}

// DeleteCue removes the cue at pos and renumbers the rest 1..N.
func (d *DB) DeleteCue(showID, pos int64) error {
	defer d.cuesChanged(showID)
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM cues WHERE show_id = ? AND pos = ?`, showID, pos); err != nil {
		return err
	}
	order, err := cueIDsOrdered(tx, showID)
	if err != nil {
		return err
	}
	if err := applyOrder(tx, showID, order); err != nil {
		return err
	}
	if err := bumpStamp(tx, showID); err != nil {
		return err
	}
	return tx.Commit()
}

// MoveCue moves the cue at position from to position to (1-based), shifting
// the others; 1..N stays contiguous.
func (d *DB) MoveCue(showID, from, to int64) error {
	defer d.cuesChanged(showID)
	if from == to {
		return nil
	}
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	order, err := cueIDsOrdered(tx, showID)
	if err != nil {
		return err
	}
	i := int(from) - 1
	j := int(to) - 1
	if i < 0 || i >= len(order) {
		return fmt.Errorf("timerpi: move cue %d: no cue at position %d", showID, from)
	}
	if j < 0 {
		j = 0
	}
	if j >= len(order) {
		j = len(order) - 1
	}
	id := order[i]
	order = append(order[:i], order[i+1:]...)
	order = append(order[:j], append([]int64{id}, order[j:]...)...)
	if err := applyOrder(tx, showID, order); err != nil {
		return err
	}
	if err := bumpStamp(tx, showID); err != nil {
		return err
	}
	return tx.Commit()
}

// DuplicateCue copies the cue at pos, inserting the copy directly after it.
func (d *DB) DuplicateCue(showID, pos int64) (Cue, error) {
	defer d.cuesChanged(showID)
	src, err := d.GetCue(showID, pos)
	if err != nil {
		return Cue{}, err
	}
	src.ID = 0 // fresh row
	tx, err := d.Beginx()
	if err != nil {
		return Cue{}, err
	}
	defer tx.Rollback()
	now := nowMS()
	res, err := insertCue(tx, showID, 0, src, now)
	if err != nil {
		return Cue{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Cue{}, err
	}
	if _, err := tx.Exec(`UPDATE cues SET pos = -1 WHERE id = ?`, id); err != nil {
		return Cue{}, err
	}
	order, err := cueIDsOrdered(tx, showID) // includes the new row (temp pos -1)
	if err != nil {
		return Cue{}, err
	}
	order = removeID(order, id)
	order = insertID(order, id, int(pos+1)) // right after the source
	if err := applyOrder(tx, showID, order); err != nil {
		return Cue{}, err
	}
	if err := tx.Commit(); err != nil {
		return Cue{}, err
	}
	return d.GetCue(showID, pos+1)
}

// ReorderCues applies a new run order given as cue IDs (stable identity, so
// a UI drag-and-drop can send the full order safely). IDs not in the list
// keep their relative order at the end; unknown IDs are ignored.
func (d *DB) ReorderCues(showID int64, cueIDs []int64) error {
	defer d.cuesChanged(showID)
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	order, err := cueIDsOrdered(tx, showID)
	if err != nil {
		return err
	}
	want := make(map[int64]bool, len(cueIDs))
	known := make(map[int64]bool, len(order))
	for _, id := range order {
		known[id] = true
	}
	// Unknown IDs match no row: drop them BEFORE numbering, or they would
	// consume positions in applyOrder and leave holes in the dense 1..N
	// order (the documented "unknown IDs are ignored" contract).
	ids := make([]int64, 0, len(cueIDs))
	for _, id := range cueIDs {
		if known[id] && !want[id] {
			want[id] = true
			ids = append(ids, id)
		}
	}
	rest := make([]int64, 0, len(order))
	for _, id := range order {
		if !want[id] {
			rest = append(rest, id)
		}
	}
	if err := applyOrder(tx, showID, append(append([]int64{}, ids...), rest...)); err != nil {
		return err
	}
	if err := bumpStamp(tx, showID); err != nil {
		return err
	}
	return tx.Commit()
}

// AppendCues adds cues after the last one, in order, in ONE transaction:
// all or none, no per-row renumbering (BUGLOG RS30: append import ran one
// CreateCue per row, each renumbering the whole show — ~250k statements for
// 500 rows — and a bad row left the import half-applied).
func (d *DB) AppendCues(showID int64, cues []Cue) error {
	defer d.cuesChanged(showID)
	for i := range cues {
		cues[i].ShowID = showID
		cues[i].Normalize()
		if err := cues[i].Validate(); err != nil {
			return fmt.Errorf("timerpi: append cue %d: %w", i+1, err)
		}
	}
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var last int64
	if err := tx.Get(&last, `SELECT COALESCE(MAX(pos), 0) FROM cues WHERE show_id = ?`, showID); err != nil {
		return err
	}
	now := nowMS()
	for i, c := range cues {
		if _, err := insertCue(tx, showID, last+int64(i)+1, c, now); err != nil {
			return fmt.Errorf("timerpi: append cue %d: %w", i+1, err)
		}
	}
	return tx.Commit()
}

// ReplaceCues swaps the whole cue list (import / PUT /api/shows/:id/cues).
// Cues keep their given order; Pos is renumbered 1..N. Runs in one tx.
func (d *DB) ReplaceCues(showID int64, cues []Cue) error {
	defer d.cuesChanged(showID)
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM cues WHERE show_id = ?`, showID); err != nil {
		return err
	}
	now := nowMS()
	ids := make([]int64, 0, len(cues))
	for i, c := range cues {
		c.ShowID = showID
		c.Normalize()
		if err := c.Validate(); err != nil {
			return err
		}
		// Distinct temp positions (negative, below nothing else) so the
		// UNIQUE (show_id,pos) constraint holds while rows land unsorted.
		res, err := insertCue(tx, showID, int64(-(i + 1)), c, now)
		if err != nil {
			return fmt.Errorf("timerpi: replace cues: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	// applyOrder's temp targets sit below -(N), so they can't collide with
	// the -(i+1) temps above; the final pass writes 1..N in list order.
	if err := applyOrder(tx, showID, ids); err != nil {
		return err
	}
	if err := bumpStamp(tx, showID); err != nil {
		return err
	}
	return tx.Commit()
}

// cueIDsOrdered lists cue ids in current Pos order (must run in a tx).
func cueIDsOrdered(q sqlx.Queryer, showID int64) ([]int64, error) {
	var ids []int64
	err := sqlx.Select(q, &ids, `SELECT id FROM cues WHERE show_id = ? ORDER BY pos ASC, id ASC`, showID)
	return ids, err
}

// bumpStamp moves the show's updated_at strictly past every stamp the show
// has (UpdatedStamp), for changes that leave no row of their own to stamp:
// a delete or a renumber. Without it a stale mesh push carrying the old
// stamp passes the sync check and restores deleted or reordered sessions
// (BUGLOG RW27).
func bumpStamp(x sqlx.Execer, showID int64) error {
	_, err := x.Exec(`UPDATE shows SET updated_at = MAX(?, 1 + (SELECT COALESCE(MAX(ts), 0) FROM (
		SELECT updated_at AS ts FROM shows WHERE id = ?
		UNION ALL SELECT MAX(updated_at) FROM cues WHERE show_id = ?
		UNION ALL SELECT MAX(updated_at) FROM messages WHERE show_id = ?
		UNION ALL SELECT MAX(updated_at) FROM runtime_state WHERE show_id = ?))) WHERE id = ?`,
		nowMS(), showID, showID, showID, showID, showID)
	return err
}

// applyOrder renumbers the given id order into contiguous Pos 1..N. Phase 1
// writes distinct negative temp positions BELOW any transient value (a
// mid-insert row briefly holds pos -1), phase 2 the final ones — with UNIQUE
// (show_id,pos) neither phase can ever collide mid-update (SQLite checks the
// constraint row by row, so an in-place +1 shift would).
func applyOrder(tx *sqlx.Tx, showID int64, order []int64) error {
	n := len(order)
	for i, id := range order {
		if _, err := tx.Exec(`UPDATE cues SET pos = ? WHERE id = ? AND show_id = ?`, -(i + 1 + n), id, showID); err != nil {
			return err
		}
	}
	for i, id := range order {
		if _, err := tx.Exec(`UPDATE cues SET pos = ? WHERE id = ? AND show_id = ?`, i+1, id, showID); err != nil {
			return err
		}
	}
	return nil
}

// insertID inserts id at 1-based position k (clamped to [1, len+1]).
func insertID(order []int64, id int64, k int) []int64 {
	if k < 1 {
		k = 1
	}
	if k > len(order)+1 {
		k = len(order) + 1
	}
	out := make([]int64, 0, len(order)+1)
	out = append(out, order[:k-1]...)
	out = append(out, id)
	return append(out, order[k-1:]...)
}

// removeID drops id from the order list (no-op when absent).
func removeID(order []int64, id int64) []int64 {
	out := make([]int64, 0, len(order))
	for _, v := range order {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

// posOf finds id's 1-based position in an order list (0 when absent).
func posOf(order []int64, id int64) int64 {
	for i, v := range order {
		if v == id {
			return int64(i + 1)
		}
	}
	return 0
}

// getCueByID fetches a cue by stable row id.
func (d *DB) getCueByID(showID, id int64) (Cue, error) {
	var c Cue
	err := d.Get(&c, `SELECT * FROM cues WHERE show_id = ? AND id = ?`, showID, id)
	if err != nil && err != sql.ErrNoRows {
		err = fmt.Errorf("timerpi: get cue by id %d: %w", id, err)
	}
	return c, err
}
