package timerpi

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// --- DB: boundary edges the lifecycle test never sends ----------------------

// MoveCue refuses a source beyond the last cue (from>N) — cueIDsOrdered is
// empty-safe, but the refusal is the operator-facing contract.
func TestMoveCueFromBeyondLast(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Beyond")
	for i := 1; i <= 5; i++ {
		if _, err := d.CreateCue(show.ID, sampleCue(0, fmt.Sprintf("cue%d", i))); err != nil {
			t.Fatalf("CreateCue: %v", err)
		}
	}
	err := d.MoveCue(show.ID, 99, 1)
	if err == nil || !strings.Contains(err.Error(), "no cue at position 99") {
		t.Fatalf("MoveCue(from=99): err=%v, want `no cue at position 99`", err)
	}
	// The show is untouched by the failed move.
	cues, _ := d.ListCues(show.ID)
	if len(cues) != 5 || cues[0].Label != "cue1" {
		t.Fatalf("move attempt poisoned the show: %+v", cues)
	}
}

// CreateCue with a request-position beyond the end clamps to APPEND (k is
// capped at len+1 in insertID); the returned cue carries the landed pos.
func TestCreateCuePosBeyondCountClampsAppend(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Clamped")
	for i := 1; i <= 3; i++ {
		if _, err := d.CreateCue(show.ID, sampleCue(0, fmt.Sprintf("cue%d", i))); err != nil {
			t.Fatalf("CreateCue: %v", err)
		}
	}
	c, err := d.CreateCue(show.ID, sampleCue(99, "late"))
	if err != nil {
		t.Fatalf("CreateCue(pos=99): %v", err)
	}
	if c.Pos != 4 || c.Label != "late" {
		t.Fatalf("pos=99 landed at %+v, want append at pos 4", c)
	}
	cues, _ := d.ListCues(show.ID)
	if len(cues) != 4 || cues[3].Label != "late" {
		t.Fatalf("order after clamp: %+v", cues)
	}
}

// DuplicateCue of the LAST cue: the copy appends at N+1 (insertID's empty
// right-hand tail), the source row is untouched.
func TestDuplicateCueLastCue(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Tail")
	for i := 1; i <= 3; i++ {
		if _, err := d.CreateCue(show.ID, sampleCue(0, fmt.Sprintf("cue%d", i))); err != nil {
			t.Fatalf("CreateCue: %v", err)
		}
	}
	copyCue, err := d.DuplicateCue(show.ID, 3)
	if err != nil {
		t.Fatalf("DuplicateCue(last): %v", err)
	}
	if copyCue.Pos != 4 || copyCue.Label != "cue3" {
		t.Fatalf("copy = %+v, want cue3 at pos 4", copyCue)
	}
	cues, _ := d.ListCues(show.ID)
	if len(cues) != 4 {
		t.Fatalf("cue count after dup: %d, want 4", len(cues))
	}
	if cues[len(cues)-1].ID == cues[2].ID {
		t.Fatal("duplicate kept the source row id")
	}
}

// migrate() rerunning on an already-migrated DB (column ADDs swallow the
// duplicate-column error) — reopen the same file-backed database.
func TestMigrateRerunTolerant(t *testing.T) {
	path := t.TempDir() + "/rerun.db"
	d1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	show := mustCreateShow(t, d1, "Persisted")
	if _, err := d1.CreateCue(show.ID, sampleCue(0, "kept")); err != nil {
		t.Fatalf("CreateCue: %v", err)
	}
	if err := d1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	d2, err := Open(path) // migrates AGAIN over the same schema
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	defer d2.Close()
	shows, err := d2.ListShows()
	if err != nil {
		t.Fatalf("Shows: %v", err)
	}
	found := false
	for _, sh := range shows {
		if sh.ID == show.ID && sh.Title == "Persisted" {
			found = true
		}
	}
	if !found {
		t.Fatalf("re-opened DB lost state: %+v", shows)
	}
	cues, _ := d2.ListCues(show.ID)
	if len(cues) != 1 || cues[0].Label != "kept" {
		t.Fatalf("re-opened DB cues: %+v", cues)
	}
}

// --- Engine edges -----------------------------------------------------------

// Crash recovery: a persisted RUNNING countdown already past zero must NOT
// replay the zero-crossing on the first tick (HOLD keeps it at pos 1; the
// autocontinue would have marched to pos 2).
func TestEngineRecoveryPastZeroHolds(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Recovered")
	for i := 1; i <= 2; i++ {
		if _, err := d.CreateCue(show.ID, sampleCue(0, fmt.Sprintf("cue%d", i))); err != nil {
			t.Fatalf("CreateCue: %v", err)
		}
	}
	reg := NewEngines(d)
	e, err := reg.Get(show.ID)
	if err != nil {
		t.Fatalf("Engines.Get: %v", err)
	}
	if err := e.Start(1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Simulate the crash: the cue has been running far past zero for a while.
	rt, found, err := d.LoadRuntime(show.ID)
	if err != nil || !found {
		t.Fatalf("load runtime: found=%v err=%v", found, err)
	}
	rt.AnchorTS = time.Now().UnixMilli() - 120_000 // 2 min into a 60 s cue
	rt.DayStartTS = rt.AnchorTS
	if err := d.SaveRuntime(rt); err != nil {
		t.Fatalf("SaveRuntime: %v", err)
	}

	e2, err := NewEngine(show.ID, d.EngineDeps(show.ID))
	if err != nil {
		t.Fatalf("re-open engine: %v", err)
	}
	if _, err := e2.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if e2.Runtime().ActivePos != 1 {
		t.Fatalf("crossing replayed: active=%d (want 1 held-at-zero)", e2.Runtime().ActivePos)
	}
	// BUGLOG RC7: the HOLD cue is frozen at zero, not left "running" in
	// overtime, and the first tick persists that without marching on.
	if err := e2.Tick(time.Now().UnixMilli()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got := e2.Runtime()
	if got.Running || got.ActivePos != 1 || got.PausedElapsedMS != 60_000 {
		t.Fatalf("not held at zero after restart: %+v", got)
	}
	if snap, _ := e2.Snapshot(); snap.Runtime.RemainingMS != 0 || snap.Runtime.Overtime {
		t.Fatalf("timer after restart: remaining=%d overtime=%v, want 0 held", snap.Runtime.RemainingMS, snap.Runtime.Overtime)
	}
	if saved, _, _ := d.LoadRuntime(show.ID); saved.Running {
		t.Fatal("held state not persisted by the first tick")
	}
}

// Start{} with no position and an already-armed cue restarts THAT cue
// (not the day's first one).
func TestStartWithoutPosRestartsArmedCue(t *testing.T) {
	t0 := int64(1_700_000_000_000)
	e, clk := newTestEngine(t, t0, nil)
	if err := e.ApplyCmd("next", nil); err != nil { // idle -> arms cue1
		t.Fatalf("next1: %v", err)
	}
	if err := e.ApplyCmd("next", nil); err != nil { // cue1 -> arms cue2
		t.Fatalf("next2: %v", err)
	}
	if rt := e.Runtime(); rt.ActivePos != 2 || rt.Running {
		t.Fatalf("next: not armed at 2: %+v", rt)
	}
	clk.ms += 30_000 // some wall time passes before the operator starts it
	if err := e.ApplyCmd("start", nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	rt := e.Runtime()
	if rt.ActivePos != 2 || !rt.Running {
		t.Fatalf("start{} = %+v, want cue2 started", rt)
	}
	// The re-anchor is fresh: cue2 (Keynote, 1200 s) shows the full duration again.
	rt2 := e.Runtime()
	cues2, _ := e.deps.Cues()
	var c *Cue
	for i := range cues2 {
		if cues2[i].Pos == rt2.ActivePos {
			x := cues2[i]
			c = &x
			break
		}
	}
	rem, _, _ := DisplayedRemaining(c, rt2, clk.Now())
	if rem != 1200000 {
		t.Fatalf("re-anchor remaining = %d, want full 1200000 (cue2 Keynote)", rem)
	}
}

// Schedule projection clamps a broken rate (≤0) before dividing by it.
func TestScheduleRateZeroClamped(t *testing.T) {
	day := int64(1_700_000_000_000)
	cues := testCues()
	rt := Runtime{ActivePos: 1, Running: true, AnchorTS: day, Rate: 0, DayStartTS: day}
	s := ComputeScheduleRuntime(cues, rt, day+60_000)
	row := s.Rows[0]
	if row.ActualEndTS != day+600_000 || row.DeltaMS != 0 {
		t.Fatalf("rate=0 projection: start=%d end=%d delta=%d, want on-time 600s", row.ActualEndTS, row.EndTS, row.DeltaMS)
	}
}

// --- Merge edges ------------------------------------------------------------

// A tombstone with Deleted=false is not a delete: the server row survives.
func TestMergeTombstoneDeletedFalseSkipped(t *testing.T) {
	server := []Cue{mergeCue(1, 1, "stay", 300), mergeCue(2, 2, "gone?", 100)}
	incoming := []Cue{}
	merged, _ := MergeCues(server, incoming, []CueTombstone{{ID: 2, Deleted: false, UpdatedAt: 999}})
	if !equalStr(mergeLabels(merged), []string{"stay", "gone?"}) {
		t.Fatalf("Deleted=false tombstone removed a row: %+v", merged)
	}
}

// Two incoming rows for the SAME id resolve last-writer-wins (>= stamp).
func TestMergeDuplicateIncomingRowsLWW(t *testing.T) {
	server := []Cue{}
	incoming := []Cue{
		mergeCue(7, 1, "earlier", 100),
		mergeCue(7, 1, "later", 200),
	}
	merged, _ := MergeCues(server, incoming, nil)
	if !equalStr(mergeLabels(merged), []string{"later"}) {
		t.Fatalf("duplicate id: %+v", merged)
	}
}

// An offline re-created row (positive id the server never had) collapses
// when a same-id tombstone is NEWER (re-created then deleted while dark).
func TestMergeUnknownPositiveIDCollapsedByNewerTombstone(t *testing.T) {
	server := []Cue{mergeCue(1, 1, "stay", 300)}
	incoming := []Cue{mergeCue(9000, 2, "recreated", 10)}
	merged, _ := MergeCues(server, incoming, []CueTombstone{{ID: 9000, Deleted: true, UpdatedAt: 20}})
	if !equalStr(mergeLabels(merged), []string{"stay"}) {
		t.Fatalf("unknown-id add resurrected past its newer tombstone: %+v", merged)
	}
}

// --- Identity / rate edges --------------------------------------------------

func TestParseNumericIDGuards(t *testing.T) {
	t.Run("refuses overflow-length digits", func(t *testing.T) {
		if id, ok := ParseNumericID("123456789012345678901"); ok || id != 0 {
			t.Fatalf("21-digit id accepted: %d %v", id, ok)
		}
	})
	t.Run("refuses zero", func(t *testing.T) {
		if id, ok := ParseNumericID("0"); ok || id != 0 {
			t.Fatalf("all-zero accepted: %d %v", id, ok)
		}
	})
	t.Run("accepts a sane numeric id", func(t *testing.T) {
		if id, ok := ParseNumericID("424242"); !ok || id != 424242 {
			t.Fatalf("sane id refused: %d %v", id, ok)
		}
	})
}

func TestClampRateBounds(t *testing.T) {
	if got := ClampRate(0.1); got != 0.5 {
		t.Errorf("ClampRate(0.1) = %v, want 0.5", got)
	}
	if got := ClampRate(2.01); got != 2.0 {
		t.Errorf("ClampRate(2.01) = %v, want 2.0", got)
	}
	if got := ClampRate(1.5); got != 1.5 {
		t.Errorf("ClampRate(1.5) = %v, want untouched", got)
	}
}
