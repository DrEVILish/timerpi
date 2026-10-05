// runclock.go drives the presentation loop: at 50 fps (20 ms ticks) it
// asks the Provider for the current View, renders it, diffs against
// the last frame, and hands the damage rects to the Backend.
//
// Pacing note: with the KMS backend Present blocks until the page
// flip completes — the flip lands ON the next vblank, so the
// presented rate is exactly the panel refresh (50 Hz) and no separate
// WAIT_VBLANK is needed. The 20 ms ticker merely keeps the loop
// scheduled when no vblank pacing exists (fbdev), and skips work
// entirely when the View hasn't changed.
package drm

import (
	"context"
	"fmt"
	"log"
	"time"
)

// Provider supplies the state to render. The ws/engine package
// implements it at integration (View from the live snapshot).
type Provider interface {
	View(nowMS int64) View
}

// Clock owns the render/present loop.
type Clock struct {
	back        Backend
	provider    Provider
	interval    time.Duration // tick period
	last        *Image        // last presented frame (for Diff)
	lastView    View          // provider-equality skip gate
	logger      *log.Logger
	maxFailures int
}

// NewClock wires a presenter. interval defaults to 20 ms (50 fps).
func NewClock(back Backend, provider Provider) *Clock {
	return &Clock{
		back:        back,
		provider:    provider,
		interval:    20 * time.Millisecond,
		logger:      log.Default(),
		maxFailures: 60, // >1s of consecutive Present failures gives up
	}
}

// SetLogger overrides the destination of loop diagnostics.
func (c *Clock) SetLogger(l *log.Logger) { c.logger = l }

// Frame renders and presents one tick. nowMS is the wall clock in ms
// (the Provider decides what to do with it). Returns whether anything
// changed; transient Present errors are logged, while the caller sees
// only the count of consecutive failures.
func (c *Clock) Frame(nowMS int64) (bool, error) {
	v := c.provider.View(nowMS)

	// Cheap gate: an unchanged View always renders byte-identically
	// (Render is pure), so skip the entire render before it starts.
	if c.last != nil && v == c.lastView {
		return false, nil
	}
	c.lastView = v

	img := Render(v)
	dirty := Diff(c.last, img)
	if len(dirty) == 0 {
		c.last = img
		return false, nil
	}
	if err := c.back.Present(img.Pix, dirty); err != nil {
		// The scan buffers may hold a partial frame; drop the diff
		// baseline so the next success copies the frame in full.
		c.last, c.lastView = nil, View{}
		return true, err
	}
	c.last = img
	return true, nil
}

// Run loops until ctx is done. It presents immediately, then on every
// tick. Present failures are logged; after maxFailures consecutive
// failures the loop returns the last error (systemd restarts us).
// Backend.Open is the caller's job.
func (c *Clock) Run(ctx context.Context) error {
	w, h := c.back.Size()
	if w != LayoutW || h != LayoutH {
		c.logger.Printf("drm: backend is %dx%d, renderer renders %dx%d — output clipped/framed as-is",
			w, h, LayoutW, LayoutH)
	}

	t := time.NewTicker(c.interval)
	defer t.Stop()

	fails := 0
	for {
		if err := c.step(); err != nil {
			fails++
			if c.logger != nil {
				c.logger.Printf("drm: present failed (%d consecutive): %v", fails, err)
			}
			if fails >= c.maxFailures {
				return fmt.Errorf("drm: presenting gave up after %d consecutive failures: %w", fails, err)
			}
		} else {
			fails = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// step wraps Frame with the monotonic wall source.
func (c *Clock) step() error {
	_, err := c.Frame(time.Now().UnixMilli())
	return err
}
