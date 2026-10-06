package importdocs

import (
	"fmt"

	"timerpi/timerpi"
)

// This adapter bridges the self-contained mirror Cue to the domain package
// (importdocs Cue → timerpi.Cue with Normalize+Validate). It lives in its own
// file so Agent D-full can build importdocs standalone (e.g. if the domain
// package is mid-refactor, dropping this one file from the worktree unbreaks
// the build).

// ToTimerpiCue converts one parsed cue to the domain type. The domain's
// Normalize is applied: empty kind/timerKind/endAction fall back to
// session/COUNTDOWN/HOLD and alert colours to defaults; Validate then checks
// the enum fields and colours.
func (c Cue) ToTimerpiCue() (timerpi.Cue, error) {
	tc := timerpi.Cue{
		Label:        c.Label,
		DurationMS:   c.DurationMS,
		Kind:         c.Kind,
		Tags:         c.Tags,
		Speaker:      c.Speaker,
		HoldMS:       c.HoldMS,
		TimerKind:    c.TimerKind, // BUGLOG RW28: stopwatch/clock sessions stay so
		Alert1MS:     c.Alert1MS,
		Alert2MS:     c.Alert2MS,
		AlertColor1:  c.AlertColor1,
		AlertColor2:  c.AlertColor2,
		EndAction:    c.EndAction,
		AutoContinue: c.AutoContinue,
		Notes:        c.Notes,
		Color:        c.Color,
		StartAt:      c.StartAt,
		Location:     c.Location,
	}
	// The domain's Normalize fills empty kind/timerKind/endAction and alert
	// colours with the PROTOCOL defaults and clamps negatives.
	tc.Normalize()
	if err := tc.Validate(); err != nil {
		return timerpi.Cue{}, fmt.Errorf("importdocs: cue %q: %w", c.Label, err)
	}
	return tc, nil
}

// ToTimerpiCues converts a whole imported document, in order. A failed row is
// reported with its 1-based position (first error wins). Every cue is left
// with Pos 0: REPLACE flows (db.ReplaceCues) renumber 1..N themselves, and
// append flows (db.CreateCue with Pos 0) append to the end.
func ToTimerpiCues(cues []Cue) ([]timerpi.Cue, error) {
	out := make([]timerpi.Cue, 0, len(cues))
	for i, c := range cues {
		tc, err := c.ToTimerpiCue()
		if err != nil {
			return nil, fmt.Errorf("importdocs: cue %d: %w", i+1, err)
		}
		out = append(out, tc)
	}
	return out, nil
}

// FromTimerpiCue converts a domain cue BACK to the mirror struct for export
// (JSON download / re-parse). It keeps the order element semantics stable and
// is lossless for every field a document can carry.
func FromTimerpiCue(tc timerpi.Cue) Cue {
	return Cue{
		Label:        tc.Label,
		DurationMS:   tc.DurationMS,
		Kind:         tc.Kind,
		Tags:         tc.Tags,
		Speaker:      tc.Speaker,
		HoldMS:       tc.HoldMS,
		TimerKind:    tc.TimerKind,
		Alert1MS:     tc.Alert1MS,
		Alert2MS:     tc.Alert2MS,
		AlertColor1:  tc.AlertColor1,
		AlertColor2:  tc.AlertColor2,
		EndAction:    tc.EndAction,
		AutoContinue: tc.AutoContinue,
		Notes:        tc.Notes,
		Color:        tc.Color,
		StartAt:      tc.StartAt,
		Location:     tc.Location,
	}
}

// FromTimerpiCues converts a whole domain cue list for export, preserving
// order (top row = first element; importers renumber Pos themselves).
func FromTimerpiCues(cues []timerpi.Cue) []Cue {
	out := make([]Cue, 0, len(cues))
	for _, tc := range cues {
		out = append(out, FromTimerpiCue(tc))
	}
	return out
}
