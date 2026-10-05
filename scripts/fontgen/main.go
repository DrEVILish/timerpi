

// fontgen renders the Share Tech Mono bitmap atlases used by the TimerPi
// DRM/fb display renderer (drm/). Run it once, offline, and commit the PNG +
// metrics output; the runtime never needs a font library:
//
//	cd scripts/fontgen && ./run.sh
//
// Output lands in ../../drm/assets/ and is embedded into drm/font.go with
// go:embed. This directory is a *nested* Go module (its own go.mod) so its
// font dependencies can never leak into the TimerPi module requirements or
// the tools/deps/deps.go pin file.
//
// Atlas format: one cell per glyph packed in a single row. cellW = max glyph
// advance, cellH = font line height at that size, baseline row recorded in
// the metrics. metrics.json carries per-glyph advance and ink box (relative
// to the cell), which the runtime layout math is built on.
package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	fontPath = "ShareTechMono-Regular.ttf"
	outRel   = "../../drm/assets"
)

// Atlases to bake. "clock" is the massive main-time face (two ladder steps:
// the big one for normal times, the smaller one for long overtime /
// stopwatch strings); "ui" is small text (NEXT line, wall clock, speaker);
// "msg" is the message strip and OVERTIME badge size.
type spec struct {
	name  string
	em    int // pixel em size (Size at DPI 72 == px)
	chars string
}

var specs = []spec{
	{"clock", 410, "0123456789:-+ "},
	{"clock", 320, "0123456789:-+ "},
	{"ui", 64, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789%:.,-!?/ "},
	{"msg", 160, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789%:.,-!?/ "},
}

type inkBox struct {
	X, Y, W, H int
}

type glyph struct {
	C    rune   `json:"c"`           // codepoint
	Adv  int    `json:"adv"`         // advance width in px
	Cell int    `json:"cell"`        // cell index in the atlas row
	Ink  inkBox `json:"ink"`         // ink bbox relative to the cell (0,0 if blank)
}

type atlas struct {
	Name     string  `json:"name"`
	EM       int     `json:"em"`
	PNG      string  `json:"png"`
	CellW    int     `json:"cellW"`
	CellH    int     `json:"cellH"`
	Baseline int     `json:"baseline"` // baseline row offset from cell top
	Glyphs   []glyph `json:"glyphs"`
}

func main() {
	ttf, err := os.ReadFile(fontPath)
	if err != nil {
		fatal("missing %s: %v — copy the TTF from\n"+
			"  /opt/capacitimer/web-server/fonts/Share_Tech_Mono/ShareTechMono-Regular.ttf", fontPath, err)
	}
	ttfFace, err := opentype.Parse(ttf)
	if err != nil {
		fatal("parsing TTF: %v", err)
	}

	if err := os.MkdirAll(outRel, 0o755); err != nil {
		fatal("mkdir %s: %v", outRel, err)
	}

	var baked []atlas
	for _, s := range specs {
		face, err := opentype.NewFace(ttfFace, &opentype.FaceOptions{
			Size:    float64(s.em),
			DPI:     72, // so Size lands in pixels
			Hinting: font.HintingFull,
		})
		if err != nil {
			fatal("face %v: %v", s.em, err)
		}
		// Note: face.Metrics() ascent/descent under-bounds the digit ink
		// for this font at some sizes (Share Tech Mono draws digits taller
		// than its metrics). The atlas geometry is therefore measured
		// empirically from the glyphs themselves below; do not trust
		// metrics for cell math.

		type measured struct {
			run  rune
			adv  int
			bits []byte // ink-only coverage, rowstride w
			w, h int
			// ink offset relative to the pen origin: dx (x) and dy from the
			// baseline row (negative = above the baseline).
			dx, dy int
			blank  bool
		}

		margin := s.em
		bigW, bigH := margin*4, margin*3

		var ms []measured
		for _, run := range s.chars {
			img := image.NewAlpha(image.Rect(0, 0, bigW, bigH))
			d := &font.Drawer{
				Dst:  img,
				Src:  image.NewUniform(color.Gray{0xff}),
				Face: face,
			}
			d.Dot = fixed.P(margin, margin) // pen origin (x, baseline y)
			before := d.Dot.X
			d.DrawString(string(run))
			advance := int((d.Dot.X - before) >> 6)

			// Ink bounds over the whole canvas.
			inkX0, inkY0, inkX1, inkY1 := bigW, bigH, 0, 0
			for y := 0; y < bigH; y++ {
				for x := 0; x < bigW; x++ {
					if img.AlphaAt(x, y).A != 0 {
						if x < inkX0 {
							inkX0 = x
						}
						if y < inkY0 {
							inkY0 = y
						}
						if x >= inkX1 {
							inkX1 = x + 1
						}
						if y >= inkY1 {
							inkY1 = y + 1
						}
					}
				}
			}
			if inkX1 <= inkX0 || inkY1 <= inkY0 { // blank glyph (space)
				ms = append(ms, measured{run: run, adv: advance, blank: true})
				continue
			}
			w, h := inkX1-inkX0, inkY1-inkY0
			bits := make([]byte, w*h)
			for y := 0; y < h; y++ {
				dst := bits[y*w : (y+1)*w]
				src := (y + inkY0) * bigW
				for x := 0; x < w; x++ {
					dst[x] = img.Pix[src+x+inkX0]
				}
			}
			ms = append(ms, measured{
				run: run, adv: advance, bits: bits, w: w, h: h,
				dx: inkX0 - margin,
				dy: inkY0 - margin, // relative to the baseline row
			})
			fmt.Printf("  %q adv=%-3d ink=%dx%d dy=%d\n", string(run), advance, w, h, inkY0-margin)
		}

		// Compose the atlas row: cell origin (0,0) top-left. Baseline and
		// cell height come from the measured ink extents (plus headroom),
		// not from font metrics (which under-bound at some sizes).
		cellW := 0
		maxAbove, maxBelow := 0, 0
		for _, m := range ms {
			if m.blank {
				continue
			}
			if m.adv > cellW {
				cellW = m.adv
			}
			if -m.dy > maxAbove {
				maxAbove = -m.dy
			}
			if m.dy+m.h > maxBelow {
				maxBelow = m.dy + m.h
			}
		}
		baseline := maxAbove + 2
		cellH := baseline + maxBelow + 2
		cellW += 2 // 1px breathing room at each side of the widest glyph

		img := image.NewAlpha(image.Rect(0, 0, cellW*len(ms), cellH))

		at := atlas{
			Name: s.name, EM: s.em,
			CellW: cellW, CellH: cellH, Baseline: baseline,
		}
		for i, m := range ms {
			g := glyph{C: m.run, Adv: m.adv, Cell: i}
			if !m.blank {
				// Paste the ink INSIDE THE GLYPH'S OWN CELL: cell i
				// occupies columns i*cellW..(i+1)*cellW; the ink's x
				// offset within the cell (pen-relative, +1 breathing
				// column) is kept, and recorded cell-relative so the
				// runtime can address it as slot.X + ink.X.
				// (Cell-relative origin left = m.dx + 1.)
				left := i*cellW + m.dx + 1
				if left < 0 {
					left = 0
				}
				top := baseline + m.dy
				if top < 0 {
					top = 0
				}
				if left+m.w > (i+1)*cellW {
					fatal("%s em=%d glyph %q ink overflows its cell (left=%d w=%d cellW=%d)",
						s.name, s.em, string(m.run), left-i*cellW, m.w, cellW)
				}
				if top+m.h > cellH {
					fatal("%s em=%d glyph %q ink overflows cellH (top=%d h=%d cellH=%d)",
						s.name, s.em, string(m.run), top, m.h, cellH)
				}
				for y := 0; y < m.h; y++ {
					dst := img.Pix[(top+y)*img.Stride+left:]
					copy(dst[:m.w], m.bits[y*m.w:(y+1)*m.w])
				}
				g.Ink = inkBox{X: left - i*cellW, Y: top, W: m.w, H: m.h} // cell-relative
			}
			at.Glyphs = append(at.Glyphs, g)
		}

		pngName := fmt.Sprintf("atlas-%s-%d.png", s.name, s.em)
		pngPath := filepath.Join(outRel, pngName)
		at.PNG = pngName
		if err := saveAlphaPNG(pngPath, img); err != nil {
			fatal("writing %s: %v", pngPath, err)
		}
		fmt.Printf("baked %-6s em=%-3d cell=%dx%d baseline=%d glyphs=%d -> %s\n",
			s.name, s.em, cellW, cellH, baseline, len(at.Glyphs), pngPath)
		baked = append(baked, at)
	}

	mj, err := json.MarshalIndent(baked, "", "  ")
	if err != nil {
		fatal("json: %v", err)
	}
	mj = append(mj, '\n')
	mp := filepath.Join(outRel, "metrics.json")
	if err := os.WriteFile(mp, mj, 0o644); err != nil {
		fatal("write %s: %v", mp, err)
	}
	fmt.Printf("metrics -> %s\n", mp)
}

// saveAlphaPNG writes the coverage as an 8-bit grayscale PNG.
func saveAlphaPNG(path string, img *image.Alpha) error {
	gray := image.NewGray(img.Bounds())
	draw.Draw(gray, gray.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			gray.SetGray(x, y, color.Gray{Y: img.AlphaAt(x, y).A})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, gray)
}

func fatal(f string, a ...any) {
	fmt.Printf("fontgen: ERROR "+f+"\n", a...)
	os.Exit(1)
}
