package venue

import (
	"context"
	"encoding/hex"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"timerpi/mdns"
	"timerpi/mesh"
	"timerpi/timerpi"
)

type quietReunionAnnouncer struct{}

func (quietReunionAnnouncer) Register(string, int, mdns.ServiceMeta) (func() bool, func()) {
	return func() bool { return false }, func() {}
}

func TestReturningOldPrimaryMirrorsTakeoverEditsBeforeNextTakeover(t *testing.T) {
	old, replacement := newSide(t, "box"), newSide(t, "box")
	ev, rooms, err := old.db.CreateEvent("Reunion", "test-password", []string{"Main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.db.CreateCue(rooms[0].ID, timerpi.Cue{Label: "Original", DurationMS: 60000}); err != nil {
		t.Fatal(err)
	}
	key, err := old.db.EventMeshKey(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := old.deps.ExportEvent(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := replacement.deps.ImportEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.db.SetEventMeshKey(copied.ID, key); err != nil {
		t.Fatal(err)
	}
	copiedRoom, ok := replacement.db.ResolveShowInEvent(copied.ID, rooms[0].Code)
	if !ok {
		t.Fatal("missing copied room")
	}
	if _, err := replacement.db.CreateCue(copiedRoom.ID, timerpi.Cue{Label: "Edit during takeover", DurationMS: 60000}); err != nil {
		t.Fatal(err)
	}
	a := &Agent{Store: old.db, Srv: old.deps, Port: old.port, Name: "original", Logf: t.Logf}
	a.save(Pairing{Event: ev.Code, EventName: ev.Name, MeshKey: key})
	now := time.Now()
	path := filepath.Join(t.TempDir(), "mesh.db")
	store, err := mesh.OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetInt64(mesh.KeyClaimedEpoch, 500); err != nil {
		t.Fatal(err)
	}
	store.Close()
	device, err := mesh.New(mesh.Options{Hostname: "original", Port: old.port, DBPath: path, Proto: 3, Auth: a.MeshAuth, Now: func() time.Time { return now }, Announcer: quietReunionAnnouncer{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(device.Close)
	a.Mesh = device
	host, _, err := net.SplitHostPort(strings.TrimPrefix(replacement.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	meta := mdns.ServiceMeta{Host: "replacement", Role: "primary", Epoch: 900, Proto: 3, Event: ev.Code, Boot: "replacement-boot"}
	decoded, _ := hex.DecodeString(key)
	meta.Sig = mesh.SignAnnouncement(decoded, meta)
	peer := mdns.Peer{Host: "replacement", Port: replacement.port, Addrs: []string{host}, TXT: mdns.DecodeTXT(meta.EncodeTXT()), LastSeen: now}
	if got := device.Evaluate(context.Background(), []mdns.Peer{peer}); got != mesh.StateMember {
		t.Fatalf("returning stale original preempted active replacement: %s", got)
	}
	a.keepEvent(context.Background(), a.Pairing())
	cues, err := old.db.ListCues(rooms[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 || cues[1].Label != "Edit during takeover" {
		t.Fatalf("returning original did not mirror latest event: %+v", cues)
	}
	now = now.Add(time.Minute)
	peer.LastSeen = now
	if got := device.Evaluate(context.Background(), []mdns.Peer{peer}); got != mesh.StateMember {
		t.Fatalf("healthy replacement lost authority: %s", got)
	}
	now = now.Add(mesh.DefaultTakeoverAfter + time.Second)
	if got := device.Evaluate(context.Background(), nil); got != mesh.StatePrimary {
		t.Fatalf("caught-up original did not take over after replacement loss: %s", got)
	}
	if id, _ := device.Status(); id.Epoch != 500 {
		t.Fatalf("persisted epoch changed: %d", id.Epoch)
	}
	cues, err = old.db.ListCues(rooms[0].ID)
	if err != nil || len(cues) != 2 || cues[1].Label != "Edit during takeover" {
		t.Fatalf("next takeover lost edit: %+v %v", cues, err)
	}
}
