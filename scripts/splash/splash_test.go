//go:build linux

package main

import (
	"testing"
	"unsafe"
)

// TestStructMirrorsMatchKernel locks the fbdev struct layout. The ioctl
// ABI is fixed: a mirror bigger/smaller than the kernel's struct would
// decode geometry wrongly and corrupt every pixel written.
//
// Kernel expectations on 64-bit Linux (uapi/linux/fb.h):
//
//	fb_var_screeninfo → 160 bytes (all fields u32, no padding)
//	fb_fix_screeninfo →  80 bytes (two unsigned long force 8-align)
func TestStructMirrorsMatchKernel(t *testing.T) {
	if got := unsafe.Sizeof(fbVarScreeninfo{}); got != 160 {
		t.Fatalf("fbVarScreeninfo mirror = %d bytes, want 160", got)
	}
	if got := unsafe.Sizeof(fbFixScreeninfo{}); got != 80 {
		t.Fatalf("fbFixScreeninfo mirror = %d bytes, want 80", got)
	}
}

// TestChanScaleAndBlend checks the deterministic pixel helpers: the
// 5-6-5 reduction for 16bpp frames and the alpha-over-black fold the
// splash logo uses.
func TestChanScaleAndBlend(t *testing.T) {
	if got := chanScale(0xff, 5); got != 31 {
		t.Fatalf("chanScale(0xff, 5) = %d, want 31 (565 red max)", got)
	}
	if got := chanScale(0x80, 6); got != 32 {
		t.Fatalf("chanScale(0x80, 6) = %d, want 32 (565 green>=128)", got)
	}
	if got := chanScale(0xff, 8); got != 0xff {
		t.Fatalf("chanScale(0xff, 8) = %d, want 255", got)
	}

	// Fully transparent → black; ~50% alpha of mid-gray → dark gray.
	r, g, b := blendOverBlack(0xff, 0xff, 0xff, 0)
	if r != 0 || g != 0 || b != 0 {
		t.Fatalf("alpha=0 did not fold to black: %d,%d,%d", r, g, b)
	}
	r, g, b = blendOverBlack(0x60, 0x60, 0x60, 0x80)
	if r != 0x30 || g != 0x30 || b != 0x30 {
		t.Fatalf("half-alpha gray = %d,%d,%d, want 0x30", r, g, b)
	}
	r, g, b = blendOverBlack(0x10, 0x20, 0x30, 0xff)
	if r != 0x10 || g != 0x20 || b != 0x30 {
		t.Fatalf("opaque source changed: %d,%d,%d", r, g, b)
	}
}

// TestPack565 pins 16bpp pixel packing (LSB-first bytes, little-endian).
func TestPack565(t *testing.T) {
	b := &fbdev{v: fbVarScreeninfo{
		BitsPerPixel: 16,
		Red:          fbBitfield{Offset: 11, Length: 5},
		Green:        fbBitfield{Offset: 5, Length: 6},
		Blue:         fbBitfield{Offset: 0, Length: 5},
	}, bytesPerPixel: 2}
	raw := b.pack(0xff, 0xff, 0xff)
	if raw[0] != 0xff || raw[1] != 0xff {
		t.Fatalf("white 565 = % x, want ff ff", raw)
	}
	raw = b.pack(0xff, 0, 0)
	if raw[0] != 0x00 || raw[1] != 0xf8 {
		t.Fatalf("red 565 = % x, want 00 f8", raw)
	}
	raw = b.pack(0, 0, 0xff)
	if raw[0] != 0x1f || raw[1] != 0x00 {
		t.Fatalf("blue 565 = % x, want 1f 00", raw)
	}
}

// TestPack32ABGR pins 32bpp packing against the common Pi layout
// (blue at offset 0, alpha at offset 24 → bytes B,G,R,A little-endian).
func TestPack32ABGR(t *testing.T) {
	b := &fbdev{v: fbVarScreeninfo{
		BitsPerPixel: 32,
		Red:          fbBitfield{Offset: 16, Length: 8},
		Green:        fbBitfield{Offset: 8, Length: 8},
		Blue:         fbBitfield{Offset: 0, Length: 8},
		Transp:       fbBitfield{Offset: 24, Length: 8},
	}, bytesPerPixel: 4}
	raw := b.pack(0x7c, 0x3a, 0xed) // TimerPi purple #7C3AED
	want := []byte{0xed, 0x3a, 0x7c, 0xff}
	for i, w := range want {
		if raw[i] != w {
			t.Fatalf("purple 32bpp = % x, want % x", raw, want)
		}
	}
}
