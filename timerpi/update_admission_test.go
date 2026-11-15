package timerpi

import (
	"errors"
	"testing"
)

func TestUpdateReservationExcludesTimerCommands(t *testing.T) {
	db := openTestDB(t)
	sh := mustCreateShow(t, db, "Update admission")
	if _, err := db.CreateCue(sh.ID, sampleCue(0, "Welcome")); err != nil {
		t.Fatal(err)
	}
	engines := NewEngines(db)
	e, err := engines.Get(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := engines.ReserveUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Go(); !errors.Is(err, ErrUpdatePending) {
		t.Fatalf("GO admitted during install: %v", err)
	}
	if e.Runtime().Running {
		t.Fatal("reserved update started a timer")
	}
	cancel()
	if err := e.Go(); err != nil {
		t.Fatal(err)
	}
	if _, err := engines.ReserveUpdate(); err == nil {
		t.Fatal("update admitted while timer runs")
	}
	if err := e.Pause(); err != nil {
		t.Fatal(err)
	}
	cancel, err = engines.ReserveUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Resume(); !errors.Is(err, ErrUpdatePending) {
		t.Fatalf("resume admitted during install: %v", err)
	}
	cancel()
	if err := e.Resume(); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateReservationChecksUnloadedRuntime(t *testing.T) {
	db := openTestDB(t)
	sh := mustCreateShow(t, db, "Recovered running show")
	if err := db.SaveRuntime(Runtime{ShowID: sh.ID, Running: true}); err != nil {
		t.Fatal(err)
	}
	engines := NewEngines(db)
	if _, err := engines.ReserveUpdate(); err == nil {
		t.Fatal("update admitted over unloaded persisted timer")
	}
}

func TestUpdateWaitsForInFlightGO(t *testing.T) {
	db := openTestDB(t)
	sh := mustCreateShow(t, db, "Concurrent GO")
	if _, err := db.CreateCue(sh.ID, sampleCue(0, "Welcome")); err != nil {
		t.Fatal(err)
	}
	engines := NewEngines(db)
	e, err := engines.Get(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	save := e.deps.Save
	e.deps.Save = func(rt Runtime) error { close(entered); <-release; return save(rt) }
	goDone := make(chan error, 1)
	go func() { goDone <- e.Go() }()
	<-entered
	updateDone := make(chan error, 1)
	go func() {
		cancel, err := engines.ReserveUpdate()
		if cancel != nil {
			cancel()
		}
		updateDone <- err
	}()
	close(release)
	if err := <-goDone; err != nil {
		t.Fatal(err)
	}
	if err := <-updateDone; err == nil {
		t.Fatal("update overtook an in-flight GO")
	}
}
