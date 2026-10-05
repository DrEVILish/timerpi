// Package timerpi — gen.go: public show share codes (Agent L/FIX-L).
//
// Every show carries a public, shareable, unique 8-character code
// (`shows.code`), displayed 4-4 ("K7QP-M3XB") and joined via QR + typed
// entry. This file owns the alphabet, generation and normalization rules —
// the single definition of record for both this package and any client
// mirror (public/src/engine.js fmtCode/normalizeCode parity):
//
//   - Alphabet: Crockford-style base32 minus I, L, O, U — i.e. the 32
//     characters "0123456789ABCDEFGHJKMNPQRSTVWXYZ" (S is KEPT: removing
//     it buys no real safety and shrinks the space). Uppercase output;
//     input is case-insensitive.
//   - Storage is the bare 8 characters (no dash); the 4-4 dash is display
//     sugar (FmtCode) and is stripped on input.
//   - Generation uses crypto/rand only (8 independent alphabet picks,
//     uniform); collision handling is a DB-checked retry loop at the call
//     site (CreateShow / backfill), not trusted modulo luck.
//
// Typo tolerance on INPUT normalization (NormalizeCode) maps the excluded
// letters onto their plausible confusions: I→1, L→1, O→0, U→V.
// (0 itself is IN the generation alphabet, so there is deliberately no
// 0→O mapping the other way: digits stay digits.)
package timerpi

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"
)

// CodeAlphabet is the 32-character code alphabet in Crockford base32
// order with I, L, O, U removed (see the file comment).
const CodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// codeLen is the number of characters in a code (8 → 4-4 display).
// 32^8 ≈ 1.0995e12: far beyond the show counts of one appliance, so the
// retry loop below essentially never spins more than once.
const codeLen = 8

// cryptoRand is the randomness seam (tests force collisions by swapping
// it out). Mirrors ws/session.go's orGenID pattern.
var cryptoRand = rand.Read

// newCodeRaw draws one code from crypto/rand (8 uniform alphabet picks).
func newCodeRaw() (string, error) {
	b := make([]byte, codeLen)
	if _, err := cryptoRand(b); err != nil {
		return "", fmt.Errorf("timerpi: code entropy: %w", err)
	}
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(CodeAlphabet[int(c)%len(CodeAlphabet)])
	}
	return sb.String(), nil
}

// NewCode generates a fresh code that does not collide with any code
// already in the DB, retrying on collision (and surfacing repeated
// collisions only as a last-resort error). Callers must still insert with
// the UNIQUE index live so a concurrent win cannot double-book.
func NewCode(d *DB) (string, error) {
	for attempt := 0; attempt < 64; attempt++ {
		code, err := newCodeRaw()
		if err != nil {
			return "", err
		}
		inUse, err := codeInUse(d, code)
		if err != nil {
			return "", err
		}
		if !inUse {
			return code, nil
		}
	}
	return "", fmt.Errorf("timerpi: code generation starved (64 collisions)")
}

// codeInUse reports whether a show already holds code (false = free).
func codeInUse(d *DB, code string) (bool, error) {
	var one int64
	// Event and room codes share one namespace: a typed code must never
	// be ambiguous between the two.
	err := d.Get(&one, `SELECT 1 FROM shows WHERE code = ? UNION SELECT 1 FROM events WHERE code = ?`, code, code)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("timerpi: code lookup %q: %w", code, err)
	}
	return true, nil
}

// FmtCode renders the bare 8-char code in 4-4 groups ("AAAA-BBBB").
// A short input is left mostly as-is (caller decides what to show); the
// canonical path is an 8-char stored code.
func FmtCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != codeLen {
		return code
	}
	return code[:4] + "-" + code[4:]
}

// normalizeTypo maps a character typed by a human onto the canonical
// alphabet character it plausibly meant (excluded letters only).
func normalizeTypo(r rune) rune {
	switch r {
	case 'I', 'L':
		return '1'
	case 'O':
		return '0'
	case 'U':
		return 'V'
	}
	return r
}

// NormalizeCode canonicalizes operator-typed / URL-embedded codes:
// strip every non-alphanumeric byte (the 4-4 dash, spaces), uppercase,
// apply the typo maps (I→1, L→1, O→0, U→V), and drop anything that is not
// in the alphabet afterwards. Returns at most codeLen characters.
func NormalizeCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	var sb strings.Builder
	for _, r := range s {
		if r == ' ' || r == '-' {
			continue
		}
		r = normalizeTypo(r)
		if strings.ContainsRune(CodeAlphabet, r) {
			sb.WriteRune(r)
			if sb.Len() >= codeLen {
				break
			}
		}
	}
	return sb.String()
}

// ValidCode reports whether s is exactly a canonical stored code
// (8 characters, all from the alphabet).
func ValidCode(s string) bool {
	if len(s) != codeLen {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(CodeAlphabet, r) {
			return false
		}
	}
	return true
}

// ResolveShowID resolves a public `:ident` path parameter to a show's
// numeric row id. SCOPE (Agent L, revised): CODE-ONLY addressing —
//
//   - the ident is NormalizeCode'd (typo maps I→1, L→1, O→0, U→V; the
//     4-4 dash/spaces strip), then must be a full 8-char code present in
//     `shows.code`; success → that show's internal row id.
//   - ALL-DIGIT idents are refused outright (found=false): digits are the
//     legacy numeric-id world and must NOT resolve — a show whose code
//     happens to be all digits is a deliberate, astronomically unlikely
//     corner (10^-7 per show realistically) that the missed lookup
//     otherwise covers; the refusal rules the contract, not the corner.
//   - `shows.id` remains the internal FK/JSON bookkeeping key — it is
//     simply no longer an address.
//
// found=false for every miss/invalid shape (callers answer 404 /
// "Unknown session code").
func ResolveShowID(d *DB, ident string) (int64, bool) {
	ident = strings.TrimSpace(ident)
	if ident == "" || d == nil {
		return 0, false
	}
	if _, ok := ParseNumericID(ident); ok {
		return 0, false // digits are legacy numeric addresses: refused
	}
	code := NormalizeCode(ident)
	if !ValidCode(code) {
		return 0, false
	}
	var s Show
	err := d.Get(&s, `SELECT * FROM shows WHERE code = ?`, code)
	if err != nil {
		return 0, false
	}
	return s.ID, true
}

// ParseNumericID is the shared "is this ident a row id" shape test
// (all digits, fits in int64, > 0). It does NOT resolve anything by
// itself; public routes use it only to REFUSE digits (legacy world).
func ParseNumericID(ident string) (int64, bool) {
	if ident == "" {
		return 0, false
	}
	for i := 0; i < len(ident); i++ {
		if ident[i] < '0' || ident[i] > '9' {
			return 0, false
		}
	}
	if len(ident) > 18 { // not all digits fit int64 comfortably
		return 0, false
	}
	var id int64
	for i := 0; i < len(ident); i++ {
		id = id*10 + int64(ident[i]-'0')
	}
	if id <= 0 {
		return 0, false
	}
	return id, true
}
