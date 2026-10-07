// display.go defines the presentation Backend contract and wires the
// concrete implementations with the environment:
//
//	TIMERPI_DISPLAY = off | drm | fb   (unset: drm, then fb, then off)
//	DRM_CARD        = /dev/dri/card0   (KMS card to open)
//	DRM_CONNECTOR   = HDMI-A-1         (optional exact connector name filter)
//	TIMERPI_FBDEV   = /dev/fb0         (fbdev fallback)
//
// The Backend is deliberately tiny so main.go can own lifecycle: Open()
// claims the display (and for KMS becomes DRM master — nothing else
// may hold the card first, see README), Size() reports the panel's
// geometry, Present(buf, dirty) pushes one rendered frame's damage and
// paces to the refresh, Close() releases everything.
package drm

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Backend is one display output. Present receives the full frame
// buffer (Image.Pix layout: 32bpp little-endian ARGB = KMS XRGB8888) —
// but only copies the dirty rects, so callers that ship an empty dirty
// list get a free no-op.
type Backend interface {
	Open() error
	Size() (w, h int)
	// Present copies the dirty rects of img (its own size and stride, not
	// the panel's) to the screen, clipped to the smaller of the two
	// (BUGLOG RW48: a 720p panel used to shear a 1080p frame).
	Present(img *Image, dirty []Rect) error
	Close() error
}

// Selected describes which backend OpenDisplay picked and why.
type Selected struct {
	Name    string // "drm" | "fb" | "off"
	Backend Backend
}

// OpenDisplay applies the TIMERPI_DISPLAY selection. Errors carry the
// per-backend failure reasons; "off" never errors (a knob the operator
// can set explicitly).
func OpenDisplay() (Selected, error) {
	switch strings.ToLower(os.Getenv("TIMERPI_DISPLAY")) {
	case "off", "none", "disabled":
		return Selected{Name: "off", Backend: OffBackend{}}, nil
	case "drm":
		b := NewKMSBackend()
		if err := b.Open(); err != nil {
			return Selected{}, fmt.Errorf("drm display failed: %w", err)
		}
		return Selected{Name: "drm", Backend: b}, nil
	case "fb":
		b := NewFBBackend()
		if err := b.Open(); err != nil {
			return Selected{}, fmt.Errorf("fb display failed: %w", err)
		}
		return Selected{Name: "fb", Backend: b}, nil
	case "", "auto":
		b := NewKMSBackend()
		if err := b.Open(); err != nil {
			fb := NewFBBackend()
			if err2 := fb.Open(); err2 != nil {
				return Selected{}, errors.Join(
					fmt.Errorf("drm display failed: %w", err),
					fmt.Errorf("fb display failed: %w", err2))
			}
			return Selected{Name: "fb", Backend: fb}, nil
		}
		return Selected{Name: "drm", Backend: b}, nil
	default:
		return Selected{}, fmt.Errorf("TIMERPI_DISPLAY: unknown mode %q", os.Getenv("TIMERPI_DISPLAY"))
	}
}

// OffBackend is the explicit "don't touch the display" backend.
type OffBackend struct{}

func (OffBackend) Open() error                  { return nil }
func (OffBackend) Size() (int, int)             { return LayoutW, LayoutH }
func (OffBackend) Present(*Image, []Rect) error { return nil }
func (OffBackend) Close() error                 { return nil }
