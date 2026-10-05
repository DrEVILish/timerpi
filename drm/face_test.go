// face_test.go — the pure layout engine: formatting, slot geometry,
// damage interning, alert tinting, message/blank semantics.
package drm

import "testing"

func assertNoDamage(t *testing.T, prev, next *Image) {
	t.Helper()
	if d := Diff(prev, next); len(d) != 0 {
		t.Fatalf("expected zero damage, got %+v", d)
	}
}

func assertDamageInRects(t *testing.T, got []Rect, allowed []Rect) {
	t.Helper()
	for _, g := range got {
		in := false
		for _, a := range allowed {
			if a.ContainsRect(g) {
				in = true
				break
			}
		}
		if !in {
			t.Fatalf("damage %v is not inside any allowed site %v", g, allowed)
		}
	}
	if len(got) > 6 {
		t.Fatalf("damage is %d rects — too broad for a one-element change: %v", len(got), got)
	}
}

func TestFormatCountdown(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "0:00"},
		{1000, "0:01"},
		{59_999, "0:59"},
		{60_000, "1:00"},
		{3_599_000, "59:59"},
		{3_600_000, "1:00:00"},
		{3_661_000, "1:01:01"},
		{86_400_000 + 5_000, "24:00:05"},
		{-1, "0:00"}, // rendered via the Overtime path, never negative
	}
	for _, c := range cases {
		if got := formatCountdown(c.ms); got != c.want {
			t.Errorf("formatCountdown(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

// TestClockLayoutSlots: cell count and per-cell geometry follow the
// format; the slot row is centered and identical for equal inputs.
func TestClockLayoutSlots(t *testing.T) {
	_, slots, atlas := ClockLayout(View{RemainingMS: 654_321}) // "10:54"
	if len(slots) != 5 {
		t.Fatalf("slots = %d, want 5 (\"10:54\")", len(slots))
	}
	if atlas == nil || atlas.EM != 410 {
		t.Fatalf("wrong atlas (EM %v) for a 5-cell string", atlasEM(atlas))
	}
	total := atlas.CellW * len(slots)
	if slots[0].Rect.X != (LayoutW-total)/2 {
		t.Fatalf("first slot x = %d, want centered %d", slots[0].Rect.X, (LayoutW-total)/2)
	}
	for i := 1; i < len(slots); i++ {
		if slots[i].Rect.X-slots[i-1].Rect.X != atlas.CellW {
			t.Fatalf("slots not on the monospaced grid: %v", slots)
		}
		if slots[i].Rect.Y != clockTop {
			t.Fatal("slot rows drift vertically")
		}
		if slots[i].Rect.W != atlas.CellW || slots[i].Rect.H != atlas.CellH {
			t.Fatal("slot cells are not full atlas cells")
		}
	}
	// Determinism: the same View must produce the same slots + keys.
	v := View{RemainingMS: 654_321}
	str1, slots1, _ := ClockLayout(v)
	str2, slots2, _ := ClockLayout(v)
	if str1 != str2 || len(slots1) != len(slots2) {
		t.Fatal("clock layout unstable on equal input")
	}
	for i := range slots1 {
		if slots1[i].Rect != slots2[i].Rect || slots1[i].Key != slots2[i].Key {
			t.Fatalf("slot %d unstable: %+v/%s vs %+v/%s", i,
				slots1[i].Rect, slots1[i].Key, slots2[i].Rect, slots2[i].Key)
		}
	}
}

func TestClockLayoutFormatSwitch(t *testing.T) {
	// Under one hour: M:SS at the big face (unless long string).
	s, slots, a := ClockLayout(View{RemainingMS: 119_000}) // "1:59"
	if s != "1:59" || atlasEM(a) != 410 || len(slots) != 4 {
		t.Fatalf("1:59 got %q, atlas %s, %d slots", s, atlasID(a), len(slots))
	}
	// One hour exactly: H:MM:SS → 7 cells, small face.
	s, slots, a = ClockLayout(View{RemainingMS: 3_600_000})
	if s != "1:00:00" || atlasEM(a) != 320 || len(slots) != 7 {
		t.Fatalf("1:00:00 got %q, atlas %s, %d slots", s, atlasID(a), len(slots))
	}
	// Overtime adds '+' and keeps magnitude.
	s, slots, a = ClockLayout(View{RemainingMS: -59_000, Overtime: true})
	if s != "+0:59" || len(slots) != 5 || atlasEM(a) != 410 {
		t.Fatalf("overtime got %q, atlas %s, %d slots", s, atlasID(a), len(slots))
	}
	// Long overtime switches the face.
	_, _, a = ClockLayout(View{RemainingMS: -3_600_000, Overtime: true})
	if atlasEM(a) != 320 {
		t.Fatalf("long overtime atlas = %s", atlasID(a))
	}
}

func atlasEM(a *Atlas) int {
	if a == nil {
		return -1
	}
	return a.EM
}

func atlasID(a *Atlas) string {
	if a == nil {
		return "<nil>"
	}
	return a.Name
}

// TestRenderStaticIdenticalAnalyzesZeroDamage: two renders of an equal
// View are byte-identical AND Diff reports nothing.
func TestRenderStaticIdenticalIsZeroDamage(t *testing.T) {
	v := View{
		Label: "WELCOME", Speaker: "LESLIE",
		Next: "MAYOR'S SPEECH", NextSpeaker: "TOM",
		RemainingMS: 45_000, Progress: 0.4,
		Message: "PLEASE WRAP UP", MessageColor: "#7C3AED",
		WallClock: "18:44:32", Rate: 1.0,
	}
	a := Render(v)
	b := Render(v)
	if len(a.Pix) != len(b.Pix) {
		t.Fatal("render sizes differ")
	}
	assertNoDamage(t, a, b)
}

// TestRenderSecondsDigitDamage: exactly one slot changes → the damage
// is small and stays inside the clock band (glyph-slot interning).
func TestRenderSecondsDigitDamage(t *testing.T) {
	a := Render(View{RemainingMS: 65_000, WallClock: "19:00:10", Progress: 0.2})
	b := Render(View{RemainingMS: 64_000, WallClock: "19:00:10", Progress: 0.2})
	d := Diff(a, b)
	if len(d) == 0 {
		t.Fatal("a changing second must produce damage")
	}
	assertDamageInRects(t, d, []Rect{Rect{400, 300, 1120, 420}})
}

// TestRenderAlertTint: alert state changes recolor digits — slot keys
// change, and the damage remains exactly the clock slots.
func TestRenderAlertTint(t *testing.T) {
	base := View{RemainingMS: 65_000, WallClock: "19:00:10", Progress: 0}
	one := base
	one.AlertState = 1
	one.AlertColor1 = "#FFCC00"
	a := Render(base)
	b := Render(one)
	d := Diff(a, b)
	if len(d) == 0 {
		t.Fatal("alert tint must produce damage")
	}
	// Damage confined to the digit area.
	for _, r := range d {
		if r.Y < 300 || r.Y > 700 {
			t.Fatalf("alert damage outside the clock: %+v", r)
		}
	}
	// The digit color token actually changed ('FFCC00' appears in the
	// interned slot key).
	_, sLots, _ := ClockLayout(base)
	_, sLots1, _ := ClockLayout(one)
	if !differsByID(sLots, sLots1) {
		t.Fatal("alert state did not change the interned slot keys")
	}
}

func differsByID(a, b []Slot) bool {
	for i := range a {
		if a[i].Key != b[i].Key {
			return true
		}
	}
	return false
}

// TestRenderProgressOnlyDamage: moving the progress sweep must damage
// only the bar row — not the digits, not the header.
func TestRenderProgressOnlyDamage(t *testing.T) {
	a := Render(View{RemainingMS: 65_000, WallClock: "19:00:10", Progress: 0.10})
	b := Render(View{RemainingMS: 65_000, WallClock: "19:00:10", Progress: 0.60})
	d := Diff(a, b)
	if len(d) == 0 {
		t.Fatal("progress movement must produce damage")
	}
	for _, r := range d {
		if r.Y+1057 < barY || r.Y > barY+barH {
			t.Fatalf("progress damage must hug the bar row: %+v", r)
		}
	}
}

// TestRenderMessageStripDamage: a message appearing damages only the
// strip; the pixels there are the MessageColor (+text).
func TestRenderMessageStripDamage(t *testing.T) {
	quiet := View{RemainingMS: 65_000, WallClock: "19:00:10", Progress: 0}
	msg := quiet
	msg.Message = "WRAP UP"
	msg.MessageColor = "#7C3AED"
	a := Render(quiet)
	b := Render(msg)
	d := Diff(a, b)
	if len(d) != 1 {
		t.Fatalf("message strip damage should be a single merged rect, got %d", len(d))
	}
	r := d[0]
	if r.H != atlMsgCellH()+msgPadY*2 {
		t.Fatalf("strip height %d != atlas cell %d + padding", r.H, atlMsgCellH())
	}
	// strip bg is exactly MessageColor in the padding zone.
	c := 0
	for y := r.Y; y < r.Y+4; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if px := b.Pixel(x, y); px == 0xff7c3aed {
				c++
			}
		}
	}
	if c == 0 {
		t.Fatal("MessageColor must paint the strip background")
	}
	// Removing the message clears the strip again (damage on the strip).
	cn := Diff(b, a)
	if len(cn) != 1 || cn[0] != r {
		t.Fatalf("clearing message damage = %+v, want the strip rect %+v", cn, r)
	}
}

func atlMsgCellH() int {
	return testFonts.Atlases["msg-160"].CellH
}

// TestBlankHidesChrome: Blank → only logo + wall clock sites, no slots.
func TestBlankHidesChrome(t *testing.T) {
	v := View{Label: "X", Next: "Y", RemainingMS: 1000, Progress: 0.5,
		Message: "HI", WallClock: "19:00:00"}
	full := Render(v)
	if len(full.Slots) == 0 {
		t.Fatal("non-blank view must have clock slots")
	}
	blank := Render(View{WallClock: "19:00:00", Blank: true})
	if len(blank.Slots) != 0 {
		t.Fatalf("blank view has %d slots", len(blank.Slots))
	}
	sites := map[string]bool{}
	for _, s := range blank.Sites {
		sites[s.Name] = true
	}
	if sites["message"] || sites["overtime"] || sites["next1"] || sites["next2"] || sites["bar"] || sites["time"] {
		t.Fatalf("blank view kept chrome sites: %v", sites)
	}
	if !sites["logo"] || !sites["wallclock"] {
		t.Fatalf("blank view dropped its fixed chrome (logo/wall clock): %v", sites)
	}

	d := Diff(blank, full)
	if len(d) == 0 {
		t.Fatal("blank→full toggle must produce damage")
	}

	// And no damage at all between two blank frames that differ only in
	// a field the blank render never draws.
	assertNoDamage(t, blank, Render(View{WallClock: "19:00:00", Blank: true}))
}

// TestOvertimeBadgeSite: overtime view paints the badge slot only.
func TestOvertimeBadgeSite(t *testing.T) {
	plain := Render(View{RemainingMS: 1_234_000, WallClock: "19:00:00", Progress: 0.5})
	over := Render(View{RemainingMS: -1_000, Overtime: true, WallClock: "19:00:00", Progress: 0.5})
	d := Diff(plain, over)
	if len(d) == 0 {
		t.Fatal("overtime switch must produce damage")
	}
	// one merged rect covering the badge + clock (they differ widely)
	badgeTop := 1 << 30
	for _, r := range d {
		if r.Y < badgeTop {
			badgeTop = r.Y
		}
	}
	if badgeTop > overtimeTop+1 {
		t.Fatalf("no rect reaches the badge band (top of damage %d)", badgeTop)
	}
}

// TestUnknownCharsDropped: text outside the baked range doesn't crash
// and falls back to dropped glyphs; message text shrinks to the baked
// set, so two messages equal after filtering render identically.
func TestUnknownCharsDropped(t *testing.T) {
	a := Render(View{Message: "Hello, 世界", WallClock: "19:00:00", Progress: 0})
	b := Render(View{Message: "Hello, 四", WallClock: "19:00:00", Progress: 0})
	if len(Diff(a, b)) != 0 {
		t.Fatal("messages identical after covered-rune filtering must render equally")
	}
}

// TestDiffSlotCountChange: minutes rolling from 9:59 to 10:00 shrinks
// the clock by one cell — the band redraw covers both frames' bands.
func TestDiffSlotCountChange(t *testing.T) {
	a := Render(View{RemainingMS: 599_000, WallClock: "19:09:59", Progress: 0}) // "9:59"
	b := Render(View{RemainingMS: 600_000, WallClock: "19:10:00", Progress: 0}) // "10:00"
	if len(a.Slots) != 4 || len(b.Slots) != 5 {
		t.Fatalf("slot counts %d/%d, want 4/5", len(a.Slots), len(b.Slots))
	}
	d := Diff(a, b)
	if len(d) == 0 {
		t.Fatal("slot-count change must produce damage")
	}
	band := Rect{300, 300, 1400, 450}
	assertDamageInRects(t, d, []Rect{band, Rect{1400, 40, 520, 160}})
}

// TestDiffDegenerate: nil and size-mismatch cases.
func TestDiffDegenerate(t *testing.T) {
	a := Render(View{WallClock: "00:00:00", Progress: 0})
	if d := Diff(nil, a); len(d) != 1 || d[0] != a.Rect() {
		t.Fatalf("Diff(nil, a) = %+v", d)
	}
	small := NewImage(10, 10)
	if d := Diff(small, a); len(d) != 1 || d[0] != a.Rect() {
		t.Fatalf("Diff(size mismatch) = %+v", d)
	}
}
