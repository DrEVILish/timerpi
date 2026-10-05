package timerpi

import (
	"strings"
	"testing"
)

// --- Domain validation / normalization (no test fed bad enums before) ------

// Cue.Validate must refuse every bad enum and negative magnitude; the
// lifecycle/replace tests only ever feed valid sampleCues.
func TestCueValidateRejectsBadEnums(t *testing.T) {
	good := sampleCue(0, "ok")
	if err := good.Validate(); err != nil {
		t.Fatalf("valid cue refused: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Cue)
		want string
	}{
		{"bad kind", func(c *Cue) { c.Kind = "bogus" }, "kind"},
		{"bad timerKind", func(c *Cue) { c.TimerKind = "EGG" }, "timerKind"},
		{"bad endAction", func(c *Cue) { c.EndAction = "VANISH" }, "endAction"},
		{"negative duration", func(c *Cue) { c.DurationMS = -1 }, "negative"},
		{"negative hold", func(c *Cue) { c.HoldMS = -1 }, "negative"},
		{"negative alert", func(c *Cue) { c.Alert1MS = -1 }, "negative"},
		{"bad color", func(c *Cue) { c.Color = "red" }, "colour"},
		{"bad alert color", func(c *Cue) { c.AlertColor2 = "#zzzzzz" }, "colour"},
		{"bad startAt", func(c *Cue) { c.StartAt = "25:99" }, "startAt"},
		{"bad startAt text", func(c *Cue) { c.StartAt = "nine" }, "startAt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := sampleCue(0, "x")
			tc.mut(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

// Cue.Normalize fills the operator defaults and zeroes negative magnitudes
// (only merge_test called it, never for defaults).
func TestCueNormalizeFillsDefaults(t *testing.T) {
	c := Cue{Label: "bare"}
	c.Normalize()
	if c.Kind != KindSession || c.TimerKind != TimerCountdown || c.EndAction != EndHold {
		t.Fatalf("enums not defaulted: %+v", c)
	}
	if c.AlertColor1 != DefaultAlertColor1 || c.AlertColor2 != DefaultAlertColor2 {
		t.Fatalf("alert colors not defaulted: %+v", c)
	}
	c = Cue{Label: "neg", DurationMS: -5, HoldMS: -5, Alert1MS: -5, Alert2MS: -5}
	c.Normalize()
	if c.DurationMS != 0 || c.HoldMS != 0 || c.Alert1MS != 0 || c.Alert2MS != 0 {
		t.Fatalf("negatives not zeroed: %+v", c)
	}
	// Explicit values survive normalization.
	c = sampleCue(0, "full")
	c.AlertColor1, c.AlertColor2 = "#111111", "#222222"
	before := c
	c.Normalize()
	if c != before {
		t.Fatalf("Normalize mutated an explicit cue: %+v", c)
	}
}

// --- DB edges ---------------------------------------------------------------

// ReorderCues with an unknown ID must ignore it (its documented contract),
// not let it consume a position and leave a hole in the dense 1..N order.
func TestReorderCuesUnknownIDsIgnored(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Reorder")
	var ids []int64
	for _, l := range []string{"a", "b", "c"} {
		c, err := d.CreateCue(show.ID, sampleCue(0, l))
		if err != nil {
			t.Fatalf("CreateCue: %v", err)
		}
		ids = append(ids, c.ID)
	}
	// Unknown ID first (worst slot for a hole) + a genuine reverse.
	if err := d.ReorderCues(show.ID, []int64{99997, ids[2], ids[0], ids[1]}); err != nil {
		t.Fatalf("ReorderCues: %v", err)
	}
	cues, err := d.ListCues(show.ID)
	if err != nil {
		t.Fatalf("ListCues: %v", err)
	}
	if len(cues) != 3 {
		t.Fatalf("cue count = %d, want 3", len(cues))
	}
	for i, c := range cues {
		if c.Pos != int64(i+1) {
			t.Fatalf("hole in dense order: pos=%d at index %d (%+v)", c.Pos, i, cues)
		}
	}
	if cues[0].Label != "c" || cues[1].Label != "a" || cues[2].Label != "b" {
		t.Fatalf("order wrong: %+v", cues)
	}
	if _, err := d.GetCue(show.ID, 1); err != nil {
		t.Fatalf("GetCue(1) after reorder: %v", err)
	}
}

// ReplaceCues with an empty slice clears the show (import of an empty-but-
// valid document), it must not error or leave ghosts.
func TestReplaceCuesEmptyClears(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Cleared")
	if _, err := d.CreateCue(show.ID, sampleCue(0, "doomed")); err != nil {
		t.Fatalf("CreateCue: %v", err)
	}
	if err := d.ReplaceCues(show.ID, nil); err != nil {
		t.Fatalf("ReplaceCues(nil): %v", err)
	}
	cues, err := d.ListCues(show.ID)
	if err != nil {
		t.Fatalf("ListCues: %v", err)
	}
	if len(cues) != 0 {
		t.Fatalf("cues after empty replace: %+v", cues)
	}
}
