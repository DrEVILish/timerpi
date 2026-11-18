//go:build linux

package drm

import (
	"bytes"
	"testing"
)

func TestAlternatingBuffersKeepBothFramesOfDamage(t *testing.T) {
	b := &KMSBackend{w: 8, h: 2, draw: 1}
	for i := range b.bufs {
		b.bufs[i] = dumbBuffer{data: make([]byte, 80), stride: 40}
	}
	img := NewImage(8, 2)
	for step, rect := range []Rect{{X: 0, Y: 0, W: 1, H: 1}, {X: 7, Y: 1, W: 1, H: 1}, {X: 3, Y: 0, W: 1, H: 1}} {
		img.SetPixel(rect.X, rect.Y, uint32(step+1))
		damage := []Rect{rect}
		if err := b.copyDamage(img, damage); err != nil {
			t.Fatal(err)
		}
		scanout := b.bufs[b.draw]
		for y := 0; y < 2; y++ {
			if !bytes.Equal(scanout.data[y*scanout.stride:y*scanout.stride+32], img.Pix[y*img.Stride:y*img.Stride+32]) {
				t.Fatalf("frame %d, row %d lost damage from the other buffer", step+1, y)
			}
		}
		b.advanceBuffers(damage)
	}
}
