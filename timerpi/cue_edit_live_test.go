package timerpi

import "testing"

// BUGLOG RC2: cue CRUD renumbers positions behind the engine. The
// playhead must follow the running cue by id, and deleting the running
// cue arms whatever took its slot instead of leaving a dangling pos.
func TestEngineFollowsActiveCueThroughEdits(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Live edit")
	for i := 1; i <= 4; i++ {
		if _, err := d.CreateCue(show.ID, sampleCue(0, cueName(i))); err != nil {
			t.Fatalf("CreateCue: %v", err)
		}
	}
	e, err := NewEngine(show.ID, d.EngineDeps(show.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Start(3); err != nil { // c3 running
		t.Fatal(err)
	}
	activeLabel := func() string {
		t.Helper()
		snap, err := e.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		c := cueAtPos(snap.Cues, snap.Runtime.ActivePos)
		if c == nil {
			return ""
		}
		return c.Label
	}

	// Delete a cue above: c3 moves to pos 2 and keeps running.
	if err := d.DeleteCue(show.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Notify(); err != nil {
		t.Fatal(err)
	}
	rt := e.Runtime()
	if got := activeLabel(); got != "c3" || rt.ActivePos != 2 || !rt.Running {
		t.Fatalf("after delete above: active=%q pos=%d running=%v, want c3 at 2 running", got, rt.ActivePos, rt.Running)
	}
	if rt.PrevPos != 1 || rt.NextPos != 3 {
		t.Fatalf("neighbours = %d/%d, want 1/3", rt.PrevPos, rt.NextPos)
	}

	// Move it to the top: still c3, still running.
	if err := d.MoveCue(show.ID, 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(nowMS()); err != nil { // the tick alone repairs it
		t.Fatal(err)
	}
	rt = e.Runtime()
	if got := activeLabel(); got != "c3" || rt.ActivePos != 1 || !rt.Running {
		t.Fatalf("after move: active=%q pos=%d running=%v", got, rt.ActivePos, rt.Running)
	}
	if saved, _, _ := d.LoadRuntime(show.ID); saved.ActivePos != 1 {
		t.Fatalf("repair not persisted: saved pos %d", saved.ActivePos)
	}

	// Delete the running cue: the one now in its slot is armed, not run.
	if err := d.DeleteCue(show.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.Notify(); err != nil {
		t.Fatal(err)
	}
	rt = e.Runtime()
	if got := activeLabel(); got != "c2" || rt.Running || rt.AnchorTS != 0 {
		t.Fatalf("after deleting running cue: active=%q running=%v anchor=%d, want c2 armed", got, rt.Running, rt.AnchorTS)
	}
	if err := e.Go(); err != nil { // GO runs the armed cue
		t.Fatalf("GO after delete: %v", err)
	}
	if got := activeLabel(); got != "c2" || !e.Runtime().Running {
		t.Fatalf("GO started %q", got)
	}
}

// STATUS U14 + BUGLOG RS35: a break's location is stored, survives
// duplicate/clone/replace with the session's day, and reaches schedules.
func TestCueLocationAndDayRoundTrip(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Loc")
	c, err := d.CreateCue(show.ID, Cue{Label: "Coffee", Kind: KindBreak, DurationMS: 900_000, Location: "  Great Hall  ", Day: 2})
	if err != nil {
		t.Fatal(err)
	}
	if c.Location != "Great Hall" || c.Day != 2 {
		t.Fatalf("stored: location %q day %d", c.Location, c.Day)
	}
	dup, err := d.DuplicateCue(show.ID, c.Pos)
	if err != nil || dup.Location != "Great Hall" || dup.Day != 2 {
		t.Fatalf("duplicate lost location/day: %+v %v", dup, err)
	}
	clone, err := d.CloneShow(show.ID, "Loc copy")
	if err != nil {
		t.Fatal(err)
	}
	cues, _ := d.ListCues(clone.ID)
	if len(cues) != 2 || cues[0].Location != "Great Hall" || cues[0].Day != 2 {
		t.Fatalf("clone lost location/day: %+v", cues)
	}
	s := ComputeSchedule(cues, 0, 1)
	if s.Rows[0].Location != "Great Hall" {
		t.Errorf("schedule row has no location: %+v", s.Rows[0])
	}
}
