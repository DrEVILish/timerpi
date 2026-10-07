package timerpi

// venue.go — the event's life across the cloud and a venue (VENUE-CLOUD
// §3–§6): its end and release, the event mesh key, and where it lives.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ReleaseAfter is how long after an event's end its boxes and screens are
// released (owner, 2026-10-06).
const ReleaseAfter = 4 * time.Hour

// SetEventEnd sets (or clears, with 0) the event's end. Moving the end
// clears an earlier release, so an extended event keeps its screens.
func (d *DB) SetEventEnd(id, endsAt int64) error {
	if endsAt < 0 {
		return fmt.Errorf("timerpi: bad event end")
	}
	_, err := d.Exec(`UPDATE events SET ends_at = ?, released_at = 0, updated_at = ? WHERE id = ?`, endsAt, nowMS(), id)
	return err
}

// EventMeshKey returns the event's mesh key, creating it on first use: 32
// random bytes, hex. It signs box announcements and the cloud link.
func (d *DB) EventMeshKey(id int64) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	if _, err := d.Exec(`UPDATE events SET mesh_key = ? WHERE id = ? AND mesh_key = ''`, hex.EncodeToString(b[:]), id); err != nil {
		return "", err
	}
	var key string
	err := d.Get(&key, `SELECT mesh_key FROM events WHERE id = ?`, id)
	return key, err
}

// SetEventMeshKey stores a key learned elsewhere (a bundle from the cloud
// or a venue registering an event it made offline).
func (d *DB) SetEventMeshKey(id int64, key string) error {
	_, err := d.Exec(`UPDATE events SET mesh_key = ? WHERE id = ?`, key, id)
	return err
}

// SetEventHome records where the event lives ("" or "venue").
func (d *DB) SetEventHome(id int64, home string) error {
	_, err := d.Exec(`UPDATE events SET home = ? WHERE id = ?`, home, id)
	return err
}

// EventsDue lists events whose end + ReleaseAfter has passed and that
// haven't been released yet.
func (d *DB) EventsDue(now time.Time) ([]Event, error) {
	var out []Event
	err := d.Select(&out, `SELECT * FROM events WHERE ends_at > 0 AND released_at = 0 AND ends_at + ? <= ?`,
		ReleaseAfter.Milliseconds(), now.UnixMilli())
	return out, err
}

// MarkEventReleased records the release.
func (d *DB) MarkEventReleased(id int64, at time.Time) error {
	_, err := d.Exec(`UPDATE events SET released_at = ? WHERE id = ?`, at.UnixMilli(), id)
	return err
}

// ForgetEventScreens drops every screen of the event's rooms (their keys
// go with them: the screens are released) and returns room id → names.
func (d *DB) ForgetEventScreens(eventID int64) (map[int64][]string, error) {
	var rows []struct {
		ShowID int64  `db:"show_id"`
		Name   string `db:"name"`
	}
	if err := d.Select(&rows, `SELECT s.show_id, s.name FROM screens s JOIN shows sh ON sh.id = s.show_id WHERE sh.event_id = ?`, eventID); err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, r := range rows {
		out[r.ShowID] = append(out[r.ShowID], r.Name)
	}
	_, err := d.Exec(`DELETE FROM screens WHERE show_id IN (SELECT id FROM shows WHERE event_id = ?)`, eventID)
	return out, err
}

// ResolveShowInEvent resolves a room code that must belong to the event.
func (d *DB) ResolveShowInEvent(eventID int64, code string) (Show, bool) {
	id, ok := ResolveShowID(d, code)
	if !ok {
		return Show{}, false
	}
	sh, err := d.GetShow(id)
	if err != nil || sh.EventID != eventID {
		return Show{}, false
	}
	return sh, true
}

// ---------------------------------------------------------------------------
// Event copies (VENUE-CLOUD §4, §6): a box pulling its event from the cloud,
// members mirroring the primary, and the cloud keeping the venue's copy all
// keep the event's and rooms' codes (the audience QR codes must work on both
// sides) and the rooms' ids where the room already exists (live sessions are
// keyed by them).

// CreateEventWithCode makes an event under a given code (a copy).
func (d *DB) CreateEventWithCode(code, name, superHash string) (Event, error) {
	code = NormalizeCode(code)
	if !ValidCode(code) {
		return Event{}, fmt.Errorf("timerpi: bad event code")
	}
	now := nowMS()
	res, err := d.Exec(`INSERT INTO events (code, name, super_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		code, ClipUTF8(name, MaxNameLen), superHash, now, now)
	if err != nil {
		return Event{}, fmt.Errorf("timerpi: create event copy: %w", err)
	}
	id, _ := res.LastInsertId()
	return d.GetEvent(id)
}

// UpdateEventCopy sets the copied event's fields.
func (d *DB) UpdateEventCopy(id int64, name, superHash, theme string, endsAt int64) error {
	_, err := d.Exec(`UPDATE events SET name = ?, super_hash = ?, theme = ?,
		released_at = CASE WHEN ends_at = ? THEN released_at ELSE 0 END, ends_at = ?, updated_at = ? WHERE id = ?`,
		ClipUTF8(name, MaxNameLen), superHash, theme, endsAt, endsAt, nowMS(), id)
	return err
}

// CreateRoomWithCode adds a room under a given code (a copy).
func (d *DB) CreateRoomWithCode(eventID int64, code, title string) (Show, error) {
	code = NormalizeCode(code)
	if !ValidCode(code) {
		return Show{}, fmt.Errorf("timerpi: bad room code")
	}
	now := nowMS()
	res, err := d.Exec(`INSERT INTO shows (title, code, created_at, updated_at, event_id) VALUES (?, ?, ?, ?, ?)`,
		ClipUTF8(title, MaxNameLen), code, now, now, eventID)
	if err != nil {
		return Show{}, fmt.Errorf("timerpi: create room copy: %w", err)
	}
	id, _ := res.LastInsertId()
	return d.GetShow(id)
}

// UpdateRoomCopy sets a copied room's own fields (the password is the
// stored hash, copied as is).
func (d *DB) UpdateRoomCopy(id int64, title string, pos int64, roomPWHash, notes, dayStart string) error {
	_, err := d.Exec(`UPDATE shows SET title = ?, room_pos = ?, room_pw = ?, notes = ?, day_start = ?, updated_at = ? WHERE id = ?`,
		ClipUTF8(title, MaxNameLen), pos, roomPWHash, notes, dayStart, nowMS(), id)
	return err
}

// ClearRoomContent empties a room before a copy refills it: messages,
// interactions (votes go with them), screens and presets. Cues are replaced
// by ReplaceCues; boards are the event's (ClearEventBoards).
func (d *DB) ClearRoomContent(showID int64) error {
	defer d.airDirty(showID)
	for _, q := range []string{
		`DELETE FROM messages WHERE show_id = ?`,
		`DELETE FROM polls WHERE show_id = ?`,
		`DELETE FROM screens WHERE show_id = ?`,
		`DELETE FROM display_presets WHERE show_id = ?`,
	} {
		if _, err := d.Exec(q, showID); err != nil {
			return fmt.Errorf("timerpi: clear room: %w", err)
		}
	}
	return nil
}

// ClearEventBoards drops the event's layouts before a copy refills them.
func (d *DB) ClearEventBoards(eventID int64) error {
	_, err := d.Exec(`DELETE FROM display_boards WHERE show_id IN (SELECT id FROM shows WHERE event_id = ?)`, eventID)
	if err != nil && strings.Contains(err.Error(), "no such table") {
		return nil
	}
	return err
}

// SetScreenKey restores a copied screen's key (its box keeps working).
func (d *DB) SetScreenKey(showID int64, name, key string) error {
	_, err := d.Exec(`UPDATE screens SET key = ? WHERE show_id = ? AND name = ?`, key, showID, SanitizeScreenName(name))
	return err
}

// ScreenKeys returns the room's screen keys by name (event copies only;
// never part of a downloadable room file).
func (d *DB) ScreenKeys(showID int64) (map[string]string, error) {
	var rows []struct {
		Name string `db:"name"`
		Key  string `db:"key"`
	}
	if err := d.Select(&rows, `SELECT name, key FROM screens WHERE show_id = ?`, showID); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.Name] = r.Key
	}
	return out, nil
}
