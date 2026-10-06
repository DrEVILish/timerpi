package drm

import "testing"

// fakeFB is an FBBackend over plain memory: 32-bit XRGB, a padded stride.
func fakeFB(w, h int) *FBBackend {
	stride := w*4 + 64 // padded rows, like real panels
	return &FBBackend{
		data: make([]byte, stride*h), w: w, h: h, bpp: 32, stride: stride,
		v: fbVarScreeninfo{Red: fbBitfield{Offset: 16, Length: 8}, Green: fbBitfield{Offset: 8, Length: 8}, Blue: fbBitfield{Offset: 0, Length: 8}},
	}
}

// BUGLOG RW48: the 1920x1080 frame lands unsheared on a smaller panel
// (clipped), and a larger panel no longer fails every frame.
func TestPresentOnOtherPanelSizes(t *testing.T) {
	img := NewImage(LayoutW, LayoutH)
	mark := func(x, y int, b byte) { img.Pix[y*img.Stride+x*4] = b } // blue channel
	mark(0, 1, 11)
	mark(1279, 719, 22)
	mark(1919, 1079, 33)
	for _, size := range [][2]int{{1280, 720}, {1366, 768}, {3840, 2160}} {
		fb := fakeFB(size[0], size[1])
		if err := fb.Present(img, []Rect{img.Rect()}); err != nil {
			t.Fatalf("%dx%d: %v", size[0], size[1], err)
		}
		at := func(x, y int) byte { return fb.data[y*fb.stride+x*4] }
		if at(0, 1) != 11 || at(1279, 719) != 22 {
			t.Errorf("%dx%d: frame sheared or misplaced (%d, %d)", size[0], size[1], at(0, 1), at(1279, 719))
		}
		if size[0] >= LayoutW && at(1919, 1079) != 33 {
			t.Errorf("%dx%d: bottom-right pixel missing", size[0], size[1])
		}
	}
}
