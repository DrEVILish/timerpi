package mesh

import (
	"sync/atomic"
	"testing"

	"timerpi/mdns"
)

// lateCollider reports a name conflict only after the first browse.
type lateCollider struct {
	fakeAnnouncer
	conflict atomic.Bool
}

func (l *lateCollider) Register(name string, port int, meta mdns.ServiceMeta) (func() bool, func()) {
	_, stop := l.fakeAnnouncer.Register(name, port, meta)
	// Another box owns "pi-stage" once conflict is set.
	return func() bool { return name == "pi-stage" && l.conflict.Load() }, stop
}

// BUGLOG RW49: our own echo is recognised by its boot id, so a second box
// with the SAME hostname and port is seen (not dropped as an echo); and a
// name conflict that appears after Register still triggers the -2 rename.
func TestSameNameBoxSeenAndLateConflictRenames(t *testing.T) {
	ann := &lateCollider{}
	kit := newKit(t, func(o *Options) { o.Announcer = ann; o.Port = 80 })
	kit.evaluate(t, nil) // first announce, no conflict yet

	_, meta := ann.last()
	if meta.Boot == "" {
		t.Fatal("announce carries no boot id")
	}
	echo := peer("pi-stage", "idle", 0)
	echo.TXT = mdns.DecodeTXT(meta.EncodeTXT())
	twin := peer("pi-stage", "primary", 500, "10.0.0.9") // same host and port, other box
	twin.TXT["boot"] = "another-boot"
	kit.evaluate(t, []mdns.Peer{echo, twin})
	kit.dev.mu.Lock()
	n := len(kit.dev.peers)
	kit.dev.mu.Unlock()
	if n != 1 {
		t.Fatalf("peers after dropping our echo = %d, want the twin only", n)
	}

	ann.conflict.Store(true)
	kit.evaluate(t, []mdns.Peer{twin})
	if name, _ := ann.last(); name != "pi-stage-2" {
		t.Errorf("late conflict not resolved: announcing %q", name)
	}
}
