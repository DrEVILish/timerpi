package main

import (
	"path/filepath"
	"testing"
	"time"

	"timerpi/timerpi"
)

func TestNativeDisplayClearsDeletedEvent(t *testing.T) {
	t.Setenv("TIMERPI_DISPLAY_SHOW", "")
	db, err := timerpi.Open(filepath.Join(t.TempDir(), "timerpi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ev, rooms, err := db.CreateEvent("Conference", "fixture-pass", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := rooms[0].ID
	if _, err := db.CreateCue(id, timerpi.Cue{Label: "Keynote", DurationMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	engines := timerpi.NewEngines(db)
	engine, err := engines.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Go(); err != nil {
		t.Fatal(err)
	}
	p := newDRMProvider(engines, db)
	now := time.Now()
	if got := p.View(now.UnixMilli()); got.Label != "Keynote" {
		t.Fatalf("native display before deletion: %+v", got)
	}
	if err := db.DeleteEvent(ev.ID); err != nil {
		t.Fatal(err)
	}
	engines.Drop(id)
	if got := p.View(now.Add(2 * time.Second).UnixMilli()); got.Label != "" || got.RemainingMS != 0 || got.WallClock == "" {
		t.Fatalf("native display after deletion: %+v", got)
	}
}
