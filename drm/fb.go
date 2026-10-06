//go:build linux

// fb.go is the /dev/fb0 fallback backend: one mmap'd framebuffer, no
// flip — damage rects are written straight into scan-out. The ioctls
// and struct mirrors follow scripts/splash/main.go's verified packing
// math (FBIOGET_VSCREENINFO/FIXSCREENINFO + fb_var/fix bitfields, so
// the device's pixel layout decides the bytes); the renderer's ARGB
// frame is first translated per-channel to whatever the fb advertises.
//
// On the target Pi the boot cmdline `video=HDMI-A-1:1920x1080@50e`
// gives fb0 exactly 1920×1080@50 XRGB; this backend then behaves as a
// no-flip damage writer (no vblank pacing — it's the fallback only).
package drm

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// fbdev ioctls: fixed arch-level uapi numbers (mirrored from
// scripts/splash/main.go, which documents the source of truth).
const (
	fbIoGetVScreenInfo = 0x4600 // FBIOGET_VSCREENINFO
	fbIoGetFScreenInfo = 0x4602 // FBIOGET_FSCREENINFO
	fbIoPanDisplay     = 0x4606 // FBIOPAN_DISPLAY
	fbIoBlank          = 0x4611 // FBIOBLANK

	// vesa.h blank level, applied once at Open.
	fbBlankUnblank = 4 // FB_BLANK_UNBLANK
)

// fbBitfield mirrors struct fb_bitfield (uapi/linux/fb.h).
type fbBitfield struct{ Offset, Length, MsbRight uint32 }

// fbVarScreeninfo mirrors struct fb_var_screeninfo (160 bytes:
// all-u32 fields, identical packing on 64-bit).
type fbVarScreeninfo struct {
	Xres, Yres, XresVirtual, YresVirtual        uint32
	Xoffset, Yoffset                            uint32
	BitsPerPixel, Grayscale                     uint32
	Red, Green, Blue, Transp                    fbBitfield
	Nonstd, Activate, Height, Width, AccelFlags uint32
	Pixclock, LeftMargin, RightMargin           uint32
	UpperMargin, LowerMargin                    uint32
	HsyncLen, VsyncLen, Sync, Vmode, Rotate     uint32
	Colorspace                                  uint32
	Reserved                                    [4]uint32
}

// fbFixScreeninfo mirrors struct fb_fix_screeninfo (80 bytes: two
// unsigned long members force 8-byte alignment exactly as here).
type fbFixScreeninfo struct {
	ID           [16]byte
	SmemStart    uint64
	SmemLen      uint32
	Type         uint32
	TypeAux      uint32
	Visual       uint32
	XPanStep     uint16
	YPanStep     uint16
	YWrapStep    uint16
	LineLength   uint32
	MmioStart    uint64
	MmioLen      uint32
	Accel        uint32
	Capabilities uint16
	Reserved     [2]uint16
}

// Backends report their drawing scale via the FB env; device fallback
// keeps splash's FBDEV naming.
func fbDevice() string {
	if p := os.Getenv("TIMERPI_FBDEV"); p != "" {
		return p
	}
	if p := os.Getenv("FBDEV"); p != "" {
		return p
	}
	return "/dev/fb0"
}

// FBBackend presents on the Linux framebuffer device.
type FBBackend struct {
	file   *os.File
	data   []byte // mmap'd SmemStart..+SmemLen
	v      fbVarScreeninfo
	fix    fbFixScreeninfo
	w, h   int
	bpp    int
	stride int
}

// ioctlValue issues an ioctl whose argument is a plain integer value
// (FBIOBLANK takes the blank level directly, not a pointer) — same
// shape as scripts/splash/main.go.
func ioctlValue(file *os.File, req uintptr, value uintptr) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, file.Fd(), req, value); errno != 0 {
		return errno
	}
	return nil
}

// NewFBBackend constructs an unopened fbdev backend.
func NewFBBackend() *FBBackend { return &FBBackend{} }

// Open reads the geometry, maps the memory, and unblanks once.
func (b *FBBackend) Open() error {
	dev := fbDevice()
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("fb: opening %s: %w", dev, err)
	}
	b.file = f
	if err := ioctlPtr(f, fbIoGetVScreenInfo, unsafe.Pointer(&b.v)); err != nil {
		return fbFail(f, dev, fmt.Sprintf("FBIOGET_VSCREENINFO: %v", err))
	}
	if err := ioctlPtr(f, fbIoGetFScreenInfo, unsafe.Pointer(&b.fix)); err != nil {
		return fbFail(f, dev, fmt.Sprintf("FBIOGET_FSCREENINFO: %v", err))
	}
	b.bpp = int(b.v.BitsPerPixel)
	b.stride = int(b.fix.LineLength)
	if b.stride == 0 {
		b.stride = int(b.v.XresVirtual) * b.bpp / 8
	}
	memLen := int(b.fix.SmemLen)
	if memLen < b.stride*int(b.v.YresVirtual) {
		memLen = b.stride * int(b.v.YresVirtual)
	}
	if b.bpp%8 != 0 || b.bpp > 32 || memLen <= 0 || b.stride <= 0 {
		return fbFail(f, dev, fmt.Sprintf("unusable geometry (bpp=%d smem=%d stride=%d)",
			b.bpp, b.fix.SmemLen, b.stride))
	}
	data, err := unix.Mmap(int(f.Fd()), 0, memLen, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fbFail(f, dev, fmt.Sprintf("mmap (%d bytes): %v", memLen, err))
	}
	b.data = data
	b.w, b.h = int(b.v.XresVirtual), int(b.v.YresVirtual)

	// One FB_BLANK(UNBLANK) — some drivers hold the panel dark until
	// told otherwise; harmless when unnecessary (splash mirrors this).
	_ = ioctlValue(f, fbIoBlank, fbBlankUnblank)
	return nil
}

func fbFail(f *os.File, dev, msg string) error {
	f.Close()
	return fmt.Errorf("fb: %s: %s", dev, msg)
}

// Size is the fbdev-visible area.
func (b *FBBackend) Size() (int, int) { return b.w, b.h }

// Present copies the damage rects, translating ARGB→fb packing via
// the var-screeninfo bitfields (splash math). Single-buffered: rows go
// to the panel as written; a fast panel shows them on the next scan.
func (b *FBBackend) Present(img *Image, dirty []Rect) error {
	if len(dirty) == 0 || b.data == nil || img == nil {
		return nil
	}
	bppBytes := b.bpp / 8
	if b.stride < b.w*bppBytes {
		return fmt.Errorf("fb: geometry is broken (stride %d < %d)", b.stride, b.w*bppBytes)
	}
	if img.Stride < img.W*4 || len(img.Pix) < img.Stride*img.H {
		return fmt.Errorf("fb: source frame is broken (%dx%d, stride %d, %d bytes)", img.W, img.H, img.Stride, len(img.Pix))
	}
	clip := Rect{0, 0, min(b.w, img.W), min(b.h, img.H)}
	scale := pixScaler(b.v)
	for _, r := range dirty {
		r = r.Clip(clip)
		if r.Empty() {
			continue
		}
		for dy := 0; dy < r.H; dy++ {
			src := img.Pix[(r.Y+dy)*img.Stride+r.X*4:]
			dst := b.data[(r.Y+dy)*b.stride+r.X*bppBytes:]
			for x := 0; x < r.W; x++ {
				off := x * 4
				px := scale(src[off+2], src[off+1], src[off])
				for i := 0; i < bppBytes && i < 4; i++ {
					dst[x*bppBytes+i] = byte(px >> (8 * i))
				}
			}
		}
	}
	return nil
}

// pixScaler produces a per-pixel repack function from the fb's
// channel bitfields (r,g,b are 8-bit; alpha forced on where present).
func pixScaler(v fbVarScreeninfo) func(r, g, bl uint8) uint32 {
	chanScale := func(val64 uint32, bits uint32) uint32 {
		switch {
		case bits == 0 || bits >= 8:
			return val64
		default:
			return val64 >> (8 - bits)
		}
	}
	return func(r, g, bl uint8) uint32 {
		px := chanScale(uint32(bl), v.Blue.Length)<<v.Blue.Offset |
			chanScale(uint32(g), v.Green.Length)<<v.Green.Offset |
			chanScale(uint32(r), v.Red.Length)<<v.Red.Offset
		if v.Transp.Length > 0 {
			px |= chanScale(0xff, v.Transp.Length) << v.Transp.Offset
		}
		return px
	}
}

// Close unmaps and closes; blank state is left as found.
func (b *FBBackend) Close() error {
	if b.file == nil {
		return nil
	}
	if b.data != nil {
		_ = unix.Munmap(b.data)
		b.data = nil
	}
	err := b.file.Close()
	b.file = nil
	return err
}
