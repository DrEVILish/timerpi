// font.go loads the baked Share Tech Mono atlases (drm/assets/*.png +
// metrics.json, produced once by scripts/fontgen) and blits glyph
// coverage onto the ARGB image. The runtime never needs a font
// library: everything below is PNG decode + fixed-grid math.
//
// Atlas layout (see scripts/fontgen/main.go for the generator):
// one row of cells, cellW = widest advance + breathing room, cellH =
// measured ink extents + headroom, with the baseline row recorded in
// the metrics — NOT derived from font metrics, which under-bound the
// digit ink for this face. Per-glyph entries carry the advance, the
// cell index, and the ink box relative to the cell.
package drm

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"strings"
)

//go:embed assets/metrics.json
var metricsJSON []byte

//go:embed assets/atlas-clock-410.png
var atlasClock410 []byte

//go:embed assets/atlas-clock-320.png
var atlasClock320 []byte

//go:embed assets/atlas-ui-64.png
var atlasUI []byte

//go:embed assets/atlas-msg-160.png
var atlasMsg []byte

//go:embed assets/timerpi-192.png
var logoPNG []byte

type inkBox struct {
	X, Y, W, H int
}

type glyphJSON struct {
	C    rune   `json:"c"`
	Adv  int    `json:"adv"`
	Cell int    `json:"cell"`
	Ink  inkBox `json:"ink"`
}

type atlasJSON struct {
	Name     string      `json:"name"`
	EM       int         `json:"em"`
	PNG      string      `json:"png"`
	CellW    int         `json:"cellW"`
	CellH    int         `json:"cellH"`
	Baseline int         `json:"baseline"`
	Glyphs   []glyphJSON `json:"glyphs"`
}

// Glyph is one baked character: where its ink lives in the atlas
// coverage (absolute atlas bounds) and where it offsets from a slot
// cell's origin when drawn (InkMin minus the cell origin).
type Glyph struct {
	Rune       rune
	Adv        int
	Ink        image.Rectangle // absolute bounds within the atlas coverage
	OffX, OffY int             // cell-relative draw offset (slot origin + this + ink!)
}

// Atlas is one baked bitmap font. Draw places whole glyph cells on an
// Image and blends ink coverage.
type Atlas struct {
	Name     string
	EM       int
	CellW    int
	CellH    int
	Baseline int // baseline row offset from the cell top

	glyphs map[rune]Glyph
	cov    *image.Gray // atlas coverage (gray levels)
}

// loads decodes the metrics and coverage into runtime Atlases.
func loads(metrics []byte, pngs map[string][]byte) (map[string]*Atlas, error) {
	var parsed []atlasJSON
	if err := json.Unmarshal(metrics, &parsed); err != nil {
		return nil, fmt.Errorf("drm: parsing metrics.json: %w", err)
	}
	out := make(map[string]*Atlas, len(parsed))
	for i, aj := range parsed {
		if aj.Name == "" || aj.CellW <= 0 || aj.CellH <= 0 {
			return nil, fmt.Errorf("drm: atlas %d: missing name or geometry", i)
		}
		if aj.Baseline <= 0 || aj.Baseline >= aj.CellH {
			return nil, fmt.Errorf("drm: atlas %s@%d: baseline %d out of cellH %d",
				aj.Name, aj.EM, aj.Baseline, aj.CellH)
		}
		raw, ok := pngs[aj.PNG]
		if !ok {
			return nil, fmt.Errorf("drm: atlas %s@%d: PNG %q not provided", aj.Name, aj.EM, aj.PNG)
		}
		img, err := png.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("drm: decoding atlas %s: %w", aj.PNG, err)
		}
		gray, ok := img.(*image.Gray)
		if !ok {
			return nil, fmt.Errorf("drm: atlas %s: expected 8-bit gray coverage PNG", aj.PNG)
		}
		a := &Atlas{
			Name: aj.Name, EM: aj.EM,
			CellW: aj.CellW, CellH: aj.CellH, Baseline: aj.Baseline,
			glyphs: make(map[rune]Glyph, len(aj.Glyphs)),
			cov:    gray,
		}
		want := aj.CellW * len(aj.Glyphs)
		if want != gray.Bounds().Dx() || aj.CellH != gray.Bounds().Dy() {
			return nil, fmt.Errorf(
				"drm: atlas %s@%d: coverage %dx%d does not match declared grid %dx%d (%d glyphs)",
				aj.Name, aj.EM, gray.Bounds().Dx(), gray.Bounds().Dy(), want, aj.CellH, len(aj.Glyphs))
		}
		cells := make(map[int]bool, len(aj.Glyphs))
		for _, gj := range aj.Glyphs {
			if gj.Cell < 0 || gj.Cell >= len(aj.Glyphs) {
				return nil, fmt.Errorf("drm: atlas %s@%d: glyph %q cell %d out of row",
					aj.Name, aj.EM, string(gj.C), gj.Cell)
			}
			if cells[gj.Cell] {
				return nil, fmt.Errorf("drm: atlas %s@%d: duplicate cell %d", aj.Name, aj.EM, gj.Cell)
			}
			cells[gj.Cell] = true
			// Ink must sit inside the cell.
			if gj.Ink.W > 0 && (gj.Ink.X < 0 || gj.Ink.Y < 0 ||
				gj.Ink.X+gj.Ink.W > aj.CellW || gj.Ink.Y+gj.Ink.H > aj.CellH) {
				return nil, fmt.Errorf(
					"drm: atlas %s@%d: glyph %q ink %v escapes cell %dx%d",
					aj.Name, aj.EM, string(gj.C), gj.Ink, aj.CellW, aj.CellH)
			}
			a.glyphs[gj.C] = Glyph{
				Rune: gj.C,
				Adv:  gj.Adv,
				// metrics ink boxes are cell-relative; shift to the
				// glyph's own cell in the atlas row.
				Ink: image.Rectangle{
					Min: image.Point{X: gj.Cell*aj.CellW + gj.Ink.X, Y: gj.Ink.Y},
					Max: image.Point{X: gj.Cell*aj.CellW + gj.Ink.X + gj.Ink.W, Y: gj.Ink.Y + gj.Ink.H},
				},
				OffX: gj.Ink.X,
				OffY: gj.Ink.Y,
			}
		}
		out[key(aj.Name, aj.EM)] = a
	}
	return out, nil
}

func key(name string, em int) string { return fmt.Sprintf("%s-%d", name, em) }

// Fonts is the loaded asset set: the four atlases plus the brand logo.
type Fonts struct {
	Atlases map[string]*Atlas
	Logo    image.Image // RGBA brand mark (public/img/timerpi-192.png snapshot)
}

// LoadFonts parses the embedded assets. Call once (the face keeps the
// result in package state).
func LoadFonts() (*Fonts, error) {
	atl, err := loads(metricsJSON, map[string][]byte{
		"atlas-clock-410.png": atlasClock410,
		"atlas-clock-320.png": atlasClock320,
		"atlas-ui-64.png":     atlasUI,
		"atlas-msg-160.png":   atlasMsg,
	})
	if err != nil {
		return nil, err
	}
	f := &Fonts{Atlases: atl}
	if len(logoPNG) > 0 {
		img, err := png.Decode(bytes.NewReader(logoPNG))
		if err != nil {
			return nil, fmt.Errorf("drm: decoding logo: %w", err)
		}
		f.Logo = img
	}
	return f, nil
}

// Glyph returns the baked glyph for r, or ok=false when the atlas does
// not cover it (callers skip — never crash — on uncovered runes).
func (a *Atlas) Glyph(r rune) (Glyph, bool) {
	g, ok := a.glyphs[r]
	return g, ok
}

// Glyphs lists the covered runes (metrics-driven tests iterate this).
func (a *Atlas) Glyphs() []Glyph {
	out := make([]Glyph, 0, len(a.glyphs))
	for _, g := range a.glyphs {
		out = append(out, g)
	}
	return out
}

// Width returns the pen width of n cells of this atlas (all renders
// advance by full cells so digit slots stay internable).
func (a *Atlas) Width(n int) int { return n * a.CellW }

// filterBaked upper-cases s and strips every rune the atlas doesn't
// bake (Share Tech Mono caps + digits + punctuation only).
func (a *Atlas) filterBaked(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if _, ok := a.glyphs[r]; ok {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// drawString renders s into dst, advancing one full cell per rune
// (monospaced slot layout), unconditional of per-glyph advances — the
// clock slots rely on this identity. Returns the drawn width.
func (a *Atlas) drawString(dst *Image, x, y int, s string, color uint32) int {
	for i, r := range s {
		g, ok := a.glyphs[r]
		if !ok {
			continue
		}
		drawGlyph(dst, a, g, x+i*a.CellW, y, color)
	}
	return len(s) * a.CellW
}

// drawGlyph blits one glyph onto dst at slot origin (x, y): the ink's
// device position is the origin + the cell-relative offset, while the
// coverage is read at the glyph's absolute atlas bounds.
func drawGlyph(dst *Image, a *Atlas, g Glyph, x, y int, color uint32) {
	dx := x + g.OffX
	dy := y + g.OffY
	ix0, iy0 := g.Ink.Min.X, g.Ink.Min.Y
	ix1, iy1 := g.Ink.Max.X, g.Ink.Max.Y
	for sy := iy0; sy < iy1; sy++ {
		dy2 := dy + sy - iy0
		if dy2 < 0 || dy2 >= dst.H {
			continue
		}
		srcRow := a.cov.Pix[sy*a.cov.Stride:]
		rowOff := dy2 * dst.Stride
		for sx := ix0; sx < ix1; sx++ {
			c := srcRow[sx]
			if c == 0 {
				continue
			}
			dCol := dx + sx - ix0
			if dCol < 0 || dCol >= dst.W {
				continue
			}
			blendPixelCoverage(dst.Pix[rowOff+dCol*4:], c, color)
		}
	}
}

// blendPixelCoverage mixes coverage c (0..255, alpha-style gray) of
// color into the dst pixel (B,G,R,A byte lanes, little-endian).
func blendPixelCoverage(dst []byte, c uint8, color uint32) {
	fgR := byte(color >> 16)
	fgG := byte(color >> 8)
	fgB := byte(color)
	var outR, outG, outB uint8
	if c == 0xff {
		outR, outG, outB = fgR, fgG, fgB
	} else {
		bgB, bgG, bgR := dst[0], dst[1], dst[2]
		inv := 255 - int(c)
		outR = uint8((int(bgR)*inv + int(fgR)*int(c) + 127) / 255)
		outG = uint8((int(bgG)*inv + int(fgG)*int(c) + 127) / 255)
		outB = uint8((int(bgB)*inv + int(fgB)*int(c) + 127) / 255)
	}
	dst[2] = outR
	dst[1] = outG
	dst[0] = outB
	dst[3] = 0xff
}

// drawLogo blits the brand image at rect r with nearest-neighbor
// sampling, alpha-blended over the destination. The rect is the site
// Diff compares.
func drawLogo(dst *Image, src image.Image, r Rect) Rect {
	if src == nil || r.W <= 0 || r.H <= 0 {
		return Rect{}
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	for y := 0; y < r.H; y++ {
		sy := sb.Min.Y + min(sh-1, y*sh/r.H)
		for x := 0; x < r.W; x++ {
			sx := sb.Min.X + min(sw-1, x*sw/r.W)
			cr, cg, cb, ca := src.At(sx, sy).RGBA()
			blendLogoPixel(dst, r.X+x, r.Y+y, cr, cg, cb, uint8(ca>>8))
		}
	}
	return r
}

// blendLogoPixel mixes one RGBA source pixel into the destination:
// out = dst*(1-a) + src*a per channel.
func blendLogoPixel(dst *Image, x, y int, cr, cg, cb uint32, cov uint8) {
	if x < 0 || y < 0 || x >= dst.W || y >= dst.H || cov == 0 {
		return
	}
	off := y*dst.Stride + x*4
	bgB, bgG, bgR := dst.Pix[off], dst.Pix[off+1], dst.Pix[off+2]
	inv := 255 - int(cov)
	dst.Pix[off+2] = uint8((int(bgR)*inv + int(cr>>8)*int(cov) + 127) / 255)
	dst.Pix[off+1] = uint8((int(bgG)*inv + int(cg>>8)*int(cov) + 127) / 255)
	dst.Pix[off] = uint8((int(bgB)*inv + int(cb>>8)*int(cov) + 127) / 255)
	dst.Pix[off+3] = 0xff
}
