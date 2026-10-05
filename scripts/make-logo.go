//go:build ignore

// make-logo generates TimerPi's brand image assets:
//
//	go run scripts/make-logo.go
//
// It writes public/img/timerpi-192.png and public/img/timerpi-512.png —
// dark rounded squares with the TimerPi clock mark: purple (#7C3AED)
// primary ring + green (#22C55E) secondary hand (green appears ONLY in the
// logo, per PROTOCOL.md §Branding), used for favicon/PWA icons and (later)
// the framebuffer splash. The SVG wordmark, public/img/timerpi.svg, is
// hand-written (see that file).
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

var (
	bg     = color.RGBA{0x14, 0x16, 0x1a, 0xff} // #14161a panel (dark neutral)
	face   = color.RGBA{0x1e, 0x21, 0x26, 0xff} // slightly raised dial
	purple = color.RGBA{0x7c, 0x3a, 0xed, 0xff} // #7C3AED primary purple
	green  = color.RGBA{0x22, 0xc5, 0x5e, 0xff} // #22C55E logo accent (logo only)
	ink    = color.RGBA{0xe8, 0xe6, 0xe3, 0xff} // warm off-white
)

// aa coverage of pixel center (x,y) inside circle cx,cy,r.
func circleCoverage(x, y, cx, cy, r float64) float64 {
	d := math.Hypot(x-cx, y-cy)
	return clamp((r+0.5)-d, 0, 1)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func lerp(a, b color.RGBA, t float64) color.RGBA {
	mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a)*(1-t) + float64(b)*t)) }
	return color.RGBA{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B), mix(a.A, b.A)}
}

func draw(img *image.RGBA, s int) {
	px := func(x, y int, c color.RGBA) { img.SetRGBA(x, y, c) }

	r := float64(s) / 2
	corner := float64(s) * 0.22

	// Panel: rounded square via per-pixel coverage of the 4 quarter circles.
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			dx := math.Max(math.Abs(fx-r)-(r-corner), 0)
			dy := math.Max(math.Abs(fy-r)-(r-corner), 0)
			d := math.Hypot(dx, dy) - corner
			cov := clamp(-d+0.5, 0, 1)
			if cov > 0 {
				px(x, y, lerp(color.RGBA{0, 0, 0, 0}, bg, cov))
			}
		}
	}

	// Dial: ring outline + faint face.
	ringR := r * 0.66
	ringW := float64(s) * 0.055
	for y := 0; y < s; y++ {
		for x := 0; x < s; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			d := math.Hypot(fx-r, fy-r)
			if d < ringR*0.94 {
				px(x, y, lerp(px2v(img, x, y), face, clamp(ringR*0.94-d, 0, 1)))
			}
			if cov := clamp(ringW+0.5-math.Abs(d-ringR), 0, 1); cov > 0 {
				px(x, y, lerp(px2v(img, x, y), purple, cov))
			}
		}
	}

	// Hands: hour hand towards 10 (ink), minute hand towards 2 (green —
	// the logo's only secondary-green accent), centre hub — roughly 10:10
	// for a friendly idle face.
	line(img, r, r, r-ringR*0.42, r-ringR*0.50, ringW*0.8, ink)
	line(img, r, r, r+ringR*0.38, r-ringR*0.62, ringW*0.7, green)
	hub := ringW * 0.9
	for y := int(r - hub - 1); y <= int(r+hub+1); y++ {
		for x := int(r - hub - 1); x <= int(r+hub+1); x++ {
			if cov := circleCoverage(float64(x)+0.5, float64(y)+0.5, r, r, hub); cov > 0 {
				px(x, y, lerp(px2v(img, x, y), ink, cov))
			}
		}
	}
}

// px2v reads a pixel back as RGBA (for compositing our own layers).
func px2v(img *image.RGBA, x, y int) color.RGBA {
	return img.RGBAAt(x, y)
}

// line draws an anti-aliased capsule (segment with round caps) from
// (cx,cy) to (tx,ty).
func line(img *image.RGBA, cx, cy, tx, ty, w float64, c color.RGBA) {
	s := img.Bounds().Dx()
	pad := int(w + 2)
	y0, y1 := int(math.Min(ty, cy))-pad, int(math.Max(ty, cy))+pad
	x0, x1 := int(math.Min(tx, cx))-pad, int(math.Max(tx, cx))+pad
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if x < 0 || y < 0 || x >= s || y >= s {
				continue
			}
			fx, fy := float64(x)+0.5, float64(y)+0.5
			// distance from segment (cx,cy)-(tx,ty)
			vx, vy := tx-cx, ty-cy
			l2 := vx*vx + vy*vy
			t := clamp(((fx-cx)*vx+(fy-cy)*vy)/l2, 0, 1)
			d := math.Hypot(fx-(cx+t*vx), fy-(cy+t*vy))
			if cov := clamp(w+0.5-d, 0, 1); cov > 0 {
				img.SetRGBA(x, y, lerp(px2v(img, x, y), c, cov))
			}
		}
	}
}

func main() {
	for _, size := range []int{192, 512} {
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		draw(img, size)
		out := filepath.Join("public", "img", "timerpi-"+itoa(size)+".png")
		f, err := os.Create(out)
		if err != nil {
			println("create:", err.Error())
			os.Exit(1)
		}
		if err := png.Encode(f, img); err != nil {
			println("encode:", err.Error())
			os.Exit(1)
		}
		f.Close()
		println("wrote", out)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
