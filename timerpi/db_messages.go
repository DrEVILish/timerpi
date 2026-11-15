package timerpi

import (
	"fmt"
)

// ---------------------------------------------------------------------------
// Messages (stage overlay)

// ListMessages returns all of the show's messages (hidden included), oldest
// first. Snapshot building filters ShownAt > 0.
func (d *DB) ListMessages(showID int64) ([]Message, error) {
	var msgs []Message
	err := d.Select(&msgs, `SELECT * FROM messages WHERE show_id = ? ORDER BY id ASC`, showID)
	return msgs, err
}

// CreateMessage adds a hidden overlay line (ShownAt = 0).
func (d *DB) CreateMessage(showID int64, text, color string) (Message, error) {
	m := Message{ShowID: showID, Text: ClipRunes(text, MaxMessageLen), Color: color}
	m.Normalize()
	if err := m.Validate(); err != nil {
		return Message{}, err
	}
	res, err := d.Exec(`INSERT INTO messages (show_id, text, color, shown_at, updated_at)
		VALUES (?, ?, ?, 0, ?)`, showID, m.Text, m.Color, nowMS())
	if err != nil {
		return Message{}, fmt.Errorf("timerpi: create message: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Message{}, err
	}
	m.ID = id
	return m, nil
}

// ShowMessage puts a message on stage; shownAt <= 0 means "now". One
// message is on stage at a time: showing one takes down any other, so the
// operator's list matches the one line the screens show (UX review I9).
func (d *DB) ShowMessage(showID, id, shownAt int64) error {
	if shownAt <= 0 {
		shownAt = nowMS()
	}
	_, err := d.Exec(`UPDATE messages SET shown_at = CASE WHEN id = ? THEN ? ELSE 0 END, updated_at = ?
		WHERE show_id = ? AND (id = ? OR shown_at > 0)
		AND EXISTS (SELECT 1 FROM messages WHERE show_id = ? AND id = ?)`,
		id, shownAt, nowMS(), showID, id, showID, id)
	return err
}

// ReplaceMessages swaps the show's whole message list in one transaction
// (a mesh sync push), keeping each message's shownAt. Invalid messages are
// skipped (BUGLOG RW32: this was delete-all then create-each, unwrapped,
// errors ignored).
func (d *DB) ReplaceMessages(showID int64, msgs []Message) error {
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM messages WHERE show_id = ?`, showID); err != nil {
		return err
	}
	now := nowMS()
	for _, m := range msgs {
		m.Normalize()
		if m.Validate() != nil {
			continue
		}
		shown := m.ShownAt
		if shown < 0 {
			shown = 0
		}
		if _, err := tx.Exec(`INSERT INTO messages (show_id, text, color, shown_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			showID, ClipRunes(m.Text, MaxMessageLen), m.Color, shown, now); err != nil {
			return err
		}
	}
	if err := bumpStamp(tx, showID); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearMessage hides a message (ShownAt = 0); it stays in the list.
func (d *DB) ClearMessage(showID, id int64) error {
	_, err := d.Exec(`UPDATE messages SET shown_at = 0, updated_at = ? WHERE show_id = ? AND id = ?`,
		nowMS(), showID, id)
	return err
}

// DeleteMessage removes a message entirely.
func (d *DB) DeleteMessage(showID, id int64) error {
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM messages WHERE show_id = ? AND id = ?`, showID, id); err != nil {
		return err
	}
	if err := bumpStamp(tx, showID); err != nil {
		return err
	}
	return tx.Commit()
}
