package timerpi

import (
	"testing"
)

// MoveCue "slot you become" contract — the drag client compensates its
// still-present row against exactly this: moving DOWN lands AT `to`,
// past-the-end clamps to the tail, same-slot is a no-op.
func TestMoveCueDownContract(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Down")
	for i := 1; i <= 5; i++ {
		if _, err := d.CreateCue(show.ID, sampleCue(0, cueName(i))); err != nil {
			t.Fatalf("CreateCue: %v", err)
		}
	}
	if err := d.MoveCue(show.ID, 1, 3); err != nil {
		t.Fatalf("MoveCue down: %v", err)
	}
	assertOrder(t, d, show.ID, []string{"c2", "c3", "c1", "c4", "c5"})

	if err := d.MoveCue(show.ID, 2, 99); err != nil {
		t.Fatalf("MoveCue clamp: %v", err)
	}
	assertOrder(t, d, show.ID, []string{"c2", "c1", "c4", "c5", "c3"})

	if err := d.MoveCue(show.ID, 3, 3); err != nil {
		t.Fatalf("MoveCue same-slot: %v", err)
	}
	assertOrder(t, d, show.ID, []string{"c2", "c1", "c4", "c5", "c3"})
}
