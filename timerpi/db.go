package timerpi

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3" // sqlite3 driver (CGO)
)

// DB wraps sqlx.DB with the TimerPi schema. SQLite is the single source of
// truth (PLAN §1): shows, cues, messages, settings kv and per-show runtime.
type DB struct {
	*sqlx.DB
	air airCache // on-air interaction per room (polls.go OnAirNow)
}

type airCache struct {
	mu  sync.Mutex
	m   map[int64]*airEntry
	gen map[int64]uint64 // bumped by every poll write
}

type airEntry struct {
	v     OnAir
	valid bool // false after a write: rebuild on next read
	have  bool // v is a real (possibly stale) value
}

// dbPragmas are driver-level DSN pragmas, set per-connection (CuTePi style).
// SQLite does not enforce FKs unless turned on per-connection; WAL lets
// readers proceed during writes and busy_timeout makes writers wait instead
// of failing with SQLITE_BUSY when e.g. the sqlite3 CLI inspects the file.
const dbPragmas = "_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000"

// Open opens (creating if missing) the SQLite database at path
// (<data dir>/timerpi.db), ensures the schema and migrations exist.
// :memory: is accepted (tests).
func Open(path string) (*DB, error) {
	dsn := path
	if strings.Contains(dsn, "?") {
		dsn += "&" + dbPragmas
	} else {
		dsn += "?" + dbPragmas
	}
	raw, err := sqlx.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("timerpi: opening db at %q: %w", path, err)
	}
	// The app serialises on one pooled connection: SQLite only benefits from
	// extra write connections and they just raise busy contention.
	raw.SetMaxOpenConns(1)
	d := &DB{DB: raw}
	if err := d.migrate(); err != nil {
		raw.Close()
		return nil, err
	}
	return d, nil
}

// nowMS is the DB layer's clock (epoch ms). Kept here so save-stamps work
// even when the DB is used without an engine.
func nowMS() int64 { return time.Now().UnixMilli() }

// migrate creates the schema and applies additive migrations (new columns on
// existing tables), mirroring CuTePi's db.go approach: CREATE IF NOT EXISTS
// + an ALTER ADD COLUMN pass that tolerates "duplicate column name".
func (d *DB) migrate() error {
	schema := []string{
		`CREATE TABLE IF NOT EXISTS shows (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			title      TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0,
			code       TEXT, -- public share code (Agent L/FIX-L, gen.go rules)
			passphrase TEXT NOT NULL DEFAULT '', -- per-show extra password (empty = none)
			notes      TEXT NOT NULL DEFAULT '', -- operator day notes (A7; shown on dashboard + daysheet)
			day_start  TEXT NOT NULL DEFAULT '', -- scheduled day start "HH:MM" (B4; empty = operator anchors manually)
			blanked    INTEGER NOT NULL DEFAULT 0 -- global display blackout (E3; 1 = all displays dark)
		);`,
		`CREATE TABLE IF NOT EXISTS cues (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			show_id       INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
			pos           INTEGER NOT NULL,
			label         TEXT NOT NULL DEFAULT '',
			duration_ms   INTEGER NOT NULL DEFAULT 0,
			kind          TEXT NOT NULL DEFAULT 'session',
			tags          TEXT NOT NULL DEFAULT '',
			speaker       TEXT NOT NULL DEFAULT '',
			hold_ms       INTEGER NOT NULL DEFAULT 0,
			timer_kind    TEXT NOT NULL DEFAULT 'COUNTDOWN',
			alert1_ms     INTEGER NOT NULL DEFAULT 0,
			alert2_ms     INTEGER NOT NULL DEFAULT 0,
			alert_color1  TEXT NOT NULL DEFAULT '',
			alert_color2  TEXT NOT NULL DEFAULT '',
			end_action    TEXT NOT NULL DEFAULT 'HOLD',
			autocontinue  INTEGER NOT NULL DEFAULT 0,
			notes         TEXT NOT NULL DEFAULT '',
			color         TEXT NOT NULL DEFAULT '',
			start_at      TEXT NOT NULL DEFAULT '', -- wall-clock auto-start "HH:MM" (E5; empty = off)
			updated_at    INTEGER NOT NULL DEFAULT 0,
			UNIQUE (show_id, pos)
		);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			show_id    INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
			text       TEXT NOT NULL DEFAULT '',
			color      TEXT NOT NULL DEFAULT '',
			shown_at   INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key   TEXT PRIMARY KEY NOT NULL,
			value TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS runtime_state (
			show_id          INTEGER PRIMARY KEY REFERENCES shows(id) ON DELETE CASCADE,
			active_pos       INTEGER NOT NULL DEFAULT 0,
			prev_pos         INTEGER NOT NULL DEFAULT 0,
			next_pos         INTEGER NOT NULL DEFAULT 0,
			paused           INTEGER NOT NULL DEFAULT 0,
			running          INTEGER NOT NULL DEFAULT 0,
			end_action       TEXT NOT NULL DEFAULT 'HOLD',
			anchor_ts        INTEGER NOT NULL DEFAULT 0,
			rate             REAL NOT NULL DEFAULT 1.0,
			paused_elapsed_ms INTEGER NOT NULL DEFAULT 0,
			day_start_ts     INTEGER NOT NULL DEFAULT 0,
			updated_at       INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_show ON messages (show_id, id);`,
		`CREATE TABLE IF NOT EXISTS actions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			show_id    INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
			ts         INTEGER NOT NULL DEFAULT 0,
			actor      TEXT NOT NULL DEFAULT '',
			action     TEXT NOT NULL DEFAULT '',
			detail     TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_actions_show ON actions (show_id, id);`,
		`CREATE TABLE IF NOT EXISTS waiting_screens (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL,
			host        TEXT NOT NULL DEFAULT '',
			last_seen   INTEGER NOT NULL DEFAULT 0,
			assigned    TEXT NOT NULL DEFAULT '',
			screen      TEXT NOT NULL DEFAULT '',
			UNIQUE (name, host)
		);`,
		`CREATE TABLE IF NOT EXISTS client_errors (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			show_id    INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
			ts         INTEGER NOT NULL DEFAULT 0,
			kind       TEXT NOT NULL DEFAULT '',
			message    TEXT NOT NULL DEFAULT '',
			source     TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_client_errors_show ON client_errors (show_id, id);`,
		`CREATE TABLE IF NOT EXISTS screens (
			show_id    INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
			name       TEXT NOT NULL,
			theme      TEXT NOT NULL DEFAULT '',
			board_id   INTEGER NOT NULL DEFAULT 0,
			room       TEXT NOT NULL DEFAULT '',
			last_seen  INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (show_id, name)
		);`,
		`CREATE TABLE IF NOT EXISTS polls (
		id       INTEGER PRIMARY KEY AUTOINCREMENT,
		show_id  INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
		kind     TEXT NOT NULL DEFAULT 'poll',
		question TEXT NOT NULL DEFAULT '',
		options  TEXT NOT NULL DEFAULT '[]',
		correct  INTEGER NOT NULL DEFAULT -1,
		state    TEXT NOT NULL DEFAULT 'hidden',
		parent   INTEGER NOT NULL DEFAULT 0,
		author   TEXT NOT NULL DEFAULT '',
		ts       INTEGER NOT NULL DEFAULT 0,
		updated  INTEGER NOT NULL DEFAULT 0
	);`,
		`CREATE INDEX IF NOT EXISTS idx_polls_show ON polls (show_id, updated);`,
		`CREATE TABLE IF NOT EXISTS votes (
		id      INTEGER PRIMARY KEY AUTOINCREMENT,
		poll_id INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
		peer    TEXT NOT NULL,
		choice  TEXT NOT NULL DEFAULT '',
		ts      INTEGER NOT NULL DEFAULT 0,
		UNIQUE (poll_id, peer)
	);`,
		`CREATE TABLE IF NOT EXISTS assets (
		id    INTEGER PRIMARY KEY AUTOINCREMENT,
		name  TEXT NOT NULL DEFAULT '',
		mime  TEXT NOT NULL DEFAULT 'application/octet-stream',
		bytes BLOB NOT NULL,
		ts    INTEGER NOT NULL DEFAULT 0
	);`,
		`CREATE TABLE IF NOT EXISTS display_presets (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			show_id    INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
			name       TEXT NOT NULL,
			data       TEXT NOT NULL DEFAULT '{}',
			updated_at INTEGER NOT NULL DEFAULT 0,
			UNIQUE (show_id, name)
		);`,
	}
	for _, stmt := range schema {
		if _, err := d.Exec(stmt); err != nil {
			return fmt.Errorf("timerpi: schema: %w", err)
		}
	}
	// Future-proofing: additive column migrations (no-ops on fresh DBs).
	// The registry is table-keyed (each entry alters a different table, so
	// the pass order does not matter); shows.code is declared LAST in the
	// chain (Agent L/FIX-L) and its backfill runs after ALL of the ALTERs
	// — see migrateShowCodes for the chain-position/UNIQUE story.
	extra := map[string][]struct{ name, ddl string }{
		"cues": {
			{"hold_ms", "INTEGER NOT NULL DEFAULT 0"},
			{"timer_kind", "TEXT NOT NULL DEFAULT 'COUNTDOWN'"},
			{"start_at", "TEXT NOT NULL DEFAULT ''"},
			{"day", "INTEGER NOT NULL DEFAULT 1"},
			{"location", "TEXT NOT NULL DEFAULT ''"},
		},
		"messages": {
			{"updated_at", "INTEGER NOT NULL DEFAULT 0"},
		},
		// Chain position: LAST. `TEXT` (no UNIQUE clause — SQLite cannot
		// ADD COLUMN with a constraint; the UNIQUE guarantee is enforced
		// by idx_shows_code, created AFTER the backfill in migrateShowCodes
		// so pre-existing rows are never in violation).
		"shows": {
			{"code", "TEXT"},
			{"passphrase", "TEXT NOT NULL DEFAULT ''"},
			{"notes", "TEXT NOT NULL DEFAULT ''"},
			{"day_start", "TEXT NOT NULL DEFAULT ''"},
			{"blanked", "INTEGER NOT NULL DEFAULT 0"},
			{"zone", "TEXT NOT NULL DEFAULT ''"},
			{"event_id", "INTEGER NOT NULL DEFAULT 0"},
			{"room_pos", "INTEGER NOT NULL DEFAULT 0"},
			{"room_pw", "TEXT NOT NULL DEFAULT ''"},
		},
		// PLAN §11.2 capture modal: per-screen Room/Location, and the
		// screen name a captured waiting display adopts on its hop.
		"screens": {
			{"room", "TEXT NOT NULL DEFAULT ''"},
			{"kind", "TEXT NOT NULL DEFAULT ''"},
			{"rotation", "INTEGER NOT NULL DEFAULT 0"},
			{"key", "TEXT NOT NULL DEFAULT ''"},
			{"template", "TEXT NOT NULL DEFAULT ''"},
		},
		"waiting_screens": {
			{"screen", "TEXT NOT NULL DEFAULT ''"},
			{"token", "TEXT NOT NULL DEFAULT ''"},
		},
		"assets": {
			{"event_id", "INTEGER NOT NULL DEFAULT 0"},
		},
	}
	for table, cols := range extra {
		for _, nc := range cols {
			_, err := d.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s;`, table, nc.name, nc.ddl))
			if err != nil && !strings.Contains(err.Error(), "duplicate column name") {
				return fmt.Errorf("timerpi: adding %s.%s: %w", table, nc.name, err)
			}
		}
	}
	if err := d.createEventsSchema(); err != nil {
		return err
	}
	if err := d.migratePolls(); err != nil {
		return err
	}
	if err := d.migrateShowCodes(); err != nil {
		return err
	}
	if err := d.adoptOrphanShows(); err != nil {
		return err
	}
	return d.migrateAssetOwners()
}

// migrateShowCodes backfills the END-of-chain shows.code migration: every
// show that predates the code column (or arrived with an empty code) gets
// a crypto/rand share code, collision-checked against the DB and written
// one row at a time. THIS migration call runs on every Open, so after it
// there are no shows without a code — and CreateShow's UNIQUE-index
// insert below keeps it that way going forward.
func (d *DB) migrateShowCodes() error {
	var ids []int64
	if err := d.Select(&ids, `SELECT id FROM shows WHERE code IS NULL OR code = '' ORDER BY id ASC`); err != nil {
		return fmt.Errorf("timerpi: code backfill scan: %w", err)
	}
	for _, id := range ids {
		code, err := NewCode(d)
		if err != nil {
			return fmt.Errorf("timerpi: code backfill show %d: %w", id, err)
		}
		if _, err := d.Exec(`UPDATE shows SET code = ? WHERE id = ?`, code, id); err != nil {
			return fmt.Errorf("timerpi: code backfill write %d: %w", id, err)
		}
	}
	// UNIQUE-safe by construction: the backfill above gave every row a
	// distinct code (NewCode checks the DB), so creating the index here
	// cannot fail on a duplicate. On every subsequent Open all rows
	// already satisfy it — and CreateShow's retry keeps new rows clean.
	if _, err := d.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_shows_code ON shows (code);`); err != nil {
		return fmt.Errorf("timerpi: shows code unique index: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shows

// CreateShow inserts a show and returns it (with ID/timestamps). The
// show's public share code (Agent L/FIX-L) is generated here with
// crypto/rand and stored in the same INSERT as the row, so no reader can
// ever see a show without a code; a UNIQUE-index collision (astronomically
// unlikely — 32^8 space, and the pre-check in NewCode above) retries the
// whole insert.
func (d *DB) CreateShow(title string) (Show, error) {
	s := Show{Title: strings.TrimSpace(title)}
	if err := s.Validate(); err != nil {
		return Show{}, err
	}
	now := nowMS()
	for attempt := 0; attempt < 16; attempt++ {
		code, cerr := NewCode(d)
		if cerr != nil {
			return Show{}, cerr
		}
		res, err := d.Exec(`INSERT INTO shows (title, code, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			s.Title, code, now, now)
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
	if src.Zone != "" {
		if err := d.SetShowZone(dst.ID, src.Zone); err != nil {
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
	for _, c := range cues {
		c.ID, c.ShowID, c.Pos = 0, 0, 0 // fresh rows, appended in listed order
		c.Normalize()
		if err := c.Validate(); err != nil {
			return Show{}, fmt.Errorf("timerpi: clone cue %q: %w", c.Label, err)
		}
		now := nowMS()
		res, err := insertCue(tx, dst.ID, 0, c, now)
		if err != nil {
			return Show{}, fmt.Errorf("timerpi: clone cue: %w", err)
		}
		newID, _ := res.LastInsertId()
		// Position = inner insert order (1..N) — the clone preserves the
		// source's run order without the append dance CreateCue does.
		if _, err := tx.Exec(`UPDATE cues SET pos = ? WHERE id = ?`, newID, newID); err != nil {
			return Show{}, fmt.Errorf("timerpi: clone pos: %w", err)
		}
	}
	if _, err := tx.Exec(`UPDATE cues SET pos = (SELECT COUNT(*) FROM cues sub WHERE sub.show_id = cues.show_id AND sub.id <= cues.id) WHERE show_id = ?`, dst.ID); err != nil {
		return Show{}, fmt.Errorf("timerpi: clone order: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Show{}, fmt.Errorf("timerpi: clone commit: %w", err)
	}
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
	title = strings.TrimSpace(title)
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
	defer d.airDirty(id)
	_, err := d.Exec(`DELETE FROM shows WHERE id = ?`, id)
	return err
}

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
	_, err = d.Exec(`UPDATE cues SET
		label = ?, duration_ms = ?, kind = ?, tags = ?, speaker = ?, hold_ms = ?,
		timer_kind = ?, alert1_ms = ?, alert2_ms = ?, alert_color1 = ?, alert_color2 = ?,
		end_action = ?, autocontinue = ?, notes = ?, color = ?, start_at = ?, location = ?, updated_at = ?
		WHERE id = ?`,
		c.Label, c.DurationMS, c.Kind, c.Tags, c.Speaker, c.HoldMS,
		c.TimerKind, c.Alert1MS, c.Alert2MS, c.AlertColor1, c.AlertColor2,
		c.EndAction, b2i(c.AutoContinue), c.Notes, c.Color, c.StartAt, c.Location, nowMS(), cur.ID)
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
	return tx.Exec(`INSERT INTO cues
		(show_id, pos, label, duration_ms, kind, tags, speaker, hold_ms,
		 timer_kind, alert1_ms, alert2_ms, alert_color1, alert_color2,
		 end_action, autocontinue, notes, color, start_at, day, location, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		showID, pos, c.Label, c.DurationMS, c.Kind, c.Tags, c.Speaker, c.HoldMS,
		c.TimerKind, c.Alert1MS, c.Alert2MS, c.AlertColor1, c.AlertColor2,
		c.EndAction, b2i(c.AutoContinue), c.Notes, c.Color, c.StartAt, day, c.Location, stamp)
}

// DeleteCue removes the cue at pos and renumbers the rest 1..N.
func (d *DB) DeleteCue(showID, pos int64) error {
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
	return tx.Commit()
}

// MoveCue moves the cue at position from to position to (1-based), shifting
// the others; 1..N stays contiguous.
func (d *DB) MoveCue(showID, from, to int64) error {
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
	return tx.Commit()
}

// DuplicateCue copies the cue at pos, inserting the copy directly after it.
func (d *DB) DuplicateCue(showID, pos int64) (Cue, error) {
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
		if known[id] {
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
	return tx.Commit()
}

// ReplaceCues swaps the whole cue list (import / PUT /api/shows/:id/cues).
// Cues keep their given order; Pos is renumbered 1..N. Runs in one tx.
func (d *DB) ReplaceCues(showID int64, cues []Cue) error {
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
	return tx.Commit()
}

// cueIDsOrdered lists cue ids in current Pos order (must run in a tx).
func cueIDsOrdered(q sqlx.Queryer, showID int64) ([]int64, error) {
	var ids []int64
	err := sqlx.Select(q, &ids, `SELECT id FROM cues WHERE show_id = ? ORDER BY pos ASC, id ASC`, showID)
	return ids, err
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
	m := Message{ShowID: showID, Text: text, Color: color}
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

// ShowMessage marks a message shown; shownAt <= 0 means "now".
func (d *DB) ShowMessage(showID, id, shownAt int64) error {
	if shownAt <= 0 {
		shownAt = nowMS()
	}
	_, err := d.Exec(`UPDATE messages SET shown_at = ?, updated_at = ? WHERE show_id = ? AND id = ?`,
		shownAt, nowMS(), showID, id)
	return err
}

// ClearMessage hides a message (ShownAt = 0); it stays in the list.
func (d *DB) ClearMessage(showID, id int64) error {
	_, err := d.Exec(`UPDATE messages SET shown_at = 0, updated_at = ? WHERE show_id = ? AND id = ?`,
		nowMS(), showID, id)
	return err
}

// DeleteMessage removes a message entirely.
func (d *DB) DeleteMessage(showID, id int64) error {
	_, err := d.Exec(`DELETE FROM messages WHERE show_id = ? AND id = ?`, showID, id)
	return err
}

// ---------------------------------------------------------------------------
// Operator action log (E4: who did what — multi-operator accountability +
// post-show review). Append-only, capped per show; the dashboard renders
// the tail live via the frag-actions oob.

// ClipUTF8 truncates s to at most n BYTES without splitting a multi-byte
// rune (byte-slicing stored text produced invalid UTF-8 / U+FFFD tails).
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
func (d *DB) LastActionID(showID int64) int64 {
	var id int64
	_ = d.Get(&id, `SELECT COALESCE(MAX(id), 0) FROM actions WHERE show_id = ?`, showID)
	return id
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

// ---------------------------------------------------------------------------
// Named display screens (F1: stable per-screen identity for the operator's
// screens panel — "Stage Left" keeps its theme/board across reconnects).
// Screens register on WS join (?screen= name, persisted to the browser's
// localStorage); unnamed displays stay anonymous (live peers only).
const MaxScreenNameLen = 40

// Screen is one named display + its operator-assigned overrides (empty =
// follow the show defaults).
type Screen struct {
	ShowID   int64  `db:"show_id"   json:"-"`
	Name     string `db:"name"      json:"name"`
	Theme    string `db:"theme"     json:"theme"`
	BoardID  int64  `db:"board_id"  json:"boardId"`
	Room     string `db:"room"      json:"room"`
	LastSeen int64  `db:"last_seen" json:"lastSeen"`
	// Kind is the display type: audience | walkin | presenter ("" = not
	// set yet). Rotation is 0/90/180/270 degrees (portrait poster screens).
	Kind     string `db:"kind"      json:"kind"`
	Rotation int    `db:"rotation"  json:"rotation"`
	// Template is a built-in layout the screen shows directly ("" = none):
	// built-ins are never edited; editing one makes an event layout
	// (STATUS U10/U11). A board_id > 0 wins over it.
	Template string `db:"template"  json:"template"`
}

// Display types (PRODUCT §3.2).
const (
	ScreenAudience  = "audience"
	ScreenWalkin    = "walkin"
	ScreenPresenter = "presenter"
)

// ValidScreenKind / ValidRotation guard the screen look settings.
func ValidScreenKind(k string) bool {
	return k == "" || k == ScreenAudience || k == ScreenWalkin || k == ScreenPresenter
}
func ValidRotation(r int) bool { return r == 0 || r == 90 || r == 180 || r == 270 }

// SetScreenLook sets a screen's display type and rotation (creating the
// registry row if needed).
func (d *DB) SetScreenLook(showID int64, name, kind string, rotation int) error {
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: empty screen name")
	}
	if !ValidScreenKind(kind) {
		return fmt.Errorf("timerpi: unknown display type %q", kind)
	}
	if !ValidRotation(rotation) {
		return fmt.Errorf("timerpi: rotation must be 0, 90, 180 or 270")
	}
	now := nowMS()
	_, err := d.Exec(`INSERT INTO screens (show_id, name, kind, rotation, last_seen) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET kind = ?, rotation = ?`, showID, name, kind, rotation, now, kind, rotation)
	return err
}

// SanitizeScreenName trims and bounds a screen name ("Stage Left" style:
// letters, digits, space, hyphen, underscore).
func SanitizeScreenName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > MaxScreenNameLen {
		name = name[:MaxScreenNameLen]
	}
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == ' ' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// UpsertScreen records a join (insert or last_seen refresh; operator
// overrides untouched).
func (d *DB) UpsertScreen(showID int64, name string) error {
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: empty screen name")
	}
	// Hardening: every JOIN upserts a registry row, and joins are
	// unauthenticated on screen/display roles — bound per show so a LAN
	// flood cannot grow the table before the 512-session cap bites.
	var n int64
	if err := d.Get(&n, `SELECT COUNT(*) FROM screens WHERE show_id = ?`, showID); err == nil && n >= maxScreenRows {
		var known int64
		if err := d.Get(&known, `SELECT COUNT(*) FROM screens WHERE show_id = ? AND name = ?`, showID, name); err == nil && known == 0 {
			return fmt.Errorf("timerpi: too many screens for show %d (cap %d)", showID, maxScreenRows)
		}
	}
	now := nowMS()
	if _, err := d.Exec(`INSERT INTO screens (show_id, name, last_seen) VALUES (?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET last_seen = ?`, showID, name, now, now); err != nil {
		return fmt.Errorf("timerpi: upsert screen: %w", err)
	}
	return nil
}

const maxScreenRows = 2000

// SetScreenConfig writes the operator's per-screen overrides (empty theme
// clears to the default; board 0 clears to the show board).
func (d *DB) SetScreenConfig(showID int64, name, theme string, boardID int64, room string) error {
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: empty screen name")
	}
	room = SanitizeScreenName(room)
	now := nowMS()
	if _, err := d.Exec(`INSERT INTO screens (show_id, name, theme, board_id, room, last_seen) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET theme = ?, board_id = ?, room = ?, last_seen = ?,
			template = CASE WHEN ? > 0 THEN '' ELSE template END`,
		showID, name, theme, boardID, room, now, theme, boardID, room, now, boardID); err != nil {
		return fmt.Errorf("timerpi: set screen config: %w", err)
	}
	return nil
}

// ListScreens returns the registered screens, most-recently-seen first.
func (d *DB) ListScreens(showID int64) ([]Screen, error) {
	var out []Screen
	err := d.Select(&out, `SELECT show_id, name, theme, board_id, room, last_seen, kind, rotation, template FROM screens
		WHERE show_id = ? ORDER BY last_seen DESC`, showID)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list screens: %w", err)
	}
	if out == nil {
		out = []Screen{}
	}
	return out, nil
}

// GetScreenByName fetches one registered screen ("" name → no rows error).
func (d *DB) GetScreenByName(showID int64, name string) (Screen, error) {
	var s Screen
	err := d.Get(&s, `SELECT show_id, name, theme, board_id, room, last_seen, kind, rotation, template FROM screens
		WHERE show_id = ? AND name = ?`, showID, name)
	if err != nil {
		return Screen{}, fmt.Errorf("timerpi: get screen: %w", err)
	}
	return s, nil
}

// RenameScreen moves a registry row (operator rename from the panel).
func (d *DB) RenameScreen(showID int64, from, to string) error {
	from, to = SanitizeScreenName(from), SanitizeScreenName(to)
	if from == "" || to == "" {
		return fmt.Errorf("timerpi: screen rename needs both names")
	}
	if from == to {
		return nil // identity rename must NOT delete the row it renames onto
	}
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM screens WHERE show_id = ? AND name = ?`, showID, to); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE screens SET name = ?, last_seen = ? WHERE show_id = ? AND name = ?`,
		to, nowMS(), showID, from); err != nil {
		return err
	}
	return tx.Commit()
}

// SetScreenTemplate makes the screen show a built-in layout directly
// ("" = none: back to the plain timer), clearing any layout assignment.
func (d *DB) SetScreenTemplate(showID int64, name, template string) error {
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: empty screen name")
	}
	now := nowMS()
	if _, err := d.Exec(`INSERT INTO screens (show_id, name, template, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET template = ?, board_id = 0, last_seen = ?`,
		showID, name, template, now, template, now); err != nil {
		return fmt.Errorf("timerpi: set screen template: %w", err)
	}
	return nil
}

// ScreenKey returns the screen's key, creating the screen row and a fresh
// random key when either is missing (BUGLOG RW9). The key rides the
// screen's URL (?key=) from capture or the Screens page "screen link"; only
// a keyed screen receives operator content (stage messages, notes, the
// Presenter item). Forgetting the screen (DeleteScreen) or deleting its
// room or event drops the key: the screen is released.
func (d *DB) ScreenKey(showID int64, name string) (string, error) {
	name = SanitizeScreenName(name)
	if name == "" {
		return "", fmt.Errorf("timerpi: screen key needs a name")
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	fresh := hex.EncodeToString(b[:])
	if _, err := d.Exec(`INSERT INTO screens (show_id, name, last_seen, key) VALUES (?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET key = CASE WHEN key = '' THEN excluded.key ELSE key END`,
		showID, name, nowMS(), fresh); err != nil {
		return "", fmt.Errorf("timerpi: screen key: %w", err)
	}
	var key string
	if err := d.Get(&key, `SELECT key FROM screens WHERE show_id = ? AND name = ?`, showID, name); err != nil {
		return "", fmt.Errorf("timerpi: screen key: %w", err)
	}
	return key, nil
}

// ScreenKeyValid reports whether key is the named screen's key.
func (d *DB) ScreenKeyValid(showID int64, name, key string) bool {
	name = SanitizeScreenName(name)
	if name == "" || key == "" {
		return false
	}
	var want string
	if err := d.Get(&want, `SELECT key FROM screens WHERE show_id = ? AND name = ?`, showID, name); err != nil || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(key)) == 1
}

// ClearScreenBoard drops the board assignment from every screen pointing
// at bid (called when a board is deleted — a stale id would navigate locked
// TVs to a 404 on every join push).
//
// Layouts are event-wide, so every screen of every room using it falls
// back (board ids are unique box-wide; showID is kept for callers).
func (d *DB) ClearScreenBoard(showID, bid int64) error {
	_, err := d.Exec(`UPDATE screens SET board_id = 0 WHERE board_id = ?`, bid)
	if err != nil {
		return fmt.Errorf("timerpi: clear screen board: %w", err)
	}
	return nil
}

// DeleteScreen forgets a registered screen (config gone; a still-live tab
// re-registers on its next join — that's the honest semantic).
func (d *DB) DeleteScreen(showID int64, name string) error {
	if _, err := d.Exec(`DELETE FROM screens WHERE show_id = ? AND name = ?`, showID, name); err != nil {
		return fmt.Errorf("timerpi: delete screen: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Waiting room (displays whose show vanished). A display whose join is
// refused registers itself here; any operator browser lists entries and can
// CAPTURE one into a live show (sets `assigned`), which the display's poll
// consumes and navigates to. Stale rows (>10 min silence) are pruned on
// every touch — a waiting display re-registers with each poll anyway.

// WaitingScreen is one registry row.
type WaitingScreen struct {
	ID       int64  `db:"id"         json:"id"`
	Name     string `db:"name"       json:"name"`
	Host     string `db:"host"       json:"host"`
	LastSeen int64  `db:"last_seen"  json:"lastSeen"`
	Assigned string `db:"assigned"   json:"-"`
	Screen   string `db:"screen"     json:"-"`
}

const waitingStaleAfterMS = 10 * 60 * 1000

// maxWaitingRows caps unassigned waiting screens (BUGLOG RW16): a loop of
// random names can't flood every operator's "Waiting" list.
const maxWaitingRows = 200

// ErrWaitingFull: the waiting list is at maxWaitingRows.
var ErrWaitingFull = errors.New("timerpi: too many screens waiting to be set up")

// RegisterWaiting upserts a waiting display (name sanitized; host free text,
// bounded). Empty name after sanitizing is refused.
func (d *DB) RegisterWaiting(name, host string) error {
	return d.RegisterWaitingToken(name, host, "")
}

// RegisterWaitingToken is RegisterWaiting with the tab's waiting token:
// the first token a row sees is the only one that may later claim its
// capture (and the screen key that comes with it; BUGLOG RW9).
func (d *DB) RegisterWaitingToken(name, host, token string) error {
	token = ClipUTF8(strings.TrimSpace(token), 64)
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: waiting register needs a name")
	}
	host = ClipUTF8(strings.TrimSpace(host), 80)
	now := nowMS()
	var fresh, known int64
	if err := d.Get(&fresh, `SELECT COUNT(*) FROM waiting_screens WHERE assigned = '' AND last_seen >= ?`, now-waitingStaleAfterMS); err != nil {
		return fmt.Errorf("timerpi: register waiting: %w", err)
	}
	if fresh >= maxWaitingRows {
		if err := d.Get(&known, `SELECT COUNT(*) FROM waiting_screens WHERE name = ? AND host = ?`, name, host); err != nil {
			return fmt.Errorf("timerpi: register waiting: %w", err)
		}
		if known == 0 {
			return ErrWaitingFull
		}
	}
	_, err := d.Exec(`INSERT INTO waiting_screens (name, host, last_seen, token) VALUES (?, ?, ?, ?)
		ON CONFLICT (name, host) DO UPDATE SET last_seen = ?,
			token = CASE WHEN token = '' THEN excluded.token ELSE token END`, name, host, now, token, now)
	if err != nil {
		return fmt.Errorf("timerpi: register waiting: %w", err)
	}
	return nil
}

// PruneWaiting removes rows silent past the stale window.
func (d *DB) PruneWaiting() {
	_, _ = d.Exec(`DELETE FROM waiting_screens WHERE last_seen < ?`, nowMS()-waitingStaleAfterMS)
}

// ListWaiting returns fresh rows by id.
func (d *DB) ListWaiting() ([]WaitingScreen, error) {
	d.PruneWaiting()
	var out []WaitingScreen
	err := d.Select(&out, `SELECT id, name, host, last_seen, assigned, screen FROM waiting_screens
		WHERE assigned = '' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list waiting: %w", err)
	}
	if out == nil {
		out = []WaitingScreen{}
	}
	return out, nil
}

// ClaimWaiting consumes an assignment for (name, host): returns the
// captured show code + the screen name to adopt, ONCE. The row is removed
// on claim — the display leaves the waiting room for good (PLAN §11.2).
func (d *DB) ClaimWaiting(name, host string) (string, string, error) {
	return d.ClaimWaitingToken(name, host, "")
}

// ClaimWaitingToken is ClaimWaiting for a tab holding a waiting token: a
// row registered with a token is only claimed by that same token, so a
// stranger polling with the screen's name can't steal its capture.
func (d *DB) ClaimWaitingToken(name, host, token string) (string, string, error) {
	name = SanitizeScreenName(name)
	host = ClipUTF8(strings.TrimSpace(host), 80)
	token = ClipUTF8(strings.TrimSpace(token), 64)
	if err := d.RegisterWaitingToken(name, host, token); err != nil && !errors.Is(err, ErrWaitingFull) {
		return "", "", nil
	}
	var row struct {
		Assigned string `db:"assigned"`
		Screen   string `db:"screen"`
		Token    string `db:"token"`
	}
	err := d.Get(&row, `SELECT assigned, screen, token FROM waiting_screens WHERE name = ? AND host = ?`, name, host)
	if err != nil || row.Assigned == "" {
		return "", "", nil
	}
	if row.Token != "" && subtle.ConstantTimeCompare([]byte(row.Token), []byte(token)) != 1 {
		return "", "", nil // not the tab that registered this screen
	}
	// Atomic win: the conditional UPDATE claims the row or loses it to a
	// racing claimant (hardening round — the read-then-delete window let
	// two displays both hop to the same capture).
	res, err := d.Exec(`UPDATE waiting_screens SET assigned = '' WHERE name = ? AND host = ? AND assigned = ?`, name, host, row.Assigned)
	if err != nil {
		return "", "", fmt.Errorf("timerpi: claim waiting: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", "", nil // lost the race — the other poller hops
	}
	if _, err := d.Exec(`DELETE FROM waiting_screens WHERE name = ? AND host = ?`, name, host); err != nil {
		return "", "", fmt.Errorf("timerpi: claim waiting: %w", err)
	}
	return row.Assigned, row.Screen, nil
}

// GetWaiting fetches one waiting row (sql.ErrNoRows when missing).
func (d *DB) GetWaiting(id int64) (WaitingScreen, error) {
	var w WaitingScreen
	err := d.Get(&w, `SELECT id, name, host, last_seen, assigned, screen FROM waiting_screens WHERE id = ?`, id)
	if err != nil {
		return WaitingScreen{}, err
	}
	return w, nil
}

// AssignWaiting marks a waiting row captured into a show code, carrying
// the screen name the display adopts on its hop (PLAN §11.2 capture modal).
// Only an unassigned row, or one already assigned to the same room (the
// operator correcting a capture before the screen hops), is taken (BUGLOG
// RW38): operators of two rooms capturing the same screen can't both win;
// the loser gets sql.ErrNoRows.
func (d *DB) AssignWaiting(id int64, code, screen string) error {
	res, err := d.Exec(`UPDATE waiting_screens SET assigned = ?, screen = ? WHERE id = ? AND (assigned = '' OR assigned = ?)`,
		code, SanitizeScreenName(screen), id, code)
	if err != nil {
		return fmt.Errorf("timerpi: assign waiting: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteWaiting dismisses a waiting row.
func (d *DB) DeleteWaiting(id int64) error {
	res, err := d.Exec(`DELETE FROM waiting_screens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("timerpi: delete waiting: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ---------------------------------------------------------------------------
// Display presets (F2: named screen-configuration bundles — theme +
// per-screen assignments — surviving restarts, exportable as JSON).
type DisplayPreset struct {
	ID        int64  `db:"id"         json:"id"`
	ShowID    int64  `db:"show_id"    json:"-"`
	Name      string `db:"name"       json:"name"`
	Data      string `db:"data"       json:"data"`
	UpdatedAt int64  `db:"updated_at" json:"updatedAt"`
}

// SavePreset creates or replaces the show's preset under name.
func (d *DB) SavePreset(showID int64, name, data string) (DisplayPreset, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(data) > 65536 {
		return DisplayPreset{}, fmt.Errorf("timerpi: bad preset (name required, data ≤64KiB)")
	}
	now := nowMS()
	if _, err := d.Exec(`INSERT INTO display_presets (show_id, name, data, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET data = ?, updated_at = ?`,
		showID, name, data, now, data, now); err != nil {
		return DisplayPreset{}, fmt.Errorf("timerpi: save preset: %w", err)
	}
	var p DisplayPreset
	if err := d.Get(&p, `SELECT id, show_id, name, data, updated_at FROM display_presets WHERE show_id = ? AND name = ?`, showID, name); err != nil {
		return DisplayPreset{}, fmt.Errorf("timerpi: read preset: %w", err)
	}
	return p, nil
}

// ListPresets returns the show's presets by name.
func (d *DB) ListPresets(showID int64) ([]DisplayPreset, error) {
	var out []DisplayPreset
	err := d.Select(&out, `SELECT id, show_id, name, data, updated_at FROM display_presets WHERE show_id = ? ORDER BY name`, showID)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list presets: %w", err)
	}
	if out == nil {
		out = []DisplayPreset{}
	}
	return out, nil
}

// GetPreset fetches one preset by id (scoped to the show).
func (d *DB) GetPreset(showID, id int64) (DisplayPreset, error) {
	var p DisplayPreset
	err := d.Get(&p, `SELECT id, show_id, name, data, updated_at FROM display_presets WHERE show_id = ? AND id = ?`, showID, id)
	if err != nil {
		return DisplayPreset{}, fmt.Errorf("timerpi: get preset: %w", err)
	}
	return p, nil
}

// DeletePreset removes one preset (scoped to the show).
func (d *DB) DeletePreset(showID, id int64) error {
	if _, err := d.Exec(`DELETE FROM display_presets WHERE show_id = ? AND id = ?`, showID, id); err != nil {
		return fmt.Errorf("timerpi: delete preset: %w", err)
	}
	return nil
}

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
