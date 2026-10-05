package timerpi

import (
	"errors"
	"strings"
	"testing"
)

// ClipUTF8 must cut on rune boundaries (byte-slice truncation stored
// invalid UTF-8 tails in the action log / client errors / preset names).
func TestClipUTF8RuneSafe(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"héllo", 3, "hé"},  // h + 2-byte é land whole
		{"héllo", 4, "hél"}, // s[4] is é-continuation byte: walk back to 3
		{"日本", 3, "日"},      // 3-byte runes
		{"aé b", 3, "aé"},
		{"🎙️x", 4, "🎙"}, // 4-byte rune exactly fits; at 3 → ""
		{"🎙️x", 3, ""},
	}
	for _, c := range cases {
		got := ClipUTF8(c.in, c.n)
		if got != c.want {
			t.Errorf("ClipUTF8(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
		if strings.ToValidUTF8(got, "\ufffd") != got {
			t.Errorf("ClipUTF8(%q,%d) emitted invalid UTF-8: %q", c.in, c.n, got)
		}
	}
}

// Renaming a screen onto its own name is a no-op — it must NOT delete the
// row it renames onto (the DELETE-then-UPDATE phase order destroyed it).
func TestRenameScreenIdentityKeepsConfig(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "RenameId")
	if err := d.SetScreenConfig(show.ID, "Stage Left", "blue-future", 3); err != nil {
		t.Fatal(err)
	}
	if err := d.RenameScreen(show.ID, "Stage Left", "Stage Left"); err != nil {
		t.Fatalf("identity rename: %v", err)
	}
	s, err := d.GetScreenByName(show.ID, "Stage Left")
	if err != nil {
		t.Fatalf("config lost through identity rename: %v", err)
	}
	if s.Theme != "blue-future" || s.BoardID != 3 {
		t.Fatalf("config mutated: %+v", s)
	}
}

// goLocked zeroes the spent elapsed BEFORE startLocked can fail (the cue can
// vanish under REST between hold and GO). runMutation has no rollback, so a
// failed GO must leave the held state byte-identical.
func TestGoFailureDoesNotUnhold(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	clk := &fakeClock{ms: t0}
	cues := testCues() // closure below reads the VARIABLE, not a snapshot
	e, err := NewEngine(1, EngineDeps{
		Now:  clk.Now,
		Show: func() (Show, error) { return Show{ID: 1, Title: "Rollback"}, nil },
		Cues: func() ([]Cue, error) { return cues, nil },
		Save: func(Runtime) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	clk.ms += 600_001
	if err := e.Tick(clk.Now()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	before := e.Runtime()
	if before.Running || before.PausedElapsedMS != 600_000 {
		t.Fatalf("setup: not held: %+v", before)
	}
	cues = cues[1:] // cue 1 disappears from under the playhead
	if gerr := e.Go(); !errors.Is(gerr, ErrUnknownPos) {
		t.Fatalf("Go with vanished cue: %v, want ErrUnknownPos", gerr)
	}
	after := e.Runtime()
	if after.PausedElapsedMS != 600_000 || after.Running {
		t.Fatalf("failed GO mutated held runtime: %+v", after)
	}
}
