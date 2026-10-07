package importdocs

import (
	"fmt"

	"timerpi/timerpi"
)

// ToTimerpiCues normalizes and validates a whole imported document, in order.
// Normalize fills empty kind/timerKind/endAction and alert colours with the
// PROTOCOL defaults and clamps negatives; Validate then checks the enum fields
// and colours. A failed row is reported with its 1-based position (first
// error wins). Every cue is left with Pos 0: REPLACE flows (db.ReplaceCues)
// renumber 1..N themselves, and append flows (db.CreateCue with Pos 0) append
// to the end.
func ToTimerpiCues(cues []timerpi.Cue) ([]timerpi.Cue, error) {
	out := make([]timerpi.Cue, 0, len(cues))
	for i, c := range cues {
		label := c.Label
		c.Normalize()
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("importdocs: cue %d: importdocs: cue %q: %w", i+1, label, err)
		}
		out = append(out, c)
	}
	return out, nil
}
