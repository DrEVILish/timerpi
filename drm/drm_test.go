// drm_test.go — package tests: backend contract with a fake cyclic
// in-memory backend, atlas/metrics parsing, glyph coverage, a2b color
// + little-endian buffer math. The real-device integration helper for
// the later Pi run lives in hw_test.go (skip-guarded).
package drm

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// ---- load once for all tests ----

var testFonts = mustFonts()

func mustFonts() *Fonts {
	f, err := LoadFonts()
	if err != nil {
		panic(err)
	}
	return f
}

// TestAtlasMetricsParse asserts the committed atlas set parses and
// records coverage for digits 0-9, ':' '-' '+' in the clock faces and
// the OVERTIME/NEXT word coverage in the small text atlases.
func TestAtlasMetricsParse(t *testing.T) {
	for _, nameEM := range []string{"clock-410", "clock-320", "ui-64", "msg-160"} {
		a, ok := testFonts.Atlases[nameEM]
		if !ok {
			t.Fatalf("atlas %s missing from parsed metrics", nameEM)
		}
		if a.CellW <= 0 || a.CellH <= 0 {
			t.Errorf("%s: bad cell geometry %dx%d", nameEM, a.CellW, a.CellH)
		}
		if a.Baseline <= 0 || a.Baseline >= a.CellH {
			t.Errorf("%s: baseline %d outside cell %d", nameEM, a.Baseline, a.CellH)
		}
		// Coverage grid must match the PNG and the declared cells.
		want := a.CellW * len(a.glyphs)
		if got := a.cov.Bounds().Dx(); got < want {
			t.Errorf("%s: atlas row width %d < %d glyphs×%d", nameEM, got, len(a.glyphs), a.CellW)
		}
		if a.cov.Bounds().Dy() != a.CellH {
			t.Errorf("%s: atlas row height %d != cellH %d", nameEM, a.cov.Bounds().Dy(), a.CellH)
		}
	}

	clockPlan := [][2]string{
		{"clock-410", "0123456789:-+ "},
		{"clock-320", "0123456789:-+ "},
		{"ui-64", "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789%:.,-!?/"},
		{"msg-160", "OVERTIMEABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"},
	}
	for _, plan := range clockPlan {
		a := testFonts.Atlases[plan[0]]
		for _, r := range plan[1] {
			g, ok := a.Glyph(r)
			if !ok {
				t.Errorf("%s: missing glyph %q", a.Name, string(r))
				continue
			}
			if r != ' ' && (g.Ink.Dx() <= 0 || g.Ink.Dy() <= 0) {
				t.Errorf("%s: glyph %q has empty ink", a.Name, string(r))
			}
			if g.OffX < 0 || g.OffX+g.Ink.Dx() > a.CellW {
				t.Errorf("%s: glyph %q ink X %d+%d escapes the cell (atlas abs %v)",
					a.Name, string(r), g.OffX, g.Ink.Dx(), g.Ink)
			}
			if g.OffY < 0 || g.OffY+g.Ink.Dy() > a.CellH {
				t.Errorf("%s: glyph %q ink Y escapes the cell", a.Name, string(r))
			}
			// Digits must be tall — nearly down to the baseline row.
			if r >= '0' && r <= '9' {
				if g.Ink.Max.Y < a.Baseline-2 {
					t.Errorf("%s: digit %q ink ends at %d, expected to reach ~baseline %d",
						a.Name, string(r), g.Ink.Max.Y, a.Baseline)
				}
			}
		}
	}

	// A glyph really rasterizes: '0' of the big clock must have ink in
	// its box, and nothing left of the box.
	a := testFonts.Atlases["clock-410"]
	g, ok := a.Glyph('0')
	if !ok {
		t.Fatal("atlas-clock-410 lost glyph '0'")
	}
	ink := 0
	for y := g.Ink.Min.Y; y < g.Ink.Max.Y; y++ {
		for x := g.Ink.Min.X; x < g.Ink.Max.X; x++ {
			if a.cov.GrayAt(x, y).Y > 0 {
				ink++
			}
		}
	}
	if ink == 0 {
		t.Fatal("atlas-clock-410 glyph '0' rasterized nothing — assets broken")
	}
	for x := 0; x < g.Ink.Min.X; x++ {
		for y := g.Ink.Min.Y; y < g.Ink.Max.Y; y++ {
			if a.cov.GrayAt(x, y).Y != 0 {
				t.Fatal("atlas-clock-410 '0' coverage bleeds left of its ink box")
			}
		}
	}
}

// ---- a2b color + little-endian buffer math ----

func TestA2BParse(t *testing.T) {
	cases := []struct {
		in   string
		def  uint32
		want uint32
	}{
		{"#7c3aed", 0, 0xff7c3aed},
		{"7C3AED", 0, 0xff7c3aed},
		{"#fff", 0x11223344, 0x11223344},
		{"#gggggg", 0x5, 0x5},
		{"", 0x6, 0x6},
		{"#ef4444", 0, 0xffef4444},
	}
	for _, c := range cases {
		if got := a2b(c.in, c.def); got != c.want {
			t.Errorf("a2b(%q) = %08x, want %08x", c.in, got, c.want)
		}
	}
}

// TestLittleEndianBufferMath: a stored 0xFF7C3AED pixel must sit in
// memory as B,G,R,A bytes — exactly KMS XRGB8888/depth-24 wire order.
func TestLittleEndianBufferMath(t *testing.T) {
	im := NewImage(4, 4)
	im.fillRectFast(im.Rect(), 0xff14161a)
	im.SetPixel(1, 1, 0xff7c3aed)
	off := 1*im.Stride + 1*4 // byte stride, 4 bytes per pixel
	want := []byte{0xed, 0x3a, 0x7c, 0xff}
	if got := im.Pix[off : off+4]; !reflect.DeepEqual(got, want) {
		t.Fatalf("pixel bytes % X, want % X", got, want)
	}
	// Every pixel lane 3 (X) must be opaque: the driver output reads it
	// verbatim for depth-24 framebuffers.
	if im.Pix[7] != 0xff || im.Pix[im.Stride+7] != 0xff {
		t.Fatal("fill leaves X transparent — XRGB8888 output unreadable")
	}
}

// ---- basic image ops ----

func TestImageRectMath(t *testing.T) {
	a := Rect{10, 10, 100, 50}
	b := Rect{50, 40, 100, 100}
	if got, want := a.Clip(b), (Rect{50, 40, 60, 20}); got != want {
		t.Fatalf("Clip = %+v, want %+v", got, want)
	}
	if u := a.Union(b); u != (Rect{10, 10, 140, 130}) {
		t.Fatalf("Union = %+v", u)
	}
	if !b.ContainsRect(a.Clip(b)) {
		t.Fatal("clip result escapes the clipper")
	}
	if (Rect{}).Empty() != true || (Rect{0, 0, 0, 5}).Empty() != true {
		t.Fatal("Empty is wrong")
	}
	if !a.Contains(20, 20) || a.Contains(112, 10) {
		t.Fatal("point Contains is wrong")
	}
}

func TestImageFillAndCopy(t *testing.T) {
	src := NewImage(32, 32)
	src.fillRectFast(src.Rect(), 0xff112233)
	dst := NewImage(64, 40)
	dst.fillRectFast(dst.Rect(), 0)
	dst.CopyRect(src, Rect{0, 0, 32, 32}, 10, 5)
	for _, p := range [][2]int{{10, 5}, {41, 36}, {25, 25}} {
		if got := dst.Pixel(p[0], p[1]); got != 0xff112233 {
			t.Fatalf("CopyRect pixel %v = %08x", p, got)
		}
	}
	if dst.Pixel(9, 5) != 0 || dst.Pixel(42, 5) != 0 {
		t.Fatal("CopyRect spilled outside the target rect")
	}
	// Oversized source rects clip, no panic.
	dst.CopyRect(src, Rect{0, 0, 100, 100}, -5, -5)
	if dst.Pixel(0, 0) != 0xff112233 {
		t.Fatal("clipped copy at edge lost content")
	}
}

// ---- backend contract with a fake cyclic in-memory backend ----

// cyclicBackend is the test double: two in-memory buffers alternating
// roles exactly like the KMS double buffer, recording every Present.
type cyclicBackend struct {
	bufs     [2][]byte
	draw     int // buffer the NEXT Present writes
	w, h     int
	opens    int
	closed   bool
	presents int
	dirs     [][]Rect
	err      error // injected failure
}

func newCyclicBackend(w, h int) *cyclicBackend {
	b := &cyclicBackend{w: w, h: h, draw: 1}
	for i := range b.bufs {
		b.bufs[i] = make([]byte, 4*w*h)
	}
	return b
}

func (b *cyclicBackend) Open() error      { b.opens++; return nil }
func (b *cyclicBackend) Size() (int, int) { return b.w, b.h }
func (b *cyclicBackend) Close() error     { b.closed = true; return nil }

func (b *cyclicBackend) Present(buf []byte, dirty []Rect) error {
	b.presents++
	b.dirs = append(b.dirs, dirty)
	if b.err != nil {
		return b.err
	}
	if len(dirty) == 0 {
		return nil // contract: empty damage never touches the buffers
	}
	for _, r := range dirty {
		r = r.Clip(Rect{0, 0, b.w, b.h})
		for dy := 0; dy < r.H; dy++ {
			src := buf[(r.Y+dy)*4*b.w+r.X*4:]
			copy(b.bufs[b.draw][(r.Y+dy)*4*b.w+r.X*4:], src[:r.W*4])
		}
	}
	b.draw ^= 1
	return nil
}

func TestBackendContract(t *testing.T) {
	back := newCyclicBackend(LayoutW, LayoutH)
	if err := back.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	w, h := back.Size()
	if w != LayoutW || h != LayoutH {
		t.Fatalf("Size = %dx%d", w, h)
	}
	if back.opens != 1 {
		t.Fatal("Open not called once")
	}

	v := View{Label: "WELCOME", RemainingMS: 65_000, Progress: 0.5, WallClock: "12:00:01"}
	img := Render(v)
	if err := back.Present(img.Pix, []Rect{img.Rect()}); err != nil {
		t.Fatalf("Present: %v", err)
	}
	// Buffer roles swapped to the other double-buffer slot.
	if back.draw != 0 {
		t.Fatal("Present did not flip the cyclic buffers")
	}
	scanned := back.bufs[1] // just-flipped front
	if scanned[0] != img.Pix[0] || scanned[len(scanned)-1] != img.Pix[len(img.Pix)-1] {
		t.Fatal("cyclic Present did not copy the presented frame")
	}

	// Empty damage is a no-op present (still counted, no flip).
	before := back.draw
	if err := back.Present(img.Pix, nil); err != nil {
		t.Fatalf("Present(dirty=nil): %v", err)
	}
	if back.draw != before {
		t.Fatal("empty damage must not flip buffers")
	}

	if back.closed {
		t.Fatal("Close flagged before Close was called")
	}
	if err := back.Close(); err != nil {
		t.Fatal(err)
	}
	if !back.closed {
		t.Fatal("Close did not mark the backend closed")
	}
}

// KMSBackend must stay inert until Open: zero-size + no-op Close.
func TestKMSBackendUnopenedIsInert(t *testing.T) {
	var b KMSBackend
	if w, h := b.Size(); w != 0 || h != 0 {
		t.Fatalf("unopened KMS backend reported %dx%d", w, h)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close on unopened backend: %v", err)
	}
}

// ---- clock loop with fake providers ----

type fakeProvider struct {
	views []View
	calls int
	nows  []int64
}

func (p *fakeProvider) View(nowMS int64) View {
	p.calls++
	p.nows = append(p.nows, nowMS)
	return p.views[min(p.calls-1, len(p.views)-1)]
}

type fixedProvider View

func (p fixedProvider) View(int64) View { return View(p) }

func TestClockLoopWithFakeBackend(t *testing.T) {
	back := newCyclicBackend(LayoutW, LayoutH)
	if err := back.Open(); err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	p := &fakeProvider{views: []View{
		{RemainingMS: 90_000, Progress: 0.1, WallClock: "12:00:00"},
		{RemainingMS: 90_000, Progress: 0.1, WallClock: "12:00:00"}, // identical
		{RemainingMS: 89_000, Progress: 0.1, WallClock: "12:00:01"}, // digit moves
	}}
	c := NewClock(back, p)
	c.interval = time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := c.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p.calls < 3 {
		t.Fatalf("provider was called only %d times — the loop ran too short", p.calls)
	}
	// must have seen a real ms wall clock pushed in
	if len(p.nows) == 0 || p.nows[0] <= 0 {
		t.Fatalf("provider got nowMS=%v — Clock must inject UnixMilli", p.nows)
	}

	// Exactly two visual changes shipped: view 1 and view 3. Identical
	// views (calls ≥ 3 with view 3 looping) must not present again.
	if back.presents != 2 {
		t.Fatalf("presents = %d, want 2 (unchanged views must skip present)", back.presents)
	}
	inspect := back.dirs[1] // the second visual change (seconds digits + wall clock)
	if len(inspect) == 0 {
		t.Fatal("second visual change shipped no damage rects")
	}
	clockBandSites := []Rect{Rect{400, 300, 1120, 420}, Rect{1400, 40, 520, 160}}
	assertDamageInRects(t, inspect, clockBandSites)
	// Only the wall clock and the touched slots may dirty — the bar and
	// header chrome must stay silent.
	for _, r := range inspect {
		if r.Y >= barY {
			t.Fatalf("damage %+v touches the bar on a clock-only change", r)
		}
	}
}

func TestClockGivesUpAfterPersistentPresentFailure(t *testing.T) {
	back := newCyclicBackend(LayoutW, LayoutH)
	back.err = fmt.Errorf("engine stall")
	p := fixedProvider(View{RemainingMS: 10_000, Progress: 0.2, WallClock: "00:00:10"})
	c := NewClock(back, p)
	c.interval = time.Millisecond
	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("persistent Present failure must terminate the loop with an error")
	}
	if back.presents != c.maxFailures {
		t.Fatalf("gave up after %d presents, want %d", back.presents, c.maxFailures)
	}
}

// Frame() reset path: after a failed Present the baseline clears and
// the next success ships the full frame again.
func TestFrameResetsBaselineAfterPresentError(t *testing.T) {
	back := newCyclicBackend(LayoutW, LayoutH)
	back.err = fmt.Errorf("stall")
	c := NewClock(back, fixedProvider(View{RemainingMS: 10_000, Progress: 0.2, WallClock: "00:00:10"}))
	if _, err := c.Frame(1000); err == nil {
		t.Fatal("Frame must surface the injected Present error")
	}
	if c.last != nil {
		t.Fatal("failed Present must reset the diff baseline to nil")
	}
	// Next good present (fresh clock round) ships the FULL frame.
	back.err = nil
	if _, err := c.Frame(1000); err != nil {
		t.Fatalf("recovery Frame: %v", err)
	}
	full := false
	for _, r := range back.dirs[len(back.dirs)-1] {
		if r.W >= LayoutW && r.H >= LayoutH {
			full = true
		}
	}
	if !full {
		t.Fatalf("expected a full-frame damage rect after baseline reset, got %+v",
			back.dirs[len(back.dirs)-1])
	}
}
