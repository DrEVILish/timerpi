// Package drm renders the TimerPi timer HUD straight onto the Pi's
// HDMI output: a pure layout engine drives a 32bpp framebuffer, and a
// KMS (dumb-buffer + page flip) or fbdev backend presents it at the
// display's refresh rate, copying only damage rects.
//
// framebuffer.go holds the pixel core: a little-endian 32bpp ARGB
// buffer, rect primitives, and the rect copy helper the backends use
// for damage propagation. It is pure Go — no syscall — so on-device and
// in-container tests exercise exactly the shipping math.
package drm

import (
	"encoding/binary"
	"strconv"
	"strings"
)

// Image is a 32bpp little-endian ARGB9001-style pixel buffer: every
// pixel is four byte lanes stored as B, G, R, A (little-endian order of
// the 0xAARRGGBB Golang uint32), which is bit-identical to KMS
// XRGB8888/depth-24 scanout when A=0xff. Stride is in BYTES, matching
// the drm dumb-buffer pitch (and fbdev line_length), and may exceed
// 4*W; only the first W pixels of a row are drawable.
//
// In addition to raw pixels, an Image carries the layout engine's
// damage bookkeeping:
//   - Slots: one entry per clock glyph cell (glyph-slot interning; see
//     face.go) — the only per-frame damage the common case produces.
//   - Sites: named rects for every other painted component (logo, wall
//     clock, message strip, …) so Diff can compare them as units.
type Image struct {
	W, H   int
	Stride int // bytes per row (always 4*W here)

	Pix []byte // Stride*H bytes

	Slots []Slot
	Sites []Site
}

// Slot is one interned glyph cell. Key identifies the rendered content
// (rune + color + atlas); two Slots with equal Rect and Key are
// guaranteed pixel-identical because glyph cells never overlap other
// painted content and rendering is deterministic — that is the
// interning contract Diff relies on.
type Slot struct {
	Rect Rect
	Key  string
}

// Site is a named component rect (logo, message strip, progress
// bar, …). Diff compares same-named Sites as one damage unit.
type Site struct {
	Name string
	Rect Rect
}

// NewImage allocates a W×H image filled with Color 0 (transparent
// black); callers fill the background right after.
func NewImage(w, h int) *Image {
	im := &Image{W: w, H: h, Stride: 4 * w, Pix: make([]byte, 4*w*h)}
	return im
}

// Rect of the whole drawable area (origin 0,0).
func (im *Image) Rect() Rect { return Rect{0, 0, im.W, im.H} }

// SetPixel writes one ARGB pixel; out-of-bounds coordinates are
// ignored (glyph blits rely on the clamp, not on pre-clip math).
func (im *Image) SetPixel(x, y int, c uint32) {
	if x < 0 || y < 0 || x >= im.W || y >= im.H {
		return
	}
	binary.LittleEndian.PutUint32(im.Pix[y*im.Stride+x*4:], c)
}

// Pixel reads one ARGB pixel, or 0 out of bounds.
func (im *Image) Pixel(x, y int) uint32 {
	if x < 0 || y < 0 || x >= im.W || y >= im.H {
		return 0
	}
	return binary.LittleEndian.Uint32(im.Pix[y*im.Stride+x*4:])
}

// fillRow writes c across one full row (256-byte doubling copy: the
// compiler keeps this on memset-like memcpy paths).
func (im *Image) fillRow(y int, c uint32) {
	row := im.Pix[y*im.Stride : y*im.Stride+im.W*4]
	var seed [8]byte
	for i := 0; i < 4; i++ {
		seed[i] = byte(c >> (8 * i))
	}
	// rgba in little-endian = B,G,R,A.
	seed[0], seed[2] = byte(c), byte(c>>16)
	// two pixels per seed
	seed[4], seed[5], seed[6], seed[7] = seed[0], seed[1], seed[2], seed[3]
	copy(row[:8], seed[:8])
	for n := 8; n < len(row); n *= 2 {
		end := n * 2
		if end > len(row) {
			end = len(row)
		}
		copy(row[n:end], row[:end-n])
	}
}

// fillRectFast sets every pixel of r (clipped to the image) to c,
// row by row.
func (im *Image) fillRectFast(r Rect, c uint32) {
	r = r.Clip(im.Rect())
	for y := r.Y; y < r.Y+r.H; y++ {
		if r.W == im.W {
			im.fillRow(y, c)
			continue
		}
		row := im.Pix[y*im.Stride+r.X*4 : y*im.Stride+(r.X+r.W)*4]
		for i := 0; i < r.W*4; i += 4 {
			binary.LittleEndian.PutUint32(row[i:], c)
		}
	}
}

// Rect is an integral rectangle in screen coordinates.
type Rect struct{ X, Y, W, H int }

// Empty reports whether the rect covers nothing.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Contains reports whether p lies inside r.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H
} // Clip returns the intersection of r and o; empty rects include the
// clamped intersection's geometry (W/H ≤ 0).
func (r Rect) Clip(o Rect) Rect {
	x0, y0 := max(r.X, o.X), max(r.Y, o.Y)
	x1, y1 := min(r.X+r.W, o.X+o.W), min(r.Y+r.H, o.Y+o.H)
	return Rect{x0, y0, max(0, x1-x0), max(0, y1-y0)}
}

// Union returns the bounding rect of r and o.
func (r Rect) Union(o Rect) Rect {
	if r.Empty() {
		return o
	}
	if o.Empty() {
		return r
	}
	x0, y0 := min(r.X, o.X), min(r.Y, o.Y)
	x1, y1 := max(r.X+r.W, o.X+o.W), max(r.Y+r.H, o.Y+o.H)
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

// ContainsRect reports whether q lies fully inside r.
func (r Rect) ContainsRect(q Rect) bool {
	return !q.Empty() && q.X >= r.X && q.Y >= r.Y &&
		q.X+q.W <= r.X+r.W && q.Y+q.H <= r.Y+r.H
}

// mergeRects merges pairwise-overlapping (or touching) rects until a
// fixed point, so a damage list stays short even when many glyphs on
// one row change. Cheap at the couple dozen rects Diff produces.
func mergeRects(rs []Rect) []Rect {
	for changed := true; changed; {
		changed = false
	out:
		for i := 0; i < len(rs); i++ {
			for j := i + 1; j < len(rs); j++ {
				if mergeable(rs[i], rs[j]) {
					rs[i] = rs[i].Union(rs[j])
					rs[j] = rs[len(rs)-1]
					rs = rs[:len(rs)-1]
					changed = true
					break out
				}
			}
		}
	}
	return rs
}

// mergeable reports whether two rects overlap or touch on both axes so
// that their union adds no bounding-box slack beyond direct adjacency.
func mergeable(a, b Rect) bool {
	return !(a.X > b.X+b.W || b.X > a.X+a.W ||
		a.Y > b.Y+b.H || b.Y > a.Y+a.H)
}

// CopyRect copies the pixel data of src rect r from o into im at
// point (x, y), clipping both source and destination. Rows are copied
// independently so stride differences (fbdev padding) work.
func (im *Image) CopyRect(o *Image, r Rect, x, y int) {
	if o == nil {
		return
	}
	r = r.Clip(o.Rect())
	if r.Empty() {
		return
	}
	// Keep it inside the destination as well.
	if x < 0 {
		r.X -= x
		r.W += x
		x = 0
	}
	if y < 0 {
		r.Y -= y
		r.H += y
		y = 0
	}
	r.W = min(r.W, im.W-x)
	r.H = min(r.H, im.H-y)
	r = r.Clip(Rect{0, 0, im.W, im.H})
	if r.Empty() {
		return
	}
	for dy := 0; dy < r.H; dy++ {
		src := o.Pix[(r.Y+dy)*o.Stride+r.X*4:]
		dst := im.Pix[(y+dy)*im.Stride+x*4:]
		copy(dst[:r.W*4], src[:r.W*4])
	}
}

// pixelsEqual compares the pixel data of rect r between two
// same-sized images, aligned identically.
func pixelsEqual(a, b *Image, r Rect) bool {
	r = r.Clip(a.Rect()).Clip(b.Rect())
	if r.Empty() {
		return true
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		ra := a.Pix[y*a.Stride+r.X*4 : y*a.Stride+(r.X+r.W)*4]
		rb := b.Pix[y*b.Stride+r.X*4 : y*b.Stride+(r.X+r.W)*4]
		for i := 0; i < len(ra); i += 4 {
			if ra[i] != rb[i] || ra[i+1] != rb[i+1] ||
				ra[i+2] != rb[i+2] || ra[i+3] != rb[i+3] {
				return false
			}
		}
	}
	return true
}

// a2b parses one "#rrggbb" (or "rrggbb") color token into the packed
// 0xAARRGGBB pixel form this package stores (alpha forced opaque).
// Unparsable input returns def.
func a2b(s string, def uint32) uint32 {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return def
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return def
	}
	return 0xff000000 | uint32(v)
}

// luminance01 of a packed 0xrrggbb color — used to pick a readable
// text color on top of MessageColor strips.
func luminance01(c uint32) float64 {
	r := float64((c >> 16) & 0xff)
	g := float64((c >> 8) & 0xff)
	b := float64(c & 0xff)
	return (0.2126*r + 0.7152*g + 0.0722*b) / 255
}
