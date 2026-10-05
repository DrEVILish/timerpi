// drm_provider.go — feeds the HDMI renderer (drm/) from the cue engine.
//
// TIMERPI_DISPLAY controls the backend (see drm.OpenDisplay; default off).
// TIMERPI_DISPLAY_SHOW optionally pins a show code; otherwise the provider
// follows the first running show, falling back to the first show by ID.
// The full snapshot refreshes at 1 Hz; per-tick remaining math uses the
// pure timerpi.DisplayedRemaining formula so digits stay smooth at 50 fps.
package main

import (
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"timerpi/drm"
	"timerpi/timerpi"
)

type drmProvider struct {
	engines *timerpi.Engines
	db      *timerpi.DB

	mu       sync.Mutex
	snap     timerpi.Snapshot
	snapAt   time.Time
	eng      *timerpi.Engine
	showCode string
}

func newDRMProvider(engines *timerpi.Engines, db *timerpi.DB) *drmProvider {
	return &drmProvider{
		engines:  engines,
		db:       db,
		showCode: strings.ToUpper(strings.TrimSpace(os.Getenv("TIMERPI_DISPLAY_SHOW"))),
	}
}

func (p *drmProvider) pickEngine() *timerpi.Engine {
	// Pinned show code wins.
	if p.showCode != "" {
		if s, err := p.db.GetShowByCode(p.showCode); err == nil {
			if eng, err := p.engines.Get(s.ID); err == nil {
				return eng
			}
		}
	}
	// First running show, else lowest show ID.
	shows, err := p.db.ListShows()
	if err != nil || len(shows) == 0 {
		return nil
	}
	for _, s := range shows {
		eng, err := p.engines.Get(s.ID)
		if err != nil {
			continue
		}
		if rt := eng.Runtime(); rt.Running {
			return eng
		}
	}
	eng, err := p.engines.Get(shows[0].ID)
	if err != nil {
		return nil
	}
	return eng
}

func (p *drmProvider) refresh(now time.Time) {
	eng := p.pickEngine()
	if eng == nil {
		return
	}
	snap, err := eng.Snapshot()
	if err != nil {
		return
	}
	p.mu.Lock()
	p.snap = snap
	p.snapAt = now
	p.eng = eng
	p.mu.Unlock()
}

// View implements drm.Provider.
func (p *drmProvider) View(nowMS int64) drm.View {
	now := time.UnixMilli(nowMS)
	p.mu.Lock()
	stale := now.Sub(p.snapAt) > time.Second || p.eng == nil
	p.mu.Unlock()
	if stale {
		p.refresh(now)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	empty := drm.View{WallClock: now.Format("15:04:05")}
	if p.eng == nil {
		return empty
	}
	snap := p.snap
	rt := p.eng.Runtime()

	var active *timerpi.Cue
	for i := range snap.Cues {
		if snap.Cues[i].Pos == rt.ActivePos {
			active = &snap.Cues[i]
			break
		}
	}
	var next *timerpi.Cue
	for i := range snap.Cues {
		if snap.Cues[i].Pos == snap.Runtime.NextPos {
			next = &snap.Cues[i]
			break
		}
	}
	v := drm.View{
		Title:     snap.Show.Title,
		WallClock: now.Format("15:04:05"),
		Rate:      rt.Rate,
	}
	if active != nil {
		rem, overtime, alert := timerpi.DisplayedRemaining(active, rt, nowMS)
		v.Label = active.Label
		v.Speaker = active.Speaker
		v.RemainingMS = rem
		v.Overtime = overtime
		v.AlertState = alert
		v.AlertColor1 = timerpi.AlertColor(active, 1)
		v.AlertColor2 = timerpi.AlertColor(active, 2)
		if snap.Runtime.Blank {
			v.Blank = true
		}
		if snap.Show.Blanked {
			v.Blank = true // E3: operator blackout blanks the HDMI too
		}
		if active.DurationMS > 0 {
			done := active.DurationMS - rem
			if done < 0 {
				done = 0
			}
			if done > active.DurationMS {
				done = active.DurationMS
			}
			v.Progress = float64(done) / float64(active.DurationMS)
		}
	}
	if next != nil {
		v.Next = next.Label
		v.NextSpeaker = next.Speaker
	}
	for _, m := range snap.Messages {
		if m.ShownAt > 0 {
			v.Message = m.Text
			v.MessageColor = m.Color
			break
		}
	}
	return v
}

// startDRMClock boots the HDMI renderer when TIMERPI_DISPLAY selects a
// real backend. "off" (or an absent display) → nil clock, server runs
// headless as usual. The caller runs clock.Run(ctx) and closes the backend.
func startDRMClock(engines *timerpi.Engines, db *timerpi.DB) (drm.Selected, *drm.Clock) {
	sel, err := drm.OpenDisplay()
	if err != nil {
		log.Printf("timerpi: display off: %v", err)
		return drm.Selected{}, nil
	}
	if sel.Name == "off" {
		return sel, nil
	}
	prov := newDRMProvider(engines, db)
	clock := drm.NewClock(sel.Backend, prov)
	w, h := sel.Backend.Size()
	log.Printf("timerpi: display on (%s %dx%d)", sel.Name, w, h)
	return sel, clock
}
