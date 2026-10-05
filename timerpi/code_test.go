// package timerpi tests — show share codes (Agent L/FIX-L): the
// Crockford-style code alphabet/rules (gen.go), generation/collision
// handling, the END-of-chain shows.code migration (backfill uniqueness),
// and the code-only resolve contract (scope change: numeric ids are
// internal DB keys, refused as addresses).
package timerpi

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

// openCodeDB is a fresh :memory: DB (migrations run, zero shows).
func openCodeDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// TestCodeAlphabetAndFormat pins the alphabet/format decision of record:
// 32 chars (Crockford base32 minus I, L, O, U; digits 0-9 stay), 8 picks,
// output uppercase, display in 4-4 groups.
func TestCodeAlphabetAndFormat(t *testing.T) {
	want := "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	if got := CodeAlphabet; got != want {
		t.Fatalf("alphabet = %q, want %q", got, want)
	}
	for _, c := range []rune{'I', 'L', 'O', 'U'} {
		if strings.ContainsRune(CodeAlphabet, c) {
			t.Errorf("alphabet contains excluded letter %q", c)
		}
	}
	if len([]rune(CodeAlphabet)) != 32 {
		t.Errorf("alphabet size = %d, want 32", len([]rune(CodeAlphabet)))
	}
	if got := FmtCode("K7QPM3XB"); got != "K7QP-M3XB" {
		t.Errorf("FmtCode(8-char) = %q, want K7QP-M3XB", got)
	}
}

// TestNormalizeCode pins canonicalization + the typo maps.
func TestNormalizeCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"k7qp-m3xb", "K7QPM3XB"},     // lowercase + dash
		{"K7QP M3XB", "K7QPM3XB"},     // space
		{"k7qp!m3xb", "K7QPM3XB"},     // stray punctuation
		{"ILOU", "110V"},              // typo maps I→1, L→1, O→0, U→V
		{"ilou", "110V"},              // …case-insensitive
		{"KU1P-L3XB", "KV1P13XB"},     // U→V and L→1 mid-code
		{"K7QP-M3XBILOU", "K7QPM3XB"}, // capped at codeLen
	}
	for _, tc := range cases {
		if got := NormalizeCode(tc.in); got != tc.want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Punctuation-only input leaves nothing at all. (A word like "cod" is
	// alphabet-legal by design — letters live in the alphabet — and simply
	// misses the lookup; that's the honest miss, not an error.)
	if got := NormalizeCode("!!!  --"); got != "" {
		t.Errorf("punctuation-only input = %q, want empty", got)
	}
	if !ValidCode(NormalizeCode("  k7 qp m3 xb  ")) {
		t.Error("normalized code is not ValidCode")
	}
}

// TestGenRoundTripCharset: generation produces nothing outside the
// alphabet, always uppercase, and always a valid stored code.
func TestGenRoundTripCharset(t *testing.T) {
	d := openCodeDB(t)
	for i := 0; i < 200; i++ {
		code, err := NewCode(d)
		if err != nil {
			t.Fatalf("NewCode: %v", err)
		}
		if !ValidCode(code) {
			t.Fatalf("NewCode produced non-canonical %q", code)
		}
		if code != strings.ToUpper(code) {
			t.Fatalf("NewCode produced lowercase %q", code)
		}
	}
}

// TestCodeCollisionRetry forces a collision by hijacking the randomness
// seam: the next draw replays an EXISTING show's code; CreateShow must
// retry inside its loop and land on a fresh, distinct code.
func TestCodeCollisionRetry(t *testing.T) {
	d := openCodeDB(t)
	first, err := d.CreateShow("Collide Me")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Draw bytes replay first.Code once (forced collision), then fall
	// back to REAL crypto entropy for every later draw.
	seq := make(chan byte, 8)
	seq <- byte(strings.Index(CodeAlphabet, first.Code[0:1]))
	seq <- byte(strings.Index(CodeAlphabet, first.Code[1:2]))
	seq <- byte(strings.Index(CodeAlphabet, first.Code[2:3]))
	seq <- byte(strings.Index(CodeAlphabet, first.Code[3:4]))
	seq <- byte(strings.Index(CodeAlphabet, first.Code[4:5]))
	seq <- byte(strings.Index(CodeAlphabet, first.Code[5:6]))
	seq <- byte(strings.Index(CodeAlphabet, first.Code[6:7]))
	seq <- byte(strings.Index(CodeAlphabet, first.Code[7:8]))

	prev := cryptoRand
	cryptoRand = func(b []byte) (int, error) {
		for i := range b {
			select {
			case b[i] = <-seq: // replayed byte (breeds the collision)
			default: // exhausted → honest entropy for later draws
				if _, err := rand.Read(b[i:]); err != nil {
					return 0, err
				}
				return len(b), nil
			}
		}
		return len(b), nil
	}
	defer func() { cryptoRand = prev }()

	second, err := d.CreateShow("Second Show")
	if err != nil {
		t.Fatalf("create with forced collision: %v", err)
	}
	if second.Code == first.Code {
		t.Fatalf("collision not retried: both shows share %q", first.Code)
	}
	if !ValidCode(second.Code) {
		t.Fatalf("retry produced non-canonical %q", second.Code)
	}
}

// TestCodeEntropyFailureSurfaces: a broken randomness source must not be
// swallowed (no silent fallback showing codes nobody generated).
func TestCodeEntropyFailureSurfaces(t *testing.T) {
	d := openCodeDB(t)
	prev := cryptoRand
	cryptoRand = func([]byte) (int, error) { return 0, errors.New("entropy broken") }
	defer func() { cryptoRand = prev }()
	if _, err := d.CreateShow("No Entropy"); err == nil {
		t.Fatal("entropy failure swallowed — CreateShow must surface it")
	}
}

// TestCreateShowCodeUniqueInBatch: many creates → all codes canonical and
// distinct (the UNIQUE index anchors the guarantee behind the retry loop).
func TestCreateShowCodeUniqueInBatch(t *testing.T) {
	d := openCodeDB(t)
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		s, err := d.CreateShow("Batch Show")
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if !ValidCode(s.Code) {
			t.Fatalf("code %q not canonical", s.Code)
		}
		if seen[s.Code] {
			t.Fatalf("duplicate code %q", s.Code)
		}
		seen[s.Code] = true
	}
	if len(seen) != 64 {
		t.Fatalf("saw %d distinct codes, want 64", len(seen))
	}
}

// TestMigrationBackfillsDistinctCodes: a PRE-migration DB (shows created
// before the code column — N rows via raw SQL) gains one DISTINCT code per
// show when the migration chain runs; the UNIQUE index lands AFTER the
// backfill and can never see a duplicate.
func TestMigrationBackfillsDistinctCodes(t *testing.T) {
	raw, err := sqlx.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer raw.Close()
	oldSchema := []string{
		`CREATE TABLE shows (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			title      TEXT NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE TABLE cues (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			show_id    INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
			pos        INTEGER NOT NULL,
			label      TEXT NOT NULL DEFAULT '',
			UNIQUE (show_id, pos)
		);`,
		// Base-shape legacy tables (the ones migrate() may still index):
		`CREATE TABLE messages (
			show_id    INTEGER NOT NULL,
			id         INTEGER PRIMARY KEY
		);`,
		`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '');`,
		`CREATE TABLE runtime_state (show_id INTEGER PRIMARY KEY);`,
	}
	for _, s := range oldSchema {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("old schema: %v (%q)", err, s)
		}
	}
	const n = 7
	for i := 1; i <= n; i++ {
		if _, err := raw.Exec(`INSERT INTO shows (title) VALUES (?)`, strings.Repeat("z", i)); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	// Run the SAME migrate() the opens use, over the legacy table shape.
	d := &DB{DB: raw}
	d.SetMaxOpenConns(1)
	if err := d.migrate(); err != nil {
		t.Fatalf("migrate over legacy shows table: %v", err)
	}

	rows, err := d.ListShows()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != n {
		t.Fatalf("rows = %d, want %d", len(rows), n)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if !ValidCode(r.Code) {
			t.Fatalf("backfilled code %q not canonical", r.Code)
		}
		if seen[r.Code] {
			t.Fatalf("backfill duplicate %q", r.Code)
		}
		seen[r.Code] = true
	}
	if len(seen) != n {
		t.Fatalf("%d distinct backfilled codes, want %d", len(seen), n)
	}
	// The unique index must now exist and hold (a duplicate insert fails).
	if _, err := d.Exec(`INSERT INTO shows (title, code) VALUES ('dupe', ?)`, rows[0].Code); err == nil {
		t.Fatal("UNIQUE index not enforcing: duplicate code insert succeeded")
	}
}

// TestResolveCodeOnly pins the SCOPE CHANGE contract: valid code spellings
// resolve (normalized), all-digit idents and junk refuse, and a well-formed
// but absent code is a plain miss.
func TestResolveCodeOnly(t *testing.T) {
	d := openCodeDB(t)
	s, err := d.CreateShow("Resolve Me")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Canonical, 4-4 and lowercase spellings resolve to the same show.
	for _, ident := range []string{s.Code, s.Code[:4] + "-" + s.Code[4:], strings.ToLower(s.Code)} {
		id, ok := ResolveShowID(d, ident)
		if !ok || id != s.ID {
			t.Errorf("resolve %q = (%d, %v), want (%d, true)", ident, id, ok, s.ID)
		}
	}

	// Typo maps up a resolvable spelling (0 survived generation: swap a
	// MAPPED letter pair instead — 1→input I, V→input U).
	confusable := map[string]string{"0": "O", "1": "I", "V": "U"}
	for i := 0; i < len(s.Code); i++ {
		if rep, has := confusable[s.Code[i:i+1]]; has {
			typo := s.Code[:i] + rep + s.Code[i+1:]
			id, ok := ResolveShowID(d, strings.ToLower(typo))
			if !ok || id != s.ID {
				t.Errorf("typo %q → (%d, %v), want the same show", typo, id, ok)
			}
			break
		}
	}

	// Refusals: digits (the legacy numeric world), non-codes, empty.
	for _, ident := range []string{"1", "424242", "0123", "not-a-code", "K7QP", "", "abcdefgh"} {
		if id, ok := ResolveShowID(d, ident); ok {
			t.Errorf("resolve %q = (%d, true), want refused", ident, id)
		}
	}
	// A well-formed but absent code is a plain miss.
	if id, ok := ResolveShowID(d, "ZZZZZZZZ"); ok {
		t.Errorf("ZZZZZZZZ = (%d, true), want false", id)
	}
}

// TestGetShowByCode uses the raw code accessor (routes go through
// ResolveShowID; this covers the db.go helper directly).
func TestGetShowByCode(t *testing.T) {
	d := openCodeDB(t)
	s, err := d.CreateShow("By code")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := d.GetShowByCode(s.Code)
	if err != nil || got.ID != s.ID {
		t.Fatalf("GetShowByCode(%q) = (%v, %v), want id %d", s.Code, got.ID, err, s.ID)
	}
	if _, err := d.GetShowByCode("ZZZZZZZZ"); err == nil {
		t.Fatal("absent code lookup returned rows")
	}
}

// ---------------------------------------------------------------- edges ----

// TestGenNoVowelsNorConfusables: generated codes never contain the excluded
// characters (I/L/O/U) or digits outside 0-9 — operators read codes aloud.
func TestGenNoExcludedCharacters(t *testing.T) {
	for i := 0; i < 500; i++ {
		code, err := newCodeRaw()
		if err != nil {
			t.Fatalf("newCodeRaw: %v", err)
		}
		if len(code) != codeLen {
			t.Fatalf("gen len = %d", len(code))
		}
		for _, r := range code {
			switch r {
			case 'I', 'L', 'O', 'U':
				t.Fatalf("gen produced excluded char %q in %q", r, code)
			}
		}
	}
}

// TestValidCodeTable: the validator is the LAST gate — it must reject every
// malformed shape the UI can hand it.
func TestValidCodeTable(t *testing.T) {
	cases := map[string]bool{
		"K7QPM3XB":  true,  // canonical
		"00000000":  true,  // all digits (0 is in alphabet)
		"ILOUILOU":  false, // excluded chars
		"K7QPM3X":   false, // short
		"K7QPM3XB1": false, // long
		"":          false,
		"K7QP M3XB": false, // not canonicalized
		"к7qpm3xb":  false, // cyrillic homoglyph
	}
	for in, want := range cases {
		if got := ValidCode(in); got != want {
			t.Errorf("ValidCode(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestNormalizeCodeConfusables: homoglyph-looking and pasted-with-unicode
// inputs either normalize cleanly to alphabet characters or are dropped —
// they never crash and never smuggle in exotic characters.
func TestNormalizeCodeAdversarial(t *testing.T) {
	for _, in := range []string{
		"Κ7QP-M3XB",         // greek Kappa lookalike
		"K7QP-M3XB\u200b",   // zero-width space
		"\n\tK7QPM3XB\n",    // whitespace
		"-K-7-Q-P-M-3-X-B-", // dashes everywhere
		"K7QP+M3XB=42",      // mixed junk past cap
	} {
		got := NormalizeCode(in)
		if len(got) > codeLen {
			t.Errorf("NormalizeCode(%q) len %d > %d", in, len(got), codeLen)
		}
		for _, r := range got {
			if !strings.ContainsRune(CodeAlphabet, r) {
				t.Errorf("NormalizeCode(%q) smuggled %q", in, r)
			}
		}
	}
}
