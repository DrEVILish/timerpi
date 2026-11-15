package routes

import (
	"path/filepath"
	"testing"

	"timerpi/oscbridge"
	"timerpi/timerpi"
)

func TestMemberOSCRefusesReplicaWrites(t *testing.T) {
	db, err := timerpi.Open(filepath.Join(t.TempDir(), "member.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, rooms, err := db.CreateEvent("Replica", "test-password", []string{"Main"})
	if err != nil {
		t.Fatal(err)
	}
	room := rooms[0]
	if _, err := db.CreateCue(room.ID, timerpi.Cue{Label: "Original", DurationMS: 60000}); err != nil {
		t.Fatal(err)
	}
	writable := false
	d := &Deps{Store: db, Engines: timerpi.NewEngines(db), BoxWriteTarget: func() (string, bool) { return "http://primary.local:18080", writable }}
	for _, verb := range []string{"go", "blank"} {
		d.oscDispatch(oscbridge.Message{Address: "/timerpi/" + room.Code + "/" + verb})
	}
	eng, _ := d.Engines.Get(room.ID)
	if eng.Runtime().Running {
		t.Fatal("OSC ran member timer")
	}
	show, _ := db.GetShow(room.ID)
	if show.Blanked {
		t.Fatal("OSC blanked member copy")
	}
	writable = true
	d.oscDispatch(oscbridge.Message{Address: "/timerpi/" + room.Code + "/go"})
	if !eng.Runtime().Running {
		t.Fatal("OSC refused primary timer")
	}
}
