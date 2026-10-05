// Package mesh implements the TimerPi device role state machine (PLAN §5):
// one appliance on the LAN becomes the primary (authority holder), the
// others join as members/displays, and on primary loss a member promotes
// itself (takeover). Discovery and announcement ride the mdns package
// (_timerpi._tcp TXT: host/role/ver/epoch).
//
// Agent H, 2026-10-03. routes/network.go mounts the HTTP surface;
// docs/MESH.md is the protocol spec.
package mesh

import (
	"database/sql"
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3" // sqlite3 driver (CGO) — same driver as timerpi.Open
)

// Store persists this device's mesh identity state in the SAME SQLite file
// the appliance already uses (<data>/timerpi.db), in an ADDITIVE table
// `mesh_state`. The main schema (timerpi/db.go) is not modified: this file
// owns its own migration runner (CREATE TABLE IF NOT EXISTS) so both
// processes/openers converge on the same file safely (WAL + busy_timeout).
//
// Keys are a tiny documented set (KeyClaimedEpoch, KeyRoleOverride); value
// encoding is plain text so an operator can inspect the DB with sqlite3.
type Store struct {
	*sqlx.DB
}

// meshPragmas mirror timerpi/db.go's connection pragmas (two openers on one
// WAL file: busy_timeout keeps writers polite; plancheck: FKs irrelevant
// for a kv table).
const meshPragmas = "_journal_mode=WAL&_busy_timeout=5000"

// mesh_state keys (the whole schema surface — do not ad-hoc new keys from
// other packages; add a named constant here instead).
const (
	// KeyClaimedEpoch holds our authority epoch (epoch-ms at first claim,
	// never re-randomized on restart). Smaller epoch = came first = wins
	// (see docs/MESH.md §epoch).
	KeyClaimedEpoch = "claimed_epoch"
	// KeyRoleOverride holds the operator override: "" (auto) | "primary" |
	// "member". POST /api/network/role writes it.
	KeyRoleOverride = "role_override"
)

// OpenStore opens the additive mesh store on the existing appliance DB
// file (path = <data dir>/timerpi.db, same convention as timerpi.Open).
// The sqlite driver schema guarantee: timerpi/db.go's tables are already
// there; if mesh_state does not exist yet this creates it. Both openers
// being present in one binary (main.go wires both) is fine.
func OpenStore(path string) (*Store, error) {
	raw, err := sqlx.Connect("sqlite3", path+"?"+meshPragmas)
	if err != nil {
		return nil, fmt.Errorf("mesh: opening store at %q: %w", path, err)
	}
	raw.SetMaxOpenConns(1) // SQLite: one writer; contention > concurrency
	s := &Store{DB: raw}
	if err := s.migrate(); err != nil {
		raw.Close()
		return nil, err
	}
	return s, nil
}

// migrate is the additive runner: idempotent CREATE IF NOT EXISTS only.
// It MUST stay additive — the base schema belongs to timerpi/db.go.
func (s *Store) migrate() error {
	_, err := s.Exec(`CREATE TABLE IF NOT EXISTS mesh_state (
		key   TEXT PRIMARY KEY NOT NULL,
		value TEXT NOT NULL DEFAULT ''
	);`)
	if err != nil {
		return fmt.Errorf("mesh: schema: %w", err)
	}
	return nil
}

// Get returns the value for key ("" when unset — missing keys are not
// errors, mirroring timerpi's settings semantics).
func (s *Store) Get(key string) (string, error) {
	if s == nil {
		return "", nil
	}
	var v string
	err := s.DB.Get(&v, `SELECT value FROM mesh_state WHERE key = ?`, key)
	if err == sql.ErrNoRows || err == nil && v == "" {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("mesh: get %s: %w", key, err)
	}
	return v, nil
}

// GetInt64 returns the parsed integer value (ok=false when unset/unparsable).
func (s *Store) GetInt64(key string) (int64, bool, error) {
	raw, err := s.Get(key)
	if err != nil || raw == "" {
		return 0, false, err
	}
	v, perr := strconv.ParseInt(raw, 10, 64)
	if perr != nil {
		return 0, false, nil
	}
	return v, true, nil
}

// Set upserts a key.
func (s *Store) Set(key, value string) error {
	if s == nil {
		return nil
	}
	_, err := s.Exec(`INSERT INTO mesh_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("mesh: set %s: %w", key, err)
	}
	return nil
}

// SetInt64 stores an integer key.
func (s *Store) SetInt64(key string, v int64) error {
	return s.Set(key, strconv.FormatInt(v, 10))
}
