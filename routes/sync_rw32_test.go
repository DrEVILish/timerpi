package routes_test

import (
	"testing"
	"time"

	"timerpi/timerpi"
)

// BUGLOG RW32: a pushed runtime is checked against the stored cues (an
// unknown activePos goes idle, the rate is clamped to ×0.5–×2.0), messages
// are replaced as one list, and the old engine is retired so it can't save
// its stale runtime over the sync.
func TestSyncRuntimeSanitizedAndOldEngineRetired(t *testing.T) {
	ts := newAPITest(t)
	if _, err := ts.db.CreateCue(ts.showID, timerpi.Cue{Label: "A", DurationMS: 60_000}); err != nil {
		t.Fatal(err)
	}
	old, err := ts.engines.Get(ts.showID)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Start(1); err != nil {
		t.Fatal(err)
	}
	stamp := ts.db.UpdatedStamp(ts.showID) + 1
	code, env := ts.syncPost(t, map[string]any{
		"show":      map[string]any{"title": "Synced"},
		"updatedAt": stamp,
		"runtime":   map[string]any{"activePos": 99, "running": true, "anchorTS": time.Now().UnixMilli(), "rate": 1e9},
		"messages":  []map[string]any{{"text": "Wrap up", "color": "", "shownAt": 5}, {"text": "", "color": ""}},
	})
	if code != 200 || env["ok"] != true {
		t.Fatalf("sync: %d %v", code, env)
	}
	rt, _, err := ts.db.LoadRuntime(ts.showID)
	if err != nil {
		t.Fatal(err)
	}
	if rt.ActivePos != 0 || rt.Running || rt.Rate != 2.0 {
		t.Errorf("runtime stored as %+v, want idle at rate 2", rt)
	}
	msgs, _ := ts.db.ListMessages(ts.showID)
	if len(msgs) != 1 || msgs[0].Text != "Wrap up" || msgs[0].ShownAt != 5 {
		t.Errorf("messages = %+v", msgs)
	}
	// The old engine was retired: ticking it can no longer write.
	_ = old.Tick(time.Now().UnixMilli() + 120_000)
	if rt2, _, _ := ts.db.LoadRuntime(ts.showID); rt2.ActivePos != 0 {
		t.Errorf("the replaced engine saved over the sync: %+v", rt2)
	}
	if err := old.Pause(); err == nil {
		t.Error("a retired engine still accepted a command")
	}
}
