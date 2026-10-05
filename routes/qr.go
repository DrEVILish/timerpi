// package routes — qr.go: share/display QR PNG generation
// (skip2/go-qrcode, pinned via tools/deps/deps.go).
package routes

import (
	"github.com/skip2/go-qrcode"
)

// qrPNG renders data as a PNG of the given pixel size. go-qrcode emits a
// square PNG directly at the requested size (vector → raster internally).
func qrPNG(data string, size int) ([]byte, error) {
	q, err := qrcode.New(data, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	q.DisableBorder = false
	return q.PNG(size)
}
