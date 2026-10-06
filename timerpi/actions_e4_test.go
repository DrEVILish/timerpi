package timerpi

import (
	"fmt"
	"testing"
)

// E4 log tail: newest-first, capped per show (oldest pruned on insert),
// detail truncated, empty log reads back clean.
func TestActionLogTailAndCap(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Logged")

	if acts, err := d.ListActions(show.ID, 10); err != nil || len(acts) != 0 {
		t.Fatalf("empty log: %+v %v", acts, err)
	}
	if id, _ := d.LastActionID(show.ID); id != 0 {
		t.Fatal("empty log cursor != 0")
	}

	d.LogAction(show.ID, "controls:peer-a", "go", "")
	d.LogAction(show.ID, "api", "clone", "Friday II")
	acts, err := d.ListActions(show.ID, 10)
	if err != nil || len(acts) != 2 {
		t.Fatalf("log: %+v %v", acts, err)
	}
	if acts[0].Action != "clone" || acts[0].Actor != "api" {
		t.Errorf("newest-first violated: %+v", acts)
	}
	if id, _ := d.LastActionID(show.ID); id != acts[0].ID {
		t.Error("cursor != newest id")
	}

	// Flood past the cap: only the newest MaxActionsPerShow survive.
	for i := 0; i < MaxActionsPerShow+5; i++ {
		d.LogAction(show.ID, "api", "tick", fmt.Sprintf("n%d", i))
	}
	acts, err = d.ListActions(show.ID, MaxActionsPerShow+50)
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(acts) != MaxActionsPerShow {
		t.Fatalf("capped log = %d rows, want %d", len(acts), MaxActionsPerShow)
	}
	if acts[0].Detail != "n204" || acts[len(acts)-1].Detail != "n5" {
		t.Errorf("cap kept the wrong window: first=%q last=%q", acts[0].Detail, acts[len(acts)-1].Detail)
	}

	// Limit clamping: absurd limits come back bounded.
	if acts, _ := d.ListActions(show.ID, 99999); len(acts) != MaxActionsPerShow {
		t.Errorf("huge limit = %d rows", len(acts))
	}
	if acts, _ := d.ListActions(show.ID, 0); len(acts) != 50 {
		t.Errorf("zero limit = %d rows, want default 50", len(acts))
	}
}
