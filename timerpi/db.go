package timerpi

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3" // sqlite3 driver (CGO)
)

// DB wraps sqlx.DB with the TimerPi schema. SQLite is the single source of
// truth (PLAN §1): shows, cues, messages, settings kv and per-show runtime.
type DB struct {
	*sqlx.DB
	readers *sqlx.DB // WAL readers never occupy the single writer connection
	tx      *sqlx.Tx // private transaction view; never shared with live readers
	air     airCache // on-air interaction per room (polls.go OnAirNow)
	cueG    cueGens  // per-show cue write generation (engine cue cache, RW55)

	secretMu sync.Mutex
	secret   []byte // session signing key, cached (SessionSecret, RS3)
}

type cueGens struct {
	mu  sync.Mutex
	gen map[int64]uint64
}

// CueGen is the show's cue write generation: it changes after every cue
// write, so an engine can keep its cue list until then (BUGLOG RW55).
func (d *DB) CueGen(showID int64) uint64 {
	d.cueG.mu.Lock()
	defer d.cueG.mu.Unlock()
	return d.cueG.gen[showID]
}

// cuesChanged bumps the show's cue generation. Write paths defer it, so it
// also runs on failure: a spurious bump only costs one re-read.
func (d *DB) cuesChanged(showID int64) {
	d.cueG.mu.Lock()
	defer d.cueG.mu.Unlock()
	if d.cueG.gen == nil {
		d.cueG.gen = map[int64]uint64{}
	}
	d.cueG.gen[showID]++
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
	// Keep one writer; WAL readers use their own query-only pool.
	raw.SetMaxOpenConns(1)
	// Unsafe: a column the structs don't know yet (a newer migration, an
	// older binary on a newer file) is skipped instead of failing every
	// SELECT * read (BUGLOG RS36).
	d := &DB{DB: raw.Unsafe()}
	if err := d.migrate(); err != nil {
		raw.Close()
		return nil, err
	}
	var journal string
	if err := d.Get(&journal, "PRAGMA journal_mode"); err != nil {
		raw.Close()
		return nil, err
	}
	if journal == "wal" {
		readers, err := sqlx.Open("sqlite3", dsn+"&_query_only=true")
		if err != nil {
			raw.Close()
			return nil, err
		}
		readers.SetMaxOpenConns(4)
		readers.SetMaxIdleConns(4)
		if err := readers.Ping(); err != nil {
			readers.Close()
			raw.Close()
			return nil, err
		}
		d.readers = readers.Unsafe()
	}
	return d, nil
}

// Close releases both pools; transaction views must not close their owner.
func (d *DB) Close() error {
	var readErr error
	if d.readers != nil {
		readErr = d.readers.Close()
	}
	return errors.Join(readErr, d.DB.Close())
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
		`CREATE TABLE IF NOT EXISTS spent_nonces (
			nonce   TEXT PRIMARY KEY,
			expires INTEGER NOT NULL
		);`,
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
		// Entries are read by parent on every on-air recompute (BUGLOG RS28).
		`CREATE INDEX IF NOT EXISTS idx_polls_parent ON polls(parent)`,
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
			{"alert_flash1", "INTEGER NOT NULL DEFAULT 0"},
			{"alert_flash2", "INTEGER NOT NULL DEFAULT 0"},
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
			{"pair_code", "TEXT NOT NULL DEFAULT ''"},  // a box's 6-digit pairing code (VENUE-CLOUD §4)
			{"handheld", "INTEGER NOT NULL DEFAULT 0"}, // a phone/tablet: capture hides Mounted
			{"ip", "TEXT NOT NULL DEFAULT ''"},         // registering client IP (cloud scoping)
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
	// ftl's core/tokens bundles were once offered as themes; screens and
	// events on them fall back to the default (REPORT #18).
	for _, q := range []string{`UPDATE screens SET theme = '' WHERE theme IN ('tokens', 'core')`,
		`UPDATE events SET theme = '' WHERE theme IN ('tokens', 'core')`} {
		if _, err := d.Exec(q); err != nil {
			return fmt.Errorf("timerpi: theme fallback: %w", err)
		}
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
