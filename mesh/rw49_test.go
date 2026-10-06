package mesh

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

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

// slowSource blocks the harvest GET.
type slowSource struct{ calls atomic.Int32 }

func (s *slowSource) Snapshot(context.Context, string, int64) (json.RawMessage, error) {
	s.calls.Add(1)
	time.Sleep(400 * time.Millisecond)
	return json.RawMessage(`{}`), nil
}

// BUGLOG RW50: without ApplySnapshot (production) a takeover makes no GET
// at all; with it, the GET runs outside the lock, so Status() answers.
func TestTakeoverHarvestNeverHoldsTheLock(t *testing.T) {
	src := &slowSource{}
	kit := newKit(t, func(o *Options) { o.Source = src; o.ApplySnapshot = nil })
	kit.evaluate(t, []mdns.Peer{peer("pi-a", "primary", 1000, "192.168.1.20")})
	kit.waitFor(t, StatePrimary, nil)
	if src.calls.Load() != 0 {
		t.Fatalf("production takeover made %d harvest GETs", src.calls.Load())
	}

	src2 := &slowSource{}
	kit2 := newKit(t, func(o *Options) {
		o.Source = src2
		o.ApplySnapshot = func(json.RawMessage, string) error { return nil }
	})
	kit2.evaluate(t, []mdns.Peer{peer("pi-a", "primary", 1000, "192.168.1.20")})
	done := make(chan struct{})
	go func() {
		kit2.waitFor(t, StatePrimary, nil)
		close(done)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for src2.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	start := time.Now()
	kit2.dev.Status()
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("Status() waited %v behind the harvest GET", d)
	}
	<-done
}
