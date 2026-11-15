package timerpi

import "testing"

func TestShowMissingMessageKeepsCurrentMessage(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Stage")
	other := mustCreateShow(t, d, "Other stage")
	current, err := d.CreateMessage(show.ID, "WRAP UP", "")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := d.CreateMessage(other.ID, "Other room", "")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := d.CreateMessage(show.ID, "Deleted", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteMessage(show.ID, deleted.ID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		name string
		id   int64
	}{
		{"deleted", deleted.ID}, {"other room", foreign.ID}, {"unknown", 99999},
	} {
		t.Run(target.name, func(t *testing.T) {
			if err := d.ShowMessage(show.ID, current.ID, 123); err != nil {
				t.Fatal(err)
			}
			if err := d.ShowMessage(show.ID, target.id, 456); err != nil {
				t.Fatal(err)
			}
			messages, err := d.ListMessages(show.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(messages) != 1 || messages[0].ID != current.ID || messages[0].ShownAt != 123 {
				t.Fatalf("missing target cleared current stage message: %+v", messages)
			}
		})
	}
}
