package timerpi

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Shows

// CreateShow inserts a show and returns it (with ID/timestamps). The
// show's public share code (Agent L/FIX-L) is generated here with
// crypto/rand and stored in the same INSERT as the row, so no reader can
// ever see a show without a code; a UNIQUE-index collision (astronomically
// unlikely — 32^8 space, and the pre-check in NewCode above) retries the
// whole insert.
func (d *DB) CreateShow(title string) (Show, error) {
	return d.createShow(title, 0)
}

// createShow inserts a show, attached to eventID (0 = none) at the end of
// its rooms, in ONE statement: a crash can never leave a room created but
// not attached (BUGLOG RW31).
func (d *DB) createShow(title string, eventID int64) (Show, error) {
	s := Show{Title: ClipRunes(strings.TrimSpace(title), MaxNameLen)}
	if err := s.Validate(); err != nil {
		return Show{}, err
	}
	now := nowMS()
	for attempt := 0; attempt < 16; attempt++ {
		code, cerr := NewCode(d)
		if cerr != nil {
			return Show{}, cerr
		}
		res, err := d.Exec(`INSERT INTO shows (title, code, created_at, updated_at, event_id, room_pos)
			VALUES (?, ?, ?, ?, ?, CASE WHEN ? = 0 THEN 0 ELSE (SELECT COALESCE(MAX(room_pos), 0) + 1 FROM shows WHERE event_id = ?) END)`,
			s.Title, code, now, now, eventID, eventID, eventID)
		if err != nil {
			// UNIQUE (idx_shows_code) lost a race → retry with a fresh code;
			// anything else is a real failure.
			if strings.Contains(err.Error(), "UNIQUE") {
				continue
			}
			return Show{}, fmt.Errorf("timerpi: create show: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return Show{}, err
		}
		return d.GetShow(id)
	}
	return Show{}, fmt.Errorf("timerpi: create show: code collision persisted")
}

// CloneShow duplicates a show day (E1: recurring shows): fresh share code,
// same cues in run order, same notes + scheduled day start, fresh runtime.
// The per-show passphrase is deliberately NOT copied — a clone must never
// silently inherit a gate its new audience wasn't told; the operator
// re-locks explicitly. Cue rows are re-created (fresh IDs, fresh stamps).
func (d *DB) CloneShow(id int64, title string) (Show, error) {
	src, err := d.GetShow(id)
	if err != nil {
		return Show{}, err
	}
	if strings.TrimSpace(title) == "" {
		title = "Copy of " + src.Title
	}
	dst, err := d.CreateRoom(src.EventID, title)
	if err != nil {
		return Show{}, err
	}
	defer d.cuesChanged(dst.ID)
	// Any later failure removes the half-made room instead of leaving an
	// empty one in the event (BUGLOG RW31).
	ok := false
	defer func() {
		if !ok {
			_ = d.DeleteShow(dst.ID)
		}
	}()
	if src.Notes != "" {
		if err := d.SetShowNotes(dst.ID, src.Notes); err != nil {
			return Show{}, err
		}
	}
	if src.DayStart != "" {
		if err := d.SetShowDayStart(dst.ID, src.DayStart); err != nil {
			return Show{}, err
		}
	}
	cues, err := d.ListCues(id)
	if err != nil {
		return Show{}, err
	}
	// One transaction: a mid-loop failure rolls the whole cue set back
	// instead of leaving a half-cloned day.
	tx, err := d.Beginx()
	if err != nil {
		return Show{}, err
	}
	defer tx.Rollback()
	for i, c := range cues {
		c.ID, c.ShowID, c.Pos = 0, 0, 0 // fresh rows, appended in listed order
		c.Normalize()
		if err := c.Validate(); err != nil {
			return Show{}, fmt.Errorf("timerpi: clone cue %q: %w", c.Label, err)
		}
		now := nowMS()
		if _, err := insertCue(tx, dst.ID, int64(i+1), c, now); err != nil {
			return Show{}, fmt.Errorf("timerpi: clone cue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Show{}, fmt.Errorf("timerpi: clone commit: %w", err)
	}
	ok = true
	return d.GetShow(dst.ID)
}

// ListShows returns all shows, most recently touched first.
func (d *DB) ListShows() ([]Show, error) {
	var shows []Show
	err := d.Select(&shows, `SELECT * FROM shows ORDER BY updated_at DESC, id ASC`)
	return shows, err
}

// SetShowZone writes the event-grouping label (proposal #3). Same charset
// as screen names ("Hall A") — one sanitizer, one vocabulary. Empty clears.
func (d *DB) SetShowZone(showID int64, zone string) error {
	zone = SanitizeScreenName(zone)
	if _, err := d.Exec(`UPDATE shows SET zone = ?, updated_at = ? WHERE id = ?`,
		zone, nowMS(), showID); err != nil {
		return fmt.Errorf("timerpi: set show zone: %w", err)
	}
	return nil
}

// GetShow fetches one show; sql.ErrNoRows when missing (callers 404 on it).
func (d *DB) GetShow(id int64) (Show, error) {
	var s Show
	err := d.Get(&s, `SELECT * FROM shows WHERE id = ?`, id)
	if err != nil && err != sql.ErrNoRows {
		err = fmt.Errorf("timerpi: get show %d: %w", id, err)
	}
	return s, err
}

// GetShowByCode fetches one show by its normalized share code (Agent L:
// code-only public addressing; see gen.go ResolveShowID for the full rule
// set — routes go through ResolveShowID, not this raw lookup).
func (d *DB) GetShowByCode(code string) (Show, error) {
	var s Show
	err := d.Get(&s, `SELECT * FROM shows WHERE code = ?`, code)
	if err != nil && err != sql.ErrNoRows {
		err = fmt.Errorf("timerpi: get show by code %q: %w", code, err)
	}
	return s, err
}

// ShowPassphrase returns the show's per-show extra password ("" = none).
func (d *DB) ShowPassphrase(id int64) (string, error) {
	var pw string
	err := d.Get(&pw, `SELECT passphrase FROM shows WHERE id = ?`, id)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("timerpi: show passphrase %d: %w", id, err)
	}
	return pw, nil
}

// SetShowPassphrase sets (non-empty) or clears the per-show extra password,
// bumping updated_at. Plaintext like the operator password: the DB file is
// 0600 and the passphrase is needed verbatim for the unlock compare path.
func (d *DB) SetShowPassphrase(id int64, pw string) error {
	pw = strings.TrimSpace(pw)
	if _, err := d.Exec(`UPDATE shows SET passphrase = ?, updated_at = ? WHERE id = ?`,
		pw, time.Now().UnixMilli(), id); err != nil {
		return fmt.Errorf("timerpi: set show passphrase %d: %w", id, err)
	}
	return nil
}

// ShowNotes returns the operator day memo (A7, "" when none).
func (d *DB) ShowNotes(id int64) (string, error) {
	var s struct {
		Notes string `db:"notes"`
	}
	err := d.Get(&s, `SELECT notes FROM shows WHERE id = ?`, id)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("timerpi: show notes %d: %w", id, err)
	}
	return s.Notes, nil
}

// SetShowNotes stores the day memo verbatim (whitespace-preserved — notes
// are operator voice), bumping updated_at.
func (d *DB) SetShowNotes(id int64, text string) error {
	if _, err := d.Exec(`UPDATE shows SET notes = ?, updated_at = ? WHERE id = ?`,
		text, time.Now().UnixMilli(), id); err != nil {
		return fmt.Errorf("timerpi: set show notes %d: %w", id, err)
	}
	return nil
}

// ShowBlanked reports the global display blackout (E3).
func (d *DB) ShowBlanked(id int64) (bool, error) {
	var blanked bool
	err := d.Get(&blanked, `SELECT blanked FROM shows WHERE id = ?`, id)
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("timerpi: show blanked %d: %w", id, err)
	}
	return blanked, nil
}

// SetShowBlanked raises/clears the global display blackout, bumping
// updated_at so every screen flips on the next fanned snapshot.
func (d *DB) SetShowBlanked(id int64, blanked bool) error {
	v := 0
	if blanked {
		v = 1
	}
	if _, err := d.Exec(`UPDATE shows SET blanked = ?, updated_at = ? WHERE id = ?`,
		v, time.Now().UnixMilli(), id); err != nil {
		return fmt.Errorf("timerpi: set show blanked %d: %w", id, err)
	}
	return nil
}

// ShowDayStart returns the scheduled day start "HH:MM" ("" = unset).
func (d *DB) ShowDayStart(id int64) (string, error) {
	var s struct {
		DayStart string `db:"day_start"`
	}
	err := d.Get(&s, `SELECT day_start FROM shows WHERE id = ?`, id)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("timerpi: show day_start %d: %w", id, err)
	}
	return s.DayStart, nil
}

// SetShowDayStart persists "HH:MM" (empty clears), bumping updated_at.
func (d *DB) SetShowDayStart(id int64, hhmm string) error {
	if _, err := d.Exec(`UPDATE shows SET day_start = ?, updated_at = ? WHERE id = ?`,
		hhmm, time.Now().UnixMilli(), id); err != nil {
		return fmt.Errorf("timerpi: set show day_start %d: %w", id, err)
	}
	return nil
}

// RenameShow updates the title and bumps updated_at.
func (d *DB) RenameShow(id int64, title string) error {
	title = ClipRunes(strings.TrimSpace(title), MaxNameLen)
	if err := (Show{Title: title}).Validate(); err != nil {
		return err
	}
	_, err := d.Exec(`UPDATE shows SET title = ?, updated_at = ? WHERE id = ?`, title, nowMS(), id)
	return err
}

// TouchShow bumps the show's updated_at (e.g. after a cue edit that already
// bumped the cue row, so ListShows ordering follows activity).
func (d *DB) TouchShow(id int64) error {
	_, err := d.Exec(`UPDATE shows SET updated_at = ? WHERE id = ?`, nowMS(), id)
	return err
}

// DeleteShow removes a show; cues, messages and runtime go via ON DELETE
// CASCADE (foreign_keys pragma must be on — see Open).
func (d *DB) DeleteShow(id int64) error {
	defer d.cuesChanged(id)
	defer d.airDirty(id)
	_, err := d.Exec(`DELETE FROM shows WHERE id = ?`, id)
	return err
}
