package timerpi

// Schedule is the computed running order for the cue list rows and the day
// bar (PLAN §2/§4). It is pure: computed from the cue sequence + durations +
// holds + breaks against a day-start instant. The rate multiplier does NOT
// change schedule math (PROTOCOL §Engine: a rate change re-anchors the
// countdown display only) — the parameter is carried for API parity and
// possible display-side scaling later.

// ScheduleRow is one computed row: scheduled start/end plus over/under when
// actual runtime is known.
type ScheduleRow struct {
	Pos        int64  `json:"pos"`
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Speaker    string `json:"speaker"`
	Location   string `json:"location,omitempty"` // a break's place (U14)
	Tags       string `json:"tags"`
	Color      string `json:"color"`
	DurationMS int64  `json:"durationMS"`
	HoldMS     int64  `json:"holdMS"` // deliberate changeover after this row
	Break      bool   `json:"break"`

	StartMS   int64 `json:"startMS"` // ms from day start (computed)
	EndMS     int64 `json:"endMS"`   // cue end (its hold is NOT included)
	StartTS   int64 `json:"startTS"` // absolute epoch ms
	EndTS     int64 `json:"endTS"`
	CumHoldMS int64 `json:"cumHoldMS"` // holds + breaks accumulated before start

	// Filled only by ComputeScheduleRuntime for the running cue:
	ActualEndTS int64 `json:"actualEndTS,omitempty"` // projected real end (epoch ms)
	DeltaMS     int64 `json:"deltaMS"`               // over(+)/under(−) vs scheduled end
}

// Schedule is the whole day's computed plan.
type Schedule struct {
	DayStartTS int64         `json:"dayStartTS"`
	Rate       float64       `json:"rate"`
	Rows       []ScheduleRow `json:"rows"`
	TotalMS    int64         `json:"totalMS"`  // whole day length incl. holds + breaks
	HoldsMS    int64         `json:"holdsMS"`  // sum of per-cue HoldMS
	BreaksMS   int64         `json:"breaksMS"` // sum of break-row durations
	EndTS      int64         `json:"endTS"`    // DayStartTS + TotalMS
}

// ComputeSchedule is the pure scheduler: each row starts where the previous
// one ended plus its HoldMS (break rows are ordinary cues whose duration is
// the gap). Day bar = 0:00 → TotalMS from DayStartTS.
func ComputeSchedule(cues []Cue, dayStartTS int64, rate float64) Schedule {
	s := Schedule{
		DayStartTS: dayStartTS,
		Rate:       rate,
		Rows:       make([]ScheduleRow, 0, len(cues)),
	}
	var cur int64 // ms from day start
	for _, c := range cues {
		brk := c.Kind == KindBreak
		row := ScheduleRow{
			Pos:        c.Pos,
			Label:      c.Label,
			Kind:       c.Kind,
			Speaker:    c.Speaker,
			Location:   c.Location,
			Tags:       c.Tags,
			Color:      c.Color,
			DurationMS: c.DurationMS,
			HoldMS:     c.HoldMS,
			Break:      brk,
			StartMS:    cur,
			EndMS:      cur + c.DurationMS,
			CumHoldMS:  s.HoldsMS + s.BreaksMS,
		}
		row.StartTS = dayStartTS + row.StartMS
		row.EndTS = dayStartTS + row.EndMS
		s.Rows = append(s.Rows, row)
		cur += c.DurationMS
		// Every row's deliberate changeover buffer follows it (break rows
		// too); break durations additionally count as break time.
		s.HoldsMS += c.HoldMS
		cur += c.HoldMS
		if brk {
			s.BreaksMS += c.DurationMS
		}
	}
	s.TotalMS = cur
	s.EndTS = dayStartTS + cur
	return s
}

// ComputeScheduleRuntime adds the "actual" side: for the running cue it
// projects the real end from the engine's remaining time and reports
// over/under vs the scheduled end. Completed cues have no stored actuals in
// v1 (DeltaMS 0); an OVERTIME cue reports DeltaMS = now − scheduled end and
// no projected end (it never ends by itself).
func ComputeScheduleRuntime(cues []Cue, rt Runtime, now int64) Schedule {
	s := ComputeSchedule(cues, rt.DayStartTS, rt.Rate)
	if rt.ActivePos == 0 {
		return s
	}
	var active *Cue
	for i := range cues {
		if cues[i].Pos == rt.ActivePos {
			active = &cues[i]
			break
		}
	}
	if active == nil {
		return s
	}
	remaining, _, _ := DisplayedRemaining(active, rt, now)
	for i := range s.Rows {
		if s.Rows[i].Pos != rt.ActivePos {
			continue
		}
		if remaining < 0 {
			s.Rows[i].DeltaMS = now - s.Rows[i].EndTS
			continue
		}
		rate := rt.Rate
		if rate <= 0 {
			rate = DefaultRate
		}
		actual := now + int64(float64(remaining)/rate) // wall ms left = scaled remaining / rate
		s.Rows[i].ActualEndTS = actual
		s.Rows[i].DeltaMS = actual - s.Rows[i].EndTS
	}
	return s
}
