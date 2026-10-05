package timerpi

import (
	"errors"
	"path/filepath"
	"testing"

	"database/sql"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "timerpi.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func mustCreateShow(t *testing.T, d *DB, title string) Show {
	t.Helper()
	s, err := d.CreateShow(title)
	if err != nil {
		t.Fatalf("CreateShow(%q): %v", title, err)
	}
	return s
}

func sampleCue(pos int64, label string) Cue {
	c := Cue{Label: label, DurationMS: 60_000, Kind: KindSession, Tags: "VT",
		Speaker: "Leslie", HoldMS: 5_000, TimerKind: TimerCountdown,
		Alert1MS: 30_000, Alert2MS: 10_000, EndAction: EndHold, AutoContinue: true,
		Notes: "n", Color: "#00ff00"}
	c.Pos = pos
	return c
}

// TestShowCRUD covers create/list/get/rename/delete.
func TestShowCRUD(t *testing.T) {
	d := openTestDB(t)

	s1 := mustCreateShow(t, d, "Pawnee Townhall")
	s2 := mustCreateShow(t, d, "Rally")
	if s1.ID == 0 || s2.ID == 0 || s1.ID == s2.ID {
		t.Fatalf("ids: %+v %+v", s1, s2)
	}

	list, err := d.ListShows()
	if err != nil {
		t.Fatalf("ListShows: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %d shows, want 2", len(list))
	}

	got, err := d.GetShow(s1.ID)
	if err != nil || got.Title != "Pawnee Townhall" {
		t.Fatalf("GetShow: %+v %v", got, err)
	}

	if err := d.RenameShow(s1.ID, "Renamed"); err != nil {
		t.Fatalf("RenameShow: %v", err)
	}
	if got, _ := d.GetShow(s1.ID); got.Title != "Renamed" {
		t.Fatalf("rename lost: %+v", got)
	}

	if err := d.DeleteShow(s2.ID); err != nil {
		t.Fatalf("DeleteShow: %v", err)
	}
	if _, err := d.GetShow(s2.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted show: %v, want ErrNoRows", err)
	}
}

// TestCueLifecycleAndOrdering: Pos must stay contiguous 1..N through
// create/insert/move/duplicate/delete/reorder — the engine's run order.
func TestCueLifecycleAndOrdering(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Ordering")

	for i := 1; i <= 5; i++ {
		if _, err := d.CreateCue(show.ID, sampleCue(0, cueName(i))); err != nil {
			t.Fatalf("CreateCue append %d: %v", i, err)
		}
	}
	assertOrder(t, d, show.ID, []string{"c1", "c2", "c3", "c4", "c5"})

	// Insert at position 2 shifts the rest.
	c := sampleCue(2, "ins")
	stored, err := d.CreateCue(show.ID, c)
	if err != nil || stored.Pos != 2 {
		t.Fatalf("insert-at: %+v %v", stored, err)
	}
	assertOrder(t, d, show.ID, []string{"c1", "ins", "c2", "c3", "c4", "c5"})

	// Move cue 6 ("c5") to the top.
	if err := d.MoveCue(show.ID, 6, 1); err != nil {
		t.Fatalf("MoveCue: %v", err)
	}
	assertOrder(t, d, show.ID, []string{"c5", "c1", "ins", "c2", "c3", "c4"})

	// Duplicate lands right after its source.
	if _, err := d.DuplicateCue(show.ID, 3); err != nil { // "ins" copy at pos 4
		t.Fatalf("DuplicateCue: %v", err)
	}
	assertOrder(t, d, show.ID, []string{"c5", "c1", "ins", "ins", "c2", "c3", "c4"})

	// Delete keeps 1..N contiguous.
	if err := d.DeleteCue(show.ID, 4); err != nil {
		t.Fatalf("DeleteCue: %v", err)
	}
	assertOrder(t, d, show.ID, []string{"c5", "c1", "ins", "c2", "c3", "c4"})

	// ReorderCues by stable IDs.
	cues, err := d.ListCues(show.ID)
	if err != nil {
		t.Fatalf("ListCues: %v", err)
	}
	ids := []int64{}
	for _, cu := range cues {
		ids = append(ids, cu.ID)
	}
	// Reverse the first three.
	ids[0], ids[2] = ids[2], ids[0]
	if err := d.ReorderCues(show.ID, ids); err != nil {
		t.Fatalf("ReorderCues: %v", err)
	}
	assertOrder(t, d, show.ID, []string{"ins", "c1", "c5", "c2", "c3", "c4"})

	// Update by ID keeps the (renumbered) position.
	upd, err := d.GetCue(show.ID, 3) // "c5" after the reorder
	if err != nil {
		t.Fatalf("GetCue for update: %v", err)
	}
	upd.Label = "edited"
	upd.DurationMS = 123_000
	saved, err := d.UpdateCue(show.ID, upd)
	if err != nil {
		t.Fatalf("UpdateCue: %v", err)
	}
	if saved.ID != upd.ID || saved.Pos != upd.Pos || saved.Label != "edited" || saved.DurationMS != 123_000 {
		t.Fatalf("UpdateCue result: %+v", saved)
	}
	assertOrder(t, d, show.ID, []string{"ins", "c1", "edited", "c2", "c3", "c4"})
}

// TestReplaceCues covers the import path (full cue replace).
func TestReplaceCues(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Import")

	for i := 1; i <= 3; i++ {
		if _, err := d.CreateCue(show.ID, sampleCue(0, cueName(i))); err != nil {
			t.Fatalf("CreateCue: %v", err)
		}
	}
	newCues := []Cue{sampleCue(0, "a"), sampleCue(0, "b"), sampleCue(0, "c"), sampleCue(0, "d")}
	if err := d.ReplaceCues(show.ID, newCues); err != nil {
		t.Fatalf("ReplaceCues: %v", err)
	}
	assertOrder(t, d, show.ID, []string{"a", "b", "c", "d"})
}

// TestMessagesCRUD covers create/show/clear/delete + list ordering.
func TestMessagesCRUD(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Msgs")

	m1, err := d.CreateMessage(show.ID, "PLEASE WRAP UP!", "#ff4444")
	if err != nil || m1.ShownAt != 0 {
		t.Fatalf("CreateMessage: %+v %v", m1, err)
	}
	m2, err := d.CreateMessage(show.ID, "CHANGE OVER", "")
	if err != nil {
		t.Fatalf("CreateMessage 2: %v", err)
	}
	if _, err := d.CreateMessage(show.ID, "   ", ""); err == nil {
		t.Fatalf("empty message accepted")
	}

	if err := d.ShowMessage(show.ID, m1.ID, 0); err != nil {
		t.Fatalf("ShowMessage: %v", err)
	}
	if err := d.ShowMessage(show.ID, m2.ID, 5_000); err != nil {
		t.Fatalf("ShowMessage: %v", err)
	}
	msgs, err := d.ListMessages(show.ID)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("ListMessages: %+v %v", msgs, err)
	}
	if msgs[0].ShownAt == 0 || msgs[1].ShownAt != 5_000 {
		t.Fatalf("shown stamps: %+v", msgs)
	}

	if err := d.ClearMessage(show.ID, m2.ID); err != nil {
		t.Fatalf("ClearMessage: %v", err)
	}
	msgs, _ = d.ListMessages(show.ID)
	if msgs[1].ShownAt != 0 {
		t.Fatalf("clear lost: %+v", msgs[1])
	}

	if err := d.DeleteMessage(show.ID, m1.ID); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	msgs, _ = d.ListMessages(show.ID)
	if len(msgs) != 1 {
		t.Fatalf("delete lost: %+v", msgs)
	}
}

// TestSettingsKV covers the settings table.
func TestSettingsKV(t *testing.T) {
	d := openTestDB(t)

	if v, err := d.GetSetting("hostname"); err != nil || v != "" {
		t.Fatalf("missing setting: %q %v", v, err)
	}
	if err := d.SetSetting("hostname", "timerpi-a"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := d.SetSetting("hostname", "timerpi-b"); err != nil { // upsert
		t.Fatalf("SetSetting 2: %v", err)
	}
	if v, _ := d.GetSetting("hostname"); v != "timerpi-b" {
		t.Fatalf("GetSetting: %q", v)
	}
	all, err := d.AllSettings()
	if err != nil || all["hostname"] != "timerpi-b" {
		t.Fatalf("AllSettings: %+v %v", all, err)
	}
}

// TestRuntimeRoundTrip covers load/save per show.
func TestRuntimeRoundTrip(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Runtime")

	rt, found, err := d.LoadRuntime(show.ID)
	if err != nil || found || rt.Rate != DefaultRate {
		t.Fatalf("fresh runtime: %+v found=%v err=%v", rt, found, err)
	}

	rt.Rate = 1.5
	rt.ActivePos = 3
	rt.PrevPos = 2
	rt.NextPos = 4
	rt.Running = true
	rt.Paused = true
	rt.EndAction = EndOvertime
	rt.AnchorTS = 1_234
	rt.PausedElapsedMS = 567
	rt.DayStartTS = 890
	if err := d.SaveRuntime(rt); err != nil {
		t.Fatalf("SaveRuntime: %v", err)
	}
	got, found, err := d.LoadRuntime(show.ID)
	if err != nil || !found {
		t.Fatalf("load after save: found=%v err=%v", found, err)
	}
	// SaveRuntime stamps updated_at (part of the snapshot's updatedAt max);
	// everything else must survive byte-for-byte.
	rt.UpdatedAt = got.UpdatedAt
	if rt.UpdatedAt == 0 {
		t.Fatalf("save did not stamp updated_at")
	}
	if got != rt {
		t.Fatalf("round trip: got %+v want %+v", got, rt)
	}

	// Runtime rows are per-show.
	other := mustCreateShow(t, d, "Other")
	if _, found, _ := d.LoadRuntime(other.ID); found {
		t.Fatalf("other show has runtime")
	}
}

// TestEngineDepsWiring: the full DB→engine loop, including persistence and
// snapshot building over real rows.
func TestEngineDepsWiring(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Wired")
	for i := 1; i <= 2; i++ {
		if _, err := d.CreateCue(show.ID, sampleCue(0, cueName(i))); err != nil {
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

	snap, err := e.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Show.Title != "Wired" || len(snap.Cues) != 2 || snap.Cues[0].Pos != 1 {
		t.Fatalf("snapshot content: %+v", snap)
	}
	if snap.UpdatedAt == 0 || snap.ServerTime == 0 {
		t.Fatalf("stamps: %+v", snap)
	}
	if !snap.Runtime.Running || snap.Runtime.Rate != DefaultRate {
		t.Fatalf("runtime view: %+v", snap.Runtime)
	}

	// Persistence: a brand-new engine for the same show resumes the state.
	e2, err := NewEngine(show.ID, d.EngineDeps(show.ID))
	if err != nil {
		t.Fatalf("re-open engine: %v", err)
	}
	if rt := e2.Runtime(); rt.ActivePos != 1 || !rt.Running {
		t.Fatalf("persisted runtime lost: %+v", rt)
	}

	// Messages flow into the snapshot (shown only).
	m, err := d.CreateMessage(show.ID, "WRAP", "#ff4444")
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	_ = d.ShowMessage(show.ID, m.ID, 0)
	snap, _ = e.Snapshot()
	if len(snap.Messages) != 1 || snap.Messages[0].Text != "WRAP" {
		t.Fatalf("snapshot messages: %+v", snap.Messages)
	}

	// Cascade delete wipes cues + runtime + messages.
	if err := d.DeleteShow(show.ID); err != nil {
		t.Fatalf("DeleteShow: %v", err)
	}
	if cues, _ := d.ListCues(show.ID); len(cues) != 0 {
		t.Fatalf("cues not cascaded")
	}
	reg.Drop(show.ID)
}

// TestCueStampBumps verifies cue mutations bump the show's stamp (updatedAt).
func TestCueStampBumps(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Stamp")
	base := d.UpdatedStamp(show.ID)

	_, err := d.CreateCue(show.ID, sampleCue(0, "x"))
	if err != nil {
		t.Fatalf("CreateCue: %v", err)
	}
	if d.UpdatedStamp(show.ID) < base {
		t.Fatalf("stamp did not advance: %d < %d", d.UpdatedStamp(show.ID), base)
	}
}

func assertOrder(t *testing.T, d *DB, showID int64, want []string) {
	t.Helper()
	cues, err := d.ListCues(showID)
	if err != nil {
		t.Fatalf("ListCues: %v", err)
	}
	if len(cues) != len(want) {
		t.Fatalf("cue count = %d, want %d (%+v)", len(cues), len(want), cues)
	}
	for i, c := range cues {
		if c.Pos != int64(i+1) {
			t.Fatalf("pos not contiguous at index %d: pos=%d", i, c.Pos)
		}
		if c.Label != want[i] {
			t.Fatalf("order: got %q at %d, want %q", c.Label, i, want[i])
		}
	}
}

func cueName(i int) string { return "c" + string(rune('0'+i)) }
