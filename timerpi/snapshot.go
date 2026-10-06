package timerpi

import (
	"sort"
)

// Snapshot is the wire state shared over WS (`state` frame), REST
// (GET /api/shows/:id) and the engine pubsub. JSON tags follow PROTOCOL §
// Snapshot; extra additive fields (prevPos, alertState, …) are documented in
// NOTES-timerpi.md.
type Snapshot struct {
	UpdatedAt  int64       `json:"updatedAt"`  // max row stamp: newer wins (mesh rule)
	ServerTime int64       `json:"serverTime"` // clients re-anchor their clock offset on receipt
	Show       Show        `json:"show"`
	Runtime    RuntimeView `json:"runtime"`
	Cues       []Cue       `json:"cues"`                // ordered by Pos
	Messages   []Message   `json:"messages"`            // shown only (ShownAt > 0)
	Poll       *PollView   `json:"poll,omitempty"`      // interaction on the audience target
	Presenter  *PollView   `json:"presenter,omitempty"` // interaction on the presenter target
}

// RuntimeView is the runtime as seen by clients: the stored Runtime fields
// plus the derived display state (alert, overtime, blank) computed by
// DisplayedRemaining at the snapshot instant. Digits are never server-ticked;
// clients recompute from anchorTS/rate/pausedElapsedMS. End states (REVIEW-3
// #2): a HOLD/BLANK-exhausted cue records Running=false, Paused stays false,
// PausedElapsedMS=DurationMS — HELD on the client is
// `!running && pausedElapsedMS >= durationMS` (BLANK additionally ships
// Blank=true with RemainingMS pinned 0).
type RuntimeView struct {
	Running         bool    `json:"running"`
	Paused          bool    `json:"paused"`
	ActivePos       int64   `json:"activePos"`
	PrevPos         int64   `json:"prevPos"`
	NextPos         int64   `json:"nextPos"`
	EndAction       string  `json:"endAction"`
	AnchorTS        int64   `json:"anchorTS"`
	PausedElapsedMS int64   `json:"pausedElapsedMS"`
	Rate            float64 `json:"rate"`
	DayStartTS      int64   `json:"dayStartTS"`

	// Derived, snapshot-instant only (not stored):
	AlertState  int    `json:"alertState"`  // 0/1/2
	AlertColor  string `json:"alertColor"`  // colour for AlertState
	Overtime    bool   `json:"overtime"`    // counting up past zero
	Blank       bool   `json:"blank"`       // BLANK action after zero crossing
	RemainingMS int64  `json:"remainingMS"` // convenience; digits use anchor math
}

// BuildSnapshot is the pure snapshot builder (also used by tests).
func BuildSnapshot(show Show, cues []Cue, msgs []Message, rt Runtime, updatedAt, serverTime int64) Snapshot {
	sorted := make([]Cue, len(cues))
	copy(sorted, cues)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Pos < sorted[j].Pos })

	shown := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.ShownAt > 0 {
			shown = append(shown, m)
		}
	}
	sort.Slice(shown, func(i, j int) bool { return shown[i].ShownAt < shown[j].ShownAt })

	var active *Cue
	for i := range sorted {
		if sorted[i].Pos == rt.ActivePos {
			active = &sorted[i]
			break
		}
	}
	remaining, overtime, alert := DisplayedRemaining(active, rt, serverTime)

	blank := false
	if active != nil && !rt.Running && remaining <= 0 && active.EndAction == EndBlank {
		// Frozen after a BLANK crossing: hide the countdown client-side.
		blank = true
		remaining = 0
	}
	if active == nil || active.TimerKind == TimerClock {
		remaining, overtime, alert = 0, false, 0
	}

	return Snapshot{
		UpdatedAt:  updatedAt,
		ServerTime: serverTime,
		Show:       show,
		Runtime: RuntimeView{
			Running:         rt.Running,
			Paused:          rt.Paused,
			ActivePos:       rt.ActivePos,
			PrevPos:         rt.PrevPos,
			NextPos:         rt.NextPos,
			EndAction:       rt.EndAction,
			AnchorTS:        rt.AnchorTS,
			PausedElapsedMS: rt.PausedElapsedMS,
			Rate:            rt.Rate,
			DayStartTS:      rt.DayStartTS,
			AlertState:      alert,
			AlertColor:      AlertColor(active, alert),
			Overtime:        overtime,
			Blank:           blank,
			RemainingMS:     remaining,
		},
		Cues:     sorted,
		Messages: shown,
	}
}

// DisplayedRemaining is the canonical countdown formula, exported so tests
// and the browser client can verify parity. The client JS re-implements it
// (public/src/engine.js, `elapsedMS`/`remainingMS`/`alertState`); the rules
// below are the contract BOTH engines mirror (REVIEW-3 D4 — this comment
// is the server-side definition of record):
//
//	scaledElapsed = running && !paused ? (now - anchorTS) * rate : 0
//	elapsed       = scaledElapsed + pausedElapsedMS
//	remaining     = durationMS - elapsed          (COUNTDOWN)
//	remaining     = elapsed                       (COUNTSTOP — counts UP
//	                                                from 0, never alerts)
//	CLOCK         → 0 (client renders the wall clock itself)
//
// Zero-crossing end states — the RECORDED RUNTIME the snapshot expresses
// (REVIEW-3 #2; the offline engine.js zero-crossing path mirrors this):
//
//   - HOLD: Running=false, Paused stays FALSE, PausedElapsedMS=DurationMS.
//     A held cue is therefore `!running && pausedElapsedMS >= durationMS`
//     (the client's HELD predicate) — NOT the `paused` flag, which stays
//     reserved for an operator pause.
//   - BLANK: the same recorded state as HOLD, plus BuildSnapshot pins
//     RuntimeView.Blank=true and RemainingMS=0 (the countdown's numbers
//     are hidden client-side; the scheduled end carries the meaning).
//   - OVERTIME: stays Running; remaining goes negative and keeps counting
//     up; `overtime=true` only while remaining < 0.
//
// Alert state (COUNTDOWN only): 0 while the cue is idle/armed (elapsed 0
// with no crossing); rises through Alert1/Alert2 as remaining passes each
// configured threshold and SATURATES at the deepest configured state once
// remaining <= 0. That saturation is UNCHANGED when the cue is HELD or
// BLANK — a held cue keeps shipping its deepest alertState/alertColor so
// display surfaces stay in their end-state color while frozen at 00:00
// (the client mirrors this by NOT gating alert state on `running`; only
// COUNTSTOP and CLOCK are permanently alert-free).
func DisplayedRemaining(c *Cue, rt Runtime, now int64) (remainingMS int64, overtime bool, alertState int) {
	if c == nil {
		return 0, false, 0
	}
	rate := rt.Rate
	if rate <= 0 {
		rate = DefaultRate // never divide/multiply by a broken rate
	}
	elapsed := rt.PausedElapsedMS
	if rt.Running && !rt.Paused && rt.AnchorTS > 0 {
		elapsed += int64(float64(now-rt.AnchorTS) * rate)
	}
	switch c.TimerKind {
	case TimerClock:
		return 0, false, 0
	case TimerCountStop:
		// Stopwatch: "remaining" is elapsed counting up; no alerts.
		return elapsed, false, 0
	default: // TimerCountdown
		rem := c.DurationMS - elapsed
		st := 0
		if c.Alert1MS > 0 && rem <= c.Alert1MS {
			st = 1
		}
		if c.Alert2MS > 0 && rem <= c.Alert2MS {
			st = 2
		}
		// Saturate at the deepest configured state once the timer is spent.
		if rem <= 0 {
			if c.Alert2MS > 0 {
				st = 2
			} else if c.Alert1MS > 0 {
				st = 1
			}
		}
		return rem, rem < 0, st
	}
}

// AlertColor maps an alert state to its display colour; "" = no colour.
func AlertColor(c *Cue, state int) string {
	if c == nil {
		return ""
	}
	switch state {
	case Alert2:
		if c.AlertColor2 != "" {
			return c.AlertColor2
		}
		return DefaultAlertColor2
	case Alert1:
		if c.AlertColor1 != "" {
			return c.AlertColor1
		}
		return DefaultAlertColor1
	}
	return ""
}

// Public is the snapshot for an untrusted screen: a browser that opened a
// room's display link without a screen key or an operator session (BUGLOG
// RW9). It keeps what a stage timer shows anyone in the room (title,
// running order labels, speakers, times, runtime, the audience item) and
// drops operator content: show and cue notes, cue tags, stage messages and
// the Presenter-only item.
func (s Snapshot) Public() Snapshot {
	out := s
	out.Show.Notes = ""
	out.Messages = []Message{}
	out.Presenter = nil
	out.Cues = make([]Cue, len(s.Cues))
	for i, c := range s.Cues {
		c.Notes, c.Tags = "", ""
		out.Cues[i] = c
	}
	return out
}
