//go:build linux

// Command splash-draw is the TimerPi framebuffer splash: it draws the
// brand PNG centered on /dev/fb0 at boot and holds it until the main
// appliance signals readiness, then fades out and releases the
// framebuffer.
//
// Usage:
//
//	splash-draw [-image PATH] [-fb DEV] [-ready PATH] [-timeout DUR]
//	            [-fork] [-clear]
//
// Flags/environment:
//
//	-image    PNG to show (default /opt/timerpi/public/img/timerpi-512.png)
//	-fb       framebuffer device (default /dev/fb0; FBDEV env overrides)
//	-ready    readiness file (default /run/timerpi/ready)
//	-timeout  wait cap (default 45s; -1 waits forever)
//	-fork     run detached (used by timerpi-splash.service, see below)
//	-clear    just black-fill + restore console blanking, then exit
//
// Exit codes: 0 whenever the framebuffer is absent or unusable (dev
// containers, headless boots) — the appliance keeps booting; 0 when the
// ready file shows up; 0 after the timeout cap. Only supervisor-level
// failures (detached spawn) exit non-zero.
//
// See scripts/splash/README.md for the ioctl contract and the ready-file
// handshake. Pure Go: PNG decode + raw framebuffer ioctls/mmap via
// golang.org/x/sys/unix — no X, no Wayland, no extra packages.
//
// Why the -fork dance: the unit uses Before=timerpi.service ordering with
// Type=oneshot. ExecStart=/opt/timerpi/bin/splash-draw -fork starts the
// real worker in a new session and returns immediately, so the oneshot is
// complete (RemainAfterExit keeps the unit active) and timerpi.service
// starts without waiting — the logo is already on screen.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Raw fbdev ioctl numbers from uapi/linux/fb.h. They are fixed arch-level
// constants; x/sys/unix v0.37 does not ship fbdev helpers yet, so these
// numbers (and the struct mirrors below) live here.
const (
	fbIoGetVScreenInfo = 0x4600 // FBIOGET_VSCREENINFO
	fbIoPutVScreenInfo = 0x4601 // FBIOPUT_VSCREENINFO
	fbIoGetFScreenInfo = 0x4602 // FBIOGET_FSCREENINFO
	fbIoPanDisplay     = 0x4606 // FBIOPAN_DISPLAY
	fbIoBlank          = 0x4611 // FBIOBLANK

	// vesa.h blank level used with FBIOBLANK.
	fbBlankUnblank = 4 // FB_BLANK_UNBLANK — display on, console timers resume
)

// fbBitfield mirrors struct fb_bitfield (uapi/linux/fb.h).
type fbBitfield struct {
	Offset, Length, MsbRight uint32
}

// fbVarScreeninfo mirrors struct fb_var_screeninfo (160 bytes on
// 64-bit Linux: every field is u32, so no implicit padding differs).
type fbVarScreeninfo struct {
	Xres, Yres, XresVirtual, YresVirtual uint32
	Xoffset, Yoffset                     uint32

	BitsPerPixel, Grayscale  uint32
	Red, Green, Blue, Transp fbBitfield

	Nonstd, Activate, Height, Width, AccelFlags uint32

	Pixclock, LeftMargin, RightMargin, UpperMargin, LowerMargin uint32
	HsyncLen, VsyncLen, Sync, Vmode, Rotate, Colorspace         uint32

	Reserved [4]uint32
}

// fbFixScreeninfo mirrors struct fb_fix_screeninfo (80 bytes on 64-bit
// Linux: the two unsigned long members force 8-byte alignment).
type fbFixScreeninfo struct {
	ID [16]byte

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

// Struct-size sanity (mirrors drift = silent framebuffer corruption).
func init() {
	const varSize, fixSize = 160, 80
	if got := unsafe.Sizeof(fbVarScreeninfo{}); got != varSize {
		log.Printf("splash: fb_var_screeninfo mirror is %d bytes, kernel expects %d — framebuffer geometry may misrender", got, varSize)
	}
	if got := unsafe.Sizeof(fbFixScreeninfo{}); got != fixSize {
		log.Printf("splash: fb_fix_screeninfo mirror is %d bytes, kernel expects %d — framebuffer geometry may misrender", got, fixSize)
	}
}

// Default paths (flags override; env FBDEV overrides only the -fb
// default).
const (
	defaultImage = "/opt/timerpi/public/img/timerpi-512.png"
	defaultReady = "/run/timerpi/ready"
	defaultFbDev = "/dev/fb0"
)

func main() {
	log.SetPrefix("splash: ")

	var (
		imgPath = flag.String("image", defaultImage, "PNG file to draw centered on the framebuffer")
		fbDev   = flag.String("fb", firstNonEmpty(os.Getenv("FBDEV"), defaultFbDev), "framebuffer device")
		ready   = flag.String("ready", defaultReady, "readiness file: appear → fade out, release, exit")
		timeout = flag.Duration("timeout", 45*time.Second, "wait cap for the ready file (-1 = forever)")
		fork    = flag.Bool("fork", false, "detach the worker and return (service entry point)")
		daemon  = flag.Bool("daemon", false, "internal: already running as the detached worker")
		clear   = flag.Bool("clear", false, "black-fill, restore blanking and exit")
	)
	flag.Parse()

	// -fork (the unit's entry point) re-launches this process detached
	// BEFORE any framebuffer work, so the oneshot completes immediately.
	if *fork {
		detachAndExit()
	}

	switch {
	case *clear:
		runClear(*fbDev)
	case *daemon: // normal path for the unit's worker
		run(*fbDev, *imgPath, *ready, *timeout)
	default:
		run(*fbDev, *imgPath, *ready, *timeout)
	}
}

// detachAndExit re-executes self with -daemon in a new session; the
// parent exits 0 right away (journal keeps inheriting stderr).
func detachAndExit() {
	self, err := os.Executable()
	if err != nil {
		log.Fatalf("splash: resolving executable: %v", err)
	}
	args := append(os.Args[1:], "-fork=false", "-daemon")
	cmd := exec.Command(self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("splash: detaching worker: %v", err)
	}
	log.Printf("splash: worker detached (pid %d)", cmd.Process.Pid)
	os.Exit(0)
}

// openFB reads geometry via the fbdev ioctls and mmaps the screen.
func openFB(dev string) (*fbdev, error) {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", dev, err)
	}
	var (
		v   fbVarScreeninfo
		fix fbFixScreeninfo
	)
	if err := ioctlPtr(int(f.Fd()), fbIoGetVScreenInfo, unsafe.Pointer(&v)); err != nil {
		f.Close()
		return nil, fmt.Errorf("FBIOGET_VSCREENINFO on %s: %w", dev, err)
	}
	if err := ioctlPtr(int(f.Fd()), fbIoGetFScreenInfo, unsafe.Pointer(&fix)); err != nil {
		f.Close()
		return nil, fmt.Errorf("FBIOGET_FSCREENINFO on %s: %w", dev, err)
	}

	bpp := int(v.BitsPerPixel)
	stride := int(fix.LineLength)
	if stride == 0 {
		stride = int(v.XresVirtual) * bpp / 8
	}
	memLen := int(fix.SmemLen)
	if memLen < stride*int(v.YresVirtual) {
		memLen = stride * int(v.YresVirtual)
	}
	if memLen <= 0 || bpp%8 != 0 || bpp > 32 {
		f.Close()
		return nil, fmt.Errorf("%s: unusable geometry (bpp=%d smem=%d stride=%d)", dev, bpp, fix.SmemLen, stride)
	}
	data, err := unix.Mmap(int(f.Fd()), 0, memLen, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap %s (%d bytes): %w", dev, memLen, err)
	}
	return &fbdev{file: f, data: data, v: v, fix: fix, bytesPerPixel: bpp / 8, stride: stride}, nil
}

func ioctlPtr(fd int, req uintptr, arg unsafe.Pointer) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}

// ioctlValue issues an ioctl whose argument is a plain integer value
// (FBIOBLANK takes the blank level directly, not a pointer).
func ioctlValue(fd int, req uintptr, value uintptr) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, value); errno != 0 {
		return errno
	}
	return nil
}

// fbdev owns the mapping and the drawing primitives.
type fbdev struct {
	file          *os.File
	data          []byte
	v             fbVarScreeninfo
	fix           fbFixScreeninfo
	bytesPerPixel int
	stride        int
}

// w/h return the drawable virtual area.
func (b *fbdev) w() int { return int(b.v.XresVirtual) }
func (b *fbdev) h() int { return int(b.v.YresVirtual) }

// fillBlack zeroes every visible line (covers the boot console text).
func (b *fbdev) fillBlack() {
	for y := 0; y < b.h(); y++ {
		off := y * b.stride
		for i := range b.data[off : off+b.stride] {
			b.data[off+i] = 0
		}
	}
}

// chanScale reduces an 8-bit value to the fb channel's bit length.
func chanScale(v uint8, bits uint32) uint32 {
	switch {
	case bits == 0 || bits >= 8:
		return uint32(v)
	default:
		return uint32(v >> (8 - bits))
	}
}

// pack assembles one fb pixel (little-endian bytes) from blended sRGB
// values. Channel placement follows the var-screeninfo bitfields, which
// is how the kernel driver exposes the current mode. The fb alpha plane
// (when present) is forced opaque.
func (b *fbdev) pack(r, g, bl uint8) []byte {
	px := chanScale(bl, b.v.Blue.Length)<<b.v.Blue.Offset |
		chanScale(g, b.v.Green.Length)<<b.v.Green.Offset |
		chanScale(r, b.v.Red.Length)<<b.v.Red.Offset
	if b.v.Transp.Length > 0 {
		px |= chanScale(0xff, b.v.Transp.Length) << b.v.Transp.Offset
	}
	raw := make([]byte, b.bytesPerPixel)
	for i := 0; i < b.bytesPerPixel && i < 4; i++ {
		raw[i] = byte(px >> (8 * i))
	}
	return raw
}

// pixel writes one already-packed pixel at (x, y).
func (b *fbdev) pixel(x, y int, raw []byte) {
	off := y*b.stride + x*b.bytesPerPixel
	copy(b.data[off:off+b.bytesPerPixel], raw)
}

// blendOverBlack folds the source alpha onto the black background the
// logo sits on: effective = source * alpha.
func blendOverBlack(r, g, b, a uint8) (uint8, uint8, uint8) {
	return uint8((uint16(r)*uint16(a) + 127) / 255),
		uint8((uint16(g)*uint16(a) + 127) / 255),
		uint8((uint16(b)*uint16(a) + 127) / 255)
}

// drawImage draws src centered at a given brightness (1.0 = full), with
// nearest-neighbor scaling when the source exceeds the screen size.
func (b *fbdev) drawImage(src image.Image, brightness float64) {
	screenW, screenH := b.w(), b.h()
	if screenW <= 0 || screenH <= 0 {
		return
	}
	bounds := src.Bounds()
	imgW, imgH := bounds.Dx(), bounds.Dy()

	scale := 1.0
	if imgW > screenW || imgH > screenH {
		scale = min(float64(screenW-1)/float64(imgW), float64(screenH-1)/float64(imgH))
	}
	drawW := max(1, int(float64(imgW)*scale))
	drawH := max(1, int(float64(imgH)*scale))
	ox := max(0, (screenW-drawW)/2)
	oy := max(0, (screenH-drawH)/2)

	for y := 0; y < drawH && oy+y < screenH; y++ {
		sy := min(bounds.Min.Y+int(float64(y)/scale), bounds.Max.Y-1)
		for x := 0; x < drawW && ox+x < screenW; x++ {
			sx := min(bounds.Min.X+int(float64(x)/scale), bounds.Max.X-1)
			cr, cg, cb, ca := src.At(sx, sy).RGBA() // 16-bit components
			r := uint8(cr >> 8)
			g := uint8(cg >> 8)
			bl := uint8(cb >> 8)
			r, g, bl = blendOverBlack(r, g, bl, uint8(ca>>8))
			if brightness < 1.0 {
				f := uint32(brightness * 255)
				r = uint8((uint16(r)*uint16(f) + 127) / 255)
				g = uint8((uint16(g)*uint16(f) + 127) / 255)
				bl = uint8((uint16(bl)*uint16(f) + 127) / 255)
			}
			b.pixel(ox+x, oy+y, b.pack(r, g, bl))
		}
	}
}

// pan flushes the visible state (some drivers only show new bytes after
// an explicit FBIOPAN_DISPLAY).
func (b *fbdev) pan() error {
	return ioctlPtr(int(b.file.Fd()), fbIoPanDisplay, unsafe.Pointer(&b.v))
}

// restoreBlank clears any active blank so the console blank timer keeps
// regulating normally (the "restore console blank state" contract step).
func (b *fbdev) restoreBlank() {
	if err := ioctlValue(int(b.file.Fd()), fbIoBlank, fbBlankUnblank); err != nil {
		log.Printf("splash: FBIOBLANK restore unavailable: %v (ignored)", err)
	}
}

func (b *fbdev) close() {
	_ = unix.Munmap(b.data)
	_ = b.file.Close()
}

// loadImage decodes the PNG; bad assets never stop the handshake.
func loadImage(path string) image.Image {
	raw, err := os.ReadFile(path)
	if err != nil {
		log.Printf("splash: reading %s: %v (holding black until exit)", path, err)
		return nil
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		log.Printf("splash: decoding %s: %v (holding black until exit)", path, err)
		return nil
	}
	return img
}

// run draws and then waits for the ready file, a signal, or the timeout
// cap; finishes with the 3-step fade and exit 0.
func run(fbDev, imgPath, readyPath string, timeout time.Duration) {
	b, err := openFB(fbDev)
	if err != nil {
		log.Printf("splash: framebuffer unavailable (%v) — nothing to do (dev container/headless) ", err)
		return
	}
	defer b.close()

	img := loadImage(imgPath)

	// Paint: black fill first (covers kernel console text), then the
	// logo centered.
	b.fillBlack()
	if img != nil {
		b.drawImage(img, 1.0)
	}
	if err := b.pan(); err != nil {
		log.Printf("splash: FBIOPAN_DISPLAY: %v (drawing anyway)", err)
	}
	log.Printf("splash: logo on %s (%dx%d %dbpp); waiting for %s, SIGTERM or cap %s",
		fbDev, b.w(), b.h(), int(b.v.BitsPerPixel), readyPath, timeout)

	// Wait loop: readiness file, SIGTERM/SIGINT, or the timeout cap.
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	var timeoutC <-chan time.Time
	if timeout >= 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timeoutC = t.C
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

wait:
	for {
		select {
		case sig := <-sigs:
			log.Printf("splash: %v — releasing the framebuffer", sig)
			break wait
		case <-timeoutC:
			log.Printf("splash: %s never appeared (timeout) — releasing the framebuffer", readyPath)
			break wait
		case <-tick.C:
			if readyFile(readyPath) {
				log.Printf("splash: app is ready (%s) — fading out", readyPath)
				break wait
			}
		}
	}

	// 3-step quick fade toward black, then restore console blank state.
	for _, brightness := range []float64{0.66, 0.33, 0.0} {
		b.fillBlack()
		if img != nil && brightness > 0 {
			b.drawImage(img, brightness)
		}
		b.pan()
		time.Sleep(150 * time.Millisecond)
	}
	b.restoreBlank()
	log.Println("splash: done, exiting 0")
	os.Exit(0)
}

// runClear exists for ExecStop and manual debugging.
func runClear(fbDev string) {
	b, err := openFB(fbDev)
	if err != nil {
		log.Printf("splash: framebuffer unavailable (%v) — nothing to clear", err)
		return
	}
	defer b.close()
	b.fillBlack()
	_ = b.pan()
	b.restoreBlank()
}

// readyFile reports the readiness file's presence. The appliance writes
// /run/timerpi/ready once it is serving; until it exists the splash holds
// the logo instead of racing the boot console away.
func readyFile(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// firstNonEmpty returns the first non-empty string (env-then-flag rule).
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
