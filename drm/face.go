// face.go is the pure 1920×1080 layout engine: it turns a View into a
// full-frame ARGB Image and, with Diff, computes the damage between two
// frames. Glyph cells of the giant clock are interned as Slots — when
// only the seconds digit changes, the damage is one small rect; static
// frames produce zero rects. Everything here is pure Go: no syscall, no
// clock reads — the Provider (runclock.go) feeds time in.
package drm

import "fmt"

// Layout constants for the 1080p50 stage output. All geometry is
// derived from these; tests assert the derivations hold together.
const (
	LayoutW = 1920
	LayoutH = 1080
)

// Brand/token colors (PROTOCOL branding: primary purple, dark neutral
// background; green is logo-only).
const (
	colorBg         uint32 = 0xff14161a // dark neutral stage background
	colorDigit      uint32 = 0xffffffff // normal clock digits
	colorPurple     uint32 = 0xff7c3aed // progress fill (#7C3AED)
	colorRail       uint32 = 0xff23262e // progress rail
	colorMeta       uint32 = 0xff9ca3af // wall clock / secondary text
	colorOvertime   uint32 = 0xffdc2626 // OVERTIME badge background (red, not orange)
	colorOvertimeFg uint32 = 0xffffffff
)

// Layout geometry in pixels at 1080p, as component sites; the clock
// cells derive from atlas geometry at Runtime.
const (
	logoX, logoY, logoW, logoH = 40, 36, 96, 96

	wallRight = 1880 // right edge of the wall clock
	wallTop   = 56

	msgTop      = 170 // message strip top
	msgPadX     = 44  // strip horizontal padding
	msgPadY     = 22  // strip vertical padding (per side)
	overtimeTop = 308 // OVERTIME badge top
	overtimePad = 26  // badge padding per side

	clockTop = 400 // giant clock cell top

	next1Top = 872 // NEXT label line top
	next2Top = 944 // speaker line top
	nextX    = 160 // left edge of the NEXT block

	barY      = 1008 // progress bar top
	barH      = 28   // bar height
	barMargin = 24   // bar side margin
)

// logoRect is the top-left brand-mark site.
func logoRect() Rect { return Rect{logoX, logoY, logoW, logoH} }

// Alert states (mirror the engine's per-cue alert states 1/2).
const (
	AlertNormal = 0
	Alert1      = 1
	Alert2      = 2
)

// View is everything the renderer needs for one frame. It mirrors the
// engine snapshot per PROTOCOL.md; the ws/engine package feeds it via
// drm.Provider at integration.
type View struct {
	Label        string  // active cue label
	Speaker      string  // active cue speaker
	Next         string  // next cue label
	NextSpeaker  string  // next cue speaker
	Title        string  // show title (not drawn in v1; reserved)
	RemainingMS  int64   // countdown remaining (may be negative in overtime)
	Overtime     bool    // overtime state: badge + '+' sign
	AlertState   int     // 0 normal, 1 alert1 color, 2 alert2 color
	AlertColor1  string  // display color at alert1 threshold
	AlertColor2  string  // display color at alert2 threshold
	Progress     float64 // 0..1 position on the bottom progress bar
	Message      string  // overlay message text ("" = hidden)
	MessageColor string  // overlay strip color (#rrggbb)
	WallClock    string  // "HH:MM:SS" from the provider, refreshed 1/s
	Rate         float64 // countdown rate multiplier (not drawn in v1)
	Blank        bool    // blank the timer: logo + wall clock only
}

// fonts holds the loaded atlases; the face uses clockBig/clockSmall/ui/msg.
var defaultFonts *Fonts

func fonts() *Fonts {
	if defaultFonts == nil {
		f, err := LoadFonts()
		if err != nil {
			// Assets are embedded and validated by tests; at runtime a
			// broken atlas cannot happen short of corrupted binary.
			panic(fmt.Sprintf("drm: font load failed: %v", err))
		}
		defaultFonts = f
	}
	return defaultFonts
}

// faceAtlas picks the atlas set used by the layout.
type faceAtlas struct {
	clockBig   *Atlas // em 410: up to 6 cells
	clockSmall *Atlas // em 320: 7+ cells (H:MM:SS, long overtime)
	ui         *Atlas // labels, wall clock
	msg        *Atlas // message strip, OVERTIME badge
}

func newFaceAtlas(f *Fonts) *faceAtlas {
	return &faceAtlas{
		clockBig:   f.Atlases[key("clock", 410)],
		clockSmall: f.Atlases[key("clock", 320)],
		ui:         f.Atlases[key("ui", 64)],
		msg:        f.Atlases[key("msg", 160)],
	}
}

// layoutClock formats the countdown and lays out one Slot per glyph
// cell. Rules:
//   - not overtime: "H:MM:SS" from 1h, else "M:SS"; tenths never shown.
//   - overtime: "+" prefix and positive magnitude ("+M:SS", or
//     "+H:MM:SS" past an hour).
//   - atlas: clockBig for ≤6 cells, clockSmall for longer strings.
//
// The result is stable for equal inputs, so slot keys intern.
func ClockLayout(v View) (string, []Slot, *Atlas) {
	digits := newFaceAtlas(fonts())

	var s string
	if v.Overtime {
		s = "+" + formatCountdown(-v.RemainingMS)
	} else {
		s = formatCountdown(max64(0, v.RemainingMS))
	}

	atlas := digits.clockBig
	if len(s) > 6 {
		atlas = digits.clockSmall
	}

	total := atlas.Width(len(s))
	x0 := (LayoutW - total) / 2

	slots := make([]Slot, len(s))
	for i := range s {
		slots[i] = Slot{
			Rect: Rect{x0 + i*atlas.CellW, clockTop, atlas.CellW, atlas.CellH},
			Key:  fmt.Sprintf("%c|%s|%d", s[i], colorHex(digitColor(v)), atlas.EM),
		}
	}
	return s, slots, atlas
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// formatCountdown renders remaining ms as "M:SS" below an hour and
// "H:MM:SS" from it; tenths are never shown by default. Displays floor
// toward the next lower whole second.
func formatCountdown(remMS int64) string {
	if remMS < 0 {
		remMS = 0
	}
	totalSec := remMS / 1000
	hh := totalSec / 3600
	mm := (totalSec % 3600) / 60
	ss := totalSec % 60
	if hh > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hh, mm, ss)
	}
	if mm >= 60 { // defensively, never reachable when hh consumed it
		return fmt.Sprintf("%d:%02d:%02d", mm/60, mm%60, ss)
	}
	return fmt.Sprintf("%d:%02d", mm, ss)
}

// digitColor resolves the alert tint: 0 → white, 1 → AlertColor1,
// 2 → AlertColor2. Unparsable/missing tokens fall back to sane alert
// colors (yellow-then-red family; orange stays out of the toolchain).
func digitColor(v View) uint32 {
	switch v.AlertState {
	case Alert1:
		return a2b(v.AlertColor1, 0xfffde68a)
	case Alert2:
		return a2b(v.AlertColor2, 0xffef4444)
	default:
		return colorDigit
	}
}

// colorHex renders a packed color as 6 hex digits (slot keys only).
func colorHex(c uint32) string {
	return fmt.Sprintf("%06x", c&0xffffff)
}

// messageRect computes the message strip geometry for its text length.
func messageRect(text string, atlas *Atlas) Rect {
	if text == "" {
		return Rect{}
	}
	w := atlas.Width(len(text)) + msgPadX*2
	h := atlas.CellH + msgPadY*2
	return Rect{(LayoutW - w) / 2, msgTop, w, h}
}

// Render produces the full-frame image for one View. It is pure:
// identical Views render identical buffers, which is what slot
// interning relies on.
func Render(v View) *Image {
	im := NewImage(LayoutW, LayoutH)
	im.fillRectFast(im.Rect(), colorBg)
	f := fonts()
	atl := newFaceAtlas(f)

	// Brand mark (site "logo").
	lr := logoRect()
	drawLogo(im, f.Logo, lr)
	addSite(im, "logo", lr)

	// Wall clock (site "wallclock"), right-aligned top-right.
	wall := atl.ui.filterBaked(v.WallClock)
	if wall != "" {
		w := atl.ui.Width(len(wall))
		x := wallRight - w
		atl.ui.drawString(im, x, wallTop, wall, colorMeta)
		addSite(im, "wallclock", Rect{x, wallTop, w, atl.ui.CellH})
	}

	if v.Blank {
		return im
	}

	// Message overlay strip (site "message"): centered under the top
	// chrome, background colored, text contrast-chosen.
	if msg := atl.msg.filterBaked(v.Message); msg != "" {
		r := messageRect(msg, atl.msg)
		bg := a2b(v.MessageColor, 0xff7c3aed)
		fg := colorBg
		if luminance01(bg) < 0.5 {
			fg = colorDigit
		}
		im.fillRectFast(r, bg)
		atl.msg.drawString(im, r.X+msgPadX, r.Y+msgPadY, msg, fg)
		addSite(im, "message", r)
	}

	// OVERTIME badge (site "overtime"): above the clock block.
	if v.Overtime {
		badge := atl.msg.filterBaked("OVERTIME")
		w := atl.msg.Width(len(badge))
		r := Rect{(LayoutW - w) / 2, overtimeTop, w + overtimePad*2, atl.msg.CellH + overtimePad*2}
		im.fillRectFast(r, colorOvertime)
		atl.msg.drawString(im, r.X+overtimePad, r.Y+overtimePad, badge, colorOvertimeFg)
		addSite(im, "overtime", r)
	}

	// Giant clock; digits are interned Slots (no band site — the slot
	// set in Diff already covers count changes and moves).
	s, slots, atlas := ClockLayout(v)
	for i, slot := range slots {
		if g, ok := atlas.Glyph(rune(s[i])); ok {
			// drawGlyph places the ink via the glyph's cell-relative
			// offset from the slot origin.
			drawGlyph(im, atlas, g, slot.Rect.X, slot.Rect.Y, digitColor(v))
		}
	}
	im.Slots = slots

	// NEXT block (sites "next1", "next2") above the progress bar.
	if line1 := atl.ui.filterBaked(nextLine("NEXT", v.Next)); line1 != "" {
		atl.ui.drawString(im, nextX, next1Top, line1, colorDigit)
		addSite(im, "next1", Rect{nextX, next1Top, atl.ui.Width(len(line1)), atl.ui.CellH})
	}
	if line2 := atl.ui.filterBaked(v.NextSpeaker); line2 != "" {
		atl.ui.drawString(im, nextX, next2Top, line2, colorMeta)
		addSite(im, "next2", Rect{nextX, next2Top, atl.ui.Width(len(line2)), atl.ui.CellH})
	}

	// Progress bar (site "bar"; the fill is its own site so per-frame
	// progress movement damages only the fill rect).
	railW := LayoutW - barMargin*2
	im.fillRectFast(Rect{barMargin, barY, railW, barH}, colorRail)
	addSite(im, "bar", Rect{barMargin, barY, railW, barH})

	fillW := int(float64(railW) * clamp01(v.Progress))
	if fillW > 0 {
		fill := Rect{barMargin, barY, fillW, barH}
		im.fillRectFast(fill, colorPurple)
		addSite(im, "barfill", fill)
	}

	return im
}

func clamp01(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 1 {
		return 1
	}
	return p
}

// nextLine composes the NEXT label line: "NEXT LABEL".
func nextLine(prefix, label string) string {
	if label == "" {
		return ""
	}
	return prefix + " " + label
}

// addSite appends a named site (Diff matches sites by name+position).
func addSite(im *Image, name string, r Rect) {
	if r.Empty() {
		return
	}
	im.Sites = append(im.Sites, Site{Name: name, Rect: r})
}

// Diff computes the damage rects between two rendered frames.
//
// Rules:
//  1. nil or differently-sized images → one full-frame rect.
//  2. Interned slots (giant clock digit cells): slot rects equal AND
//     slot keys equal → zero damage (interning contract: the glyph
//     cells never overlap other pixels and rendering is deterministic).
//     Otherwise the slot's rect is the damage.
//  3. Sites (every other painted component): same-name site pairs are
//     pixel-compared; equal → zero damage, else the site's rect.
//     A site in only one frame is damage in full.
//
// The result is merged and within frame bounds; callers copy exactly
// these rects into the scan buffer.
func Diff(prev, next *Image) []Rect {
	if prev == nil || next == nil ||
		prev.W != next.W || prev.H != next.H {
		if next != nil {
			return []Rect{next.Rect()}
		}
		return []Rect{{0, 0, LayoutW, LayoutH}}
	}

	full := next.Rect()
	var dirty []Rect

	// Slots in order; equal-count renders intern 1:1.
	if len(prev.Slots) == len(next.Slots) {
		for i := range next.Slots {
			a, b := prev.Slots[i], next.Slots[i]
			if a.Rect == b.Rect && a.Key == b.Key {
				continue // interned: identical glyph content
			}
			if a.Rect != b.Rect {
				dirty = append(dirty, a.Rect.Clip(full), b.Rect.Clip(full))
				continue
			}
			if !pixelsEqual(prev, next, b.Rect) {
				dirty = append(dirty, b.Rect.Clip(full))
			}
		}
	} else {
		// Digit-count change (e.g. 9:59 → 10:00): whole band redraws.
		var band Rect
		for _, s := range prev.Slots {
			band = band.Union(s.Rect)
		}
		for _, s := range next.Slots {
			band = band.Union(s.Rect)
		}
		if !band.Empty() {
			dirty = append(dirty, band.Clip(full))
		}
	}

	// Sites, matched by NAME (render order is stable, but position and
	// count vary with optional components like the message strip).
	prevSites := make(map[string]Rect, len(prev.Sites))
	for _, s := range prev.Sites {
		prevSites[s.Name] = s.Rect
	}
	nextSites := make(map[string]Rect, len(next.Sites))
	for _, s := range next.Sites {
		nextSites[s.Name] = s.Rect
	}
	for _, s := range next.Sites {
		pr, ok := prevSites[s.Name]
		if !ok {
			dirty = append(dirty, s.Rect.Clip(full)) // site appeared
			continue
		}
		if pr == s.Rect {
			if !pixelsEqual(prev, next, s.Rect) {
				dirty = append(dirty, s.Rect.Clip(full))
			}
			continue
		}
		dirty = append(dirty, pr.Clip(full), s.Rect.Clip(full)) // site moved/resized
	}
	for _, s := range prev.Sites {
		if _, ok := nextSites[s.Name]; !ok {
			dirty = append(dirty, s.Rect.Clip(full)) // site vanished
		}
	}

	dirty = mergeRects(clipAll(dirty, full))
	return dirty
}

func clipAll(rs []Rect, bounds Rect) []Rect {
	out := make([]Rect, 0, len(rs))
	for _, r := range rs {
		r = r.Clip(bounds)
		if !r.Empty() {
			out = append(out, r)
		}
	}
	return out
}
