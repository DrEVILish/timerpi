package drm

import (
	"sync/atomic"
	"testing"
	"time"
)

// slowBackend notes a Present that runs after Close (the crash RW47 is
// about: writing into an unmapped framebuffer).
type slowBackend struct {
	closed, afterClose, presents atomic.Int32
}

func (b *slowBackend) Open() error      { return nil }
func (b *slowBackend) Size() (int, int) { return LayoutW, LayoutH }
func (b *slowBackend) Close() error     { b.closed.Store(1); return nil }
func (b *slowBackend) Present(*Image, []Rect) error {
	b.presents.Add(1)
	time.Sleep(30 * time.Millisecond) // a frame in flight
	if b.closed.Load() == 1 {
		b.afterClose.Add(1)
	}
	return nil
}

// BUGLOG RW47: stop cancels, waits for the frame in flight, then closes.
func TestClockStopClosesAfterTheLoop(t *testing.T) {
	back := &slowBackend{}
	c := NewClock(back, &countingProvider{})
	stop := c.Start(nil)
	time.Sleep(50 * time.Millisecond)
	stop()
	if back.closed.Load() != 1 {
		t.Fatal("backend not closed")
	}
	if back.afterClose.Load() != 0 {
		t.Fatalf("%d frames were written after Close", back.afterClose.Load())
	}
	stop() // idempotent
}

// countingProvider changes the view every frame so every tick presents.
type countingProvider struct{ n int64 }

func (p *countingProvider) View(int64) View {
	p.n++
	return View{Label: "x", RemainingMS: p.n * 1000}
}
