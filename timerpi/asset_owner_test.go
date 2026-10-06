package timerpi

import (
	"path/filepath"
	"testing"
)

// BUGLOG RW8: on upgrade, each event's map image becomes that event's;
// other old images stay unowned (legacy, box-wide).
func TestMigrateAssetOwners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timerpi.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ev, _, err := d.CreateEvent("Owner", "pw-owner", []string{"R"})
	if err != nil {
		t.Fatal(err)
	}
	mapA, _ := d.CreateAsset(0, "map.png", "image/png", []byte{1})
	other, _ := d.CreateAsset(0, "logo.png", "image/png", []byte{2})
	if err := d.SetEventMap(ev.ID, mapA.ID); err != nil {
		t.Fatal(err)
	}
	d.Close()

	d, err = Open(path) // migrations run again
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if a, _ := d.GetAsset(mapA.ID); a.EventID != ev.ID {
		t.Errorf("event map owner = %d, want %d", a.EventID, ev.ID)
	}
	if a, _ := d.GetAsset(other.ID); a.EventID != 0 {
		t.Errorf("unrelated image owner = %d, want 0 (legacy)", a.EventID)
	}
}
