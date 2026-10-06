package timerpi

// events.go — the Event layer (PRODUCT §3): an event is what a
// SuperOperator creates; it owns rooms. A room IS a show (one running
// order, its own engine, its own screens) with event_id pointing here.
//
// Passwords:
//   - events.super_hash  — the supervisor password (SuperOperator / admin).
//   - shows.room_pw      — the optional per-room moderator password.
// Both are stored as PBKDF2 hashes (HashPassword). Session cookies are
// HMACs over the code + the stored hash (routes/access.go), so changing a
// password signs every holder of the old one out.

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Event is one conference (single day in v2; Days is the day count the
// model is ready for — PRODUCT §4.7).
type Event struct {
	ID        int64  `db:"id"         json:"id"`
	Code      string `db:"code"       json:"code"`
	Name      string `db:"name"       json:"name"`
	SuperHash string `db:"super_hash" json:"-"`
	Theme     string `db:"theme"      json:"theme"`
	MapAsset  int64  `db:"map_asset"  json:"mapAsset"`
	Days      int64  `db:"days"       json:"days"`
	CreatedAt int64  `db:"created_at" json:"createdAt"`
	UpdatedAt int64  `db:"updated_at" json:"updatedAt"`
}

// HasSuperPassword reports whether the event is protected (legacy events
// migrated from pre-event shows start without one).
func (e Event) HasSuperPassword() bool { return e.SuperHash != "" }

func (d *DB) createEventsSchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			code       TEXT NOT NULL,
			name       TEXT NOT NULL,
			super_hash TEXT NOT NULL DEFAULT '',
			theme      TEXT NOT NULL DEFAULT '',
			map_asset  INTEGER NOT NULL DEFAULT 0,
			days       INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_events_code ON events (code);`,
		`CREATE INDEX IF NOT EXISTS idx_shows_event ON shows (event_id, room_pos);`,
	}
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return fmt.Errorf("timerpi: events schema: %w", err)
		}
	}
	return nil
}

// adoptOrphanShows gives every pre-event show a parent event: shows that
// share a zone label become rooms of one event named after the zone; any
// other show becomes a one-room event named after itself. Legacy plaintext
// show passphrases become hashed room passwords. Idempotent.
func (d *DB) adoptOrphanShows() error {
	var orphans []Show
	if err := d.Select(&orphans, `SELECT * FROM shows WHERE event_id = 0 ORDER BY id`); err != nil {
		return fmt.Errorf("timerpi: orphan scan: %w", err)
	}
	byZone := map[string]int64{}
	for _, sh := range orphans {
		var evID int64
		if z := strings.TrimSpace(sh.Zone); z != "" {
			if id, ok := byZone[z]; ok {
				evID = id
			}
		}
		if evID == 0 {
			name := sh.Title
			if z := strings.TrimSpace(sh.Zone); z != "" {
				name = z
			}
			ev, err := d.insertEvent(name, "")
			if err != nil {
				return err
			}
			evID = ev.ID
			if z := strings.TrimSpace(sh.Zone); z != "" {
				byZone[z] = evID
				// Zone maps become event maps.
				if v, _ := d.GetSetting("zone.map." + z); v != "" {
					if id, perr := strconv.ParseInt(v, 10, 64); perr == nil {
						_, _ = d.Exec(`UPDATE events SET map_asset = ? WHERE id = ?`, id, evID)
					}
				}
			}
		}
		var pos int64
		_ = d.Get(&pos, `SELECT COALESCE(MAX(room_pos), 0) + 1 FROM shows WHERE event_id = ?`, evID)
		roomPW := ""
		if sh.Passphrase != "" {
			roomPW = HashPassword(sh.Passphrase)
		}
		if _, err := d.Exec(`UPDATE shows SET event_id = ?, room_pos = ?, room_pw = CASE WHEN ? != '' THEN ? ELSE room_pw END, passphrase = '' WHERE id = ?`,
			evID, pos, roomPW, roomPW, sh.ID); err != nil {
			return fmt.Errorf("timerpi: adopt show %d: %w", sh.ID, err)
		}
	}
	return nil
}

func (d *DB) insertEvent(name, superHash string) (Event, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Event{}, fmt.Errorf("timerpi: event name must not be empty")
	}
	now := nowMS()
	for attempt := 0; attempt < 16; attempt++ {
		code, err := NewCode(d)
		if err != nil {
			return Event{}, err
		}
		res, err := d.Exec(`INSERT INTO events (code, name, super_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			code, name, superHash, now, now)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				continue
			}
			return Event{}, fmt.Errorf("timerpi: create event: %w", err)
		}
		id, _ := res.LastInsertId()
		return d.GetEvent(id)
	}
	return Event{}, fmt.Errorf("timerpi: create event: code collision persisted")
}

// CreateEvent makes an event with its supervisor password (required for
// new events) and its first rooms (at least one; blank names are skipped).
func (d *DB) CreateEvent(name, superPassword string, rooms []string) (Event, []Show, error) {
	if strings.TrimSpace(superPassword) == "" {
		return Event{}, nil, fmt.Errorf("timerpi: supervisor password required")
	}
	var names []string
	for _, r := range rooms {
		if r = strings.TrimSpace(r); r != "" {
			names = append(names, r)
		}
	}
	if len(names) == 0 {
		names = []string{"Main room"}
	}
	ev, err := d.insertEvent(name, HashPassword(superPassword))
	if err != nil {
		return Event{}, nil, err
	}
	out := make([]Show, 0, len(names))
	for _, n := range names {
		sh, err := d.CreateRoom(ev.ID, n)
		if err != nil {
			return Event{}, nil, err
		}
		out = append(out, sh)
	}
	return ev, out, nil
}

// CreateRoom adds a room (a show) at the end of the event's room list.
func (d *DB) CreateRoom(eventID int64, name string) (Show, error) {
	sh, err := d.CreateShow(name)
	if err != nil {
		return Show{}, err
	}
	var pos int64
	_ = d.Get(&pos, `SELECT COALESCE(MAX(room_pos), 0) + 1 FROM shows WHERE event_id = ?`, eventID)
	if _, err := d.Exec(`UPDATE shows SET event_id = ?, room_pos = ? WHERE id = ?`, eventID, pos, sh.ID); err != nil {
		return Show{}, fmt.Errorf("timerpi: attach room: %w", err)
	}
	return d.GetShow(sh.ID)
}

// GetEvent fetches one event by id.
func (d *DB) GetEvent(id int64) (Event, error) {
	var e Event
	err := d.Get(&e, `SELECT * FROM events WHERE id = ?`, id)
	if err != nil && err != sql.ErrNoRows {
		err = fmt.Errorf("timerpi: get event %d: %w", id, err)
	}
	return e, err
}

// ResolveEvent maps a typed event code (typo-tolerant, 4-4 or bare) to
// its event. found=false for any miss.
func (d *DB) ResolveEvent(ident string) (Event, bool) {
	code := NormalizeCode(strings.TrimSpace(ident))
	if !ValidCode(code) {
		return Event{}, false
	}
	var e Event
	if err := d.Get(&e, `SELECT * FROM events WHERE code = ?`, code); err != nil {
		return Event{}, false
	}
	return e, true
}

// ListEvents returns every event (appliance housekeeping only — never
// exposed publicly; codes are credentials).
func (d *DB) ListEvents() ([]Event, error) {
	var out []Event
	err := d.Select(&out, `SELECT * FROM events ORDER BY id`)
	return out, err
}

// CountProtectedEvents reports how many events carry a supervisor
// password (box settings stay open while there are none).
// A query error is returned, never read as "none" (BUGLOG RW6: that
// opened box settings to everyone whenever the database was busy).
func (d *DB) CountProtectedEvents() (int, error) {
	var n int
	err := d.Get(&n, `SELECT COUNT(*) FROM events WHERE super_hash != ''`)
	return n, err
}

// ListRooms returns the event's rooms in room order.
func (d *DB) ListRooms(eventID int64) ([]Show, error) {
	var out []Show
	err := d.Select(&out, `SELECT * FROM shows WHERE event_id = ? ORDER BY room_pos, id`, eventID)
	if out == nil {
		out = []Show{}
	}
	return out, err
}

// RenameEvent sets the event name.
func (d *DB) RenameEvent(id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("timerpi: event name must not be empty")
	}
	_, err := d.Exec(`UPDATE events SET name = ?, updated_at = ? WHERE id = ?`, name, nowMS(), id)
	return err
}

// SetEventSuperPassword replaces the supervisor password (never cleared:
// an empty password is refused).
func (d *DB) SetEventSuperPassword(id int64, pw string) error {
	if strings.TrimSpace(pw) == "" {
		return fmt.Errorf("timerpi: supervisor password must not be empty")
	}
	_, err := d.Exec(`UPDATE events SET super_hash = ?, updated_at = ? WHERE id = ?`, HashPassword(pw), nowMS(), id)
	return err
}

// SetEventTheme sets the event's default screen theme ("" = appliance default).
func (d *DB) SetEventTheme(id int64, theme string) error {
	_, err := d.Exec(`UPDATE events SET theme = ?, updated_at = ? WHERE id = ?`, theme, nowMS(), id)
	return err
}

// SetEventMap points the event at a map image asset (0 clears).
func (d *DB) SetEventMap(id, assetID int64) error {
	_, err := d.Exec(`UPDATE events SET map_asset = ?, updated_at = ? WHERE id = ?`, assetID, nowMS(), id)
	return err
}

// SetRoomPassword sets ("" clears) one room's moderator password.
func (d *DB) SetRoomPassword(showID int64, pw string) error {
	h := ""
	if strings.TrimSpace(pw) != "" {
		h = HashPassword(pw)
	}
	_, err := d.Exec(`UPDATE shows SET room_pw = ?, updated_at = ? WHERE id = ?`, h, nowMS(), showID)
	return err
}

// MoveRoom reorders a room within its event to position to (1-based).
func (d *DB) MoveRoom(eventID, showID, to int64) error {
	rooms, err := d.ListRooms(eventID)
	if err != nil {
		return err
	}
	var ids []int64
	for _, r := range rooms {
		if r.ID != showID {
			ids = append(ids, r.ID)
		}
	}
	if to < 1 {
		to = 1
	}
	if to > int64(len(ids))+1 {
		to = int64(len(ids)) + 1
	}
	ids = append(ids[:to-1], append([]int64{showID}, ids[to-1:]...)...)
	for i, id := range ids {
		if _, err := d.Exec(`UPDATE shows SET room_pos = ? WHERE id = ? AND event_id = ?`, i+1, id, eventID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteEvent removes the event and every room in it.
func (d *DB) DeleteEvent(id int64) error {
	if _, err := d.Exec(`DELETE FROM shows WHERE event_id = ?`, id); err != nil {
		return err
	}
	_, err := d.Exec(`DELETE FROM events WHERE id = ?`, id)
	return err
}

// ---------------------------------------------------------------------------
// Password hashing + session secret

const pbkdf2Iter = 60000

// HashPassword returns a salted PBKDF2-SHA256 hash string.
func HashPassword(pw string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key, _ := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, 32)
	return fmt.Sprintf("pbkdf2$%d$%s$%s", pbkdf2Iter, hex.EncodeToString(salt), hex.EncodeToString(key))
}

// CheckPassword verifies pw against a HashPassword string. An empty hash
// never matches (callers treat "no password" explicitly).
func CheckPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err1 := hex.DecodeString(parts[2])
	want, err2 := hex.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// SessionSecret returns the appliance's HMAC key for session cookies,
// creating it on first use (settings "auth.secret").
func (d *DB) SessionSecret() []byte {
	if v, _ := d.GetSetting("auth.secret"); len(v) == 64 {
		if b, err := hex.DecodeString(v); err == nil {
			return b
		}
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	_ = d.SetSetting("auth.secret", hex.EncodeToString(b))
	return b
}

// SignSession is HMAC-SHA256(secret, parts joined by "|"), hex.
func SignSession(secret []byte, parts ...string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(m.Sum(nil))
}
