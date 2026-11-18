package routes

import (
	"errors"
	"testing"

	"timerpi/timerpi"
)

func TestUplinkCopyStaysWithinAuthenticatedEvent(t *testing.T) {
	db, err := timerpi.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, _, err := db.CreateEvent("Main conference", "fixture-pass", nil)
	if err != nil {
		t.Fatal(err)
	}
	first.MeshKey, err = db.EventMeshKey(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ClaimVenueEvent(first); err != nil {
		t.Fatal(err)
	}
	other, _, err := db.CreateEvent("Other conference", "fixture-pass", nil)
	if err != nil {
		t.Fatal(err)
	}
	deps := &Deps{Store: db}
	b := eventBundle{Event: eventBundleEvent{Code: other.Code, Name: "Replacement"}}
	for _, final := range []bool{false, true} {
		if _, err := deps.applyEventCopy(b, eventCopyScope{event: first, final: final}); !errors.Is(err, errEventCopyOwnership) {
			t.Fatalf("copy for a different event (final=%t): %v", final, err)
		}
	}
	got, err := db.GetEvent(other.ID)
	if err != nil || got.Name != other.Name {
		t.Fatalf("different event's data changed: %v", err)
	}
}
