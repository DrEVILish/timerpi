package views

import (
	"testing"
	"time"
)

// ParseDuration had no tests at all: the
// dashboard quick-add parses operator input through here (owner decision
// 2026-10-05: bare = minutes, two-part = hours:minutes, Ns = seconds).
func TestParseDurationTable(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"30", 1_800_000},      // bare number = minutes
		{" 30 ", 1_800_000},    // surrounding whitespace tolerated
		{"30s", 30_000},        // explicit seconds
		{"90S", 90_000},        // suffix case-insensitive
		{"1:30", 5_400_000},    // h:mm
		{"05:00", 18_000_000},  // zero-padded h:mm (= 5 hours)
		{"1:00:05", 3_605_000}, // h:mm:ss
		{"", 0},                // empty
		{"   ", 0},             // whitespace-only
		{"banana", 0},          // not a number
		{"30x", 0},             // unknown suffix
		{"1:2:3:4", 0},         // too many parts
		{"1:xx", 0},            // bad minute part
		{"0", 0},               // zero minutes
		{"0:00", 0},            // zero h:mm
		{"-5", 0},              // negatives rejected (parity with JS)
		{"-30s", 0},            // seconds suffix too
		{"1::30", 0},           // empty part is garbage, not "1:30"
	}
	for _, tc := range cases {
		if got := ParseDuration(tc.in); got != tc.want {
			t.Errorf("ParseDuration(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// FmtAgo buckets wall-clock age for the UI.
func TestFmtAgoBoundaries(t *testing.T) {
	now := time.Now().UnixMilli()
	cases := []struct {
		name string
		ms   int64
		want string
	}{
		{"just now", now - 5_000, "just now"},
		{"minutes", now - 30*60_000, "30m ago"},
		{"hours", now - 5*3_600_000, "5h ago"},
		{"days", now - 3*86_400_000, "3d ago"},
	}
	for _, tc := range cases {
		if got := FmtAgo(tc.ms); got != tc.want {
			t.Errorf("%s: FmtAgo = %q, want %q", tc.name, got, tc.want)
		}
	}
}
