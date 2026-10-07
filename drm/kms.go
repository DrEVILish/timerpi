//go:build linux

// kms.go is the real display backend: KMS with dumb buffers. It claims
// /dev/dri/card0 (DRM_IOCTL_SET_MASTER — this service must be the
// display's DRM master; any other master, e.g. a diagnostic tool that
// opened the card first, will make this fail with "permission denied" —
// see AGENTS.md/CuTePi wisdom), walks the connector→encoder→CRTC chain,
// prefers a 1920×1080@50 mode (falling back to the best fit), allocates
// TWO dumb buffers + framebuffers, SETCRTCs the first one, and presents
// by damage-copying into the back buffer followed by a legacy
// DRM_IOCTL_MODE_PAGE_FLIP with DRM_MODE_PAGE_FLIP_EVENT.
//
// Pacing chosen (documented per the plan): the page flip completes ON
// the next vblank and completion is read back as a drm_event on the
// DRM fd — that wait IS the vblank wait, so WAIT_VBLANK is never called
// separately. A blocking SetPlane commit would serialize identically,
// but a flip is atomic and tear-free by construction, so damage memcpys
// never touch the scanout buffer mid-frame.
//
// All drm_mode.h numbers are hand-copied uapi constants (verified
// against <drm/drm.h> + <drm/drm_mode.h> struct layouts); ioctl values
// are _IOWR('d', nr, size) encoded manually. No cgo.
package drm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ---- uapi: ioctl numbers (asm-generic _IOWR/_IO encoding, 'd'=0x64) ----

const (
	drmIoctlBase = 0x64 // 'd'
)

// iowr/io mirror the Linux _IOWR/_IO macros for the DRM ioctl base
// 'd' (0x64): dir=3 (_IOWR, bits 30-31), 14-bit size at bit 16,
// 8-bit type at bit 8, 8-bit nr at bit 0. Plain functions, so the
// ioctl numbers below live in vars — asserted against known-good
// values in TestUapiIoctlNumbers.
func iowr(nr, size uint32) uint32 { return 3<<30 | size<<16 | drmIoctlBase<<8 | nr }
func io(nr uint32) uint32         { return drmIoctlBase<<8 | nr }

func ioctlPtr(f *os.File, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

var (
	ioctlGetCap = iowr(0x0c, 16) // drm_get_cap

	ioctlSetMaster  = io(0x1e)
	ioctlDropMaster = io(0x1f)

	ioctlModeGetResources = iowr(0xa0, 64)  // drm_mode_card_res
	ioctlModeGetCRTC      = iowr(0xa1, 104) // drm_mode_crtc
	ioctlModeSetCRTC      = iowr(0xa2, 104)
	ioctlModeGetEncoder   = iowr(0xa6, 20) // drm_mode_get_encoder
	ioctlModeGetConnector = iowr(0xa7, 80) // drm_mode_get_connector
	ioctlModeAddFB        = iowr(0xae, 28) // drm_mode_fb_cmd
	ioctlModeRmFB         = iowr(0xaf, 4)  // unsigned int
	ioctlModePageFlip     = iowr(0xb0, 24) // drm_mode_crtc_page_flip
	ioctlModeCreateDumb   = iowr(0xb2, 32) // drm_mode_create_dumb
	ioctlModeMapDumb      = iowr(0xb3, 16) // drm_mode_map_dumb
	ioctlModeDestroyDumb  = iowr(0xb4, 4)  // drm_mode_destroy_dumb
)

const (
	capDumbBuffer = 0x1 // DRM_CAP_DUMB_BUFFER

	pageFlipEvent = 1 // DRM_MODE_PAGE_FLIP_EVENT

	eventFlipComplete = 0x02 // DRM_EVENT_FLIP_COMPLETE

	connDisconnected = 1
	connConnected    = 2
	connUnknown      = 3

	connectorHDMIA = 11
	connectorHDMIB = 12

	modeTypePreferred = 1 << 3 // DRM_MODE_TYPE_PREFERRED
)

// ---- uapi: struct mirrors (field-for-field, sizes asserted in tests) ----

type drmModeCardRes struct {
	FBPtr, CrtcPtr, ConnPtr, EncPtr          uint64
	CountFB, CountCrtc, CountConn, CountEnc  uint32
	MinWidth, MaxWidth, MinHeight, MaxHeight uint32
}

type drmModeModeInfo struct {
	Clock                                         uint32
	HDisplay, HSyncStart, HSyncEnd, HTotal, HSkew uint16
	VDisplay, VSyncStart, VSyncEnd, VTotal, VScan uint16
	VRefresh, Flags, Type                         uint32
	Name                                          [32]byte
}

type drmModeCrtc struct {
	SetConnectorsPtr                         uint64
	CountConnectors                          uint32
	CrtcID, FBID, X, Y, GammaSize, ModeValid uint32
	Mode                                     drmModeModeInfo
}

type drmModeGetEncoder struct {
	EncoderID, EncoderType, CrtcID, PossibleCrtcs, PossibleClones uint32
}

type drmModeGetConnector struct {
	EncodersPtr, ModesPtr, PropsPtr, PropValuesPtr                uint64
	CountModes, CountProps, CountEncoders, EncoderID, ConnectorID uint32
	ConnectorType, ConnectorTypeID, Connection                    uint32
	MMWidth, MMHeight, Subpixel, Pad                              uint32
}

type drmModeFBCmd struct {
	FBID, Width, Height, Pitch, BPP, Depth, Handle uint32
}

type drmModeCrtcPageFlip struct {
	CrtcID, FBID, Flags, Reserved uint32
	UserData                      uint64
}

type drmModeCreateDumb struct {
	Height, Width, BPP, Flags, Handle, Pitch uint32
	Size                                     uint64
}

type drmModeMapDumb struct {
	Handle, Pad uint32
	Offset      uint64
}

type drmModeDestroyDumb struct {
	Handle uint32
}

type drmGetCap struct {
	Capability, Value uint64
}

// drmModeModeInfo must be exactly 68 bytes, drm_mode_crtc 104:
// (padding drift = silent framebuffer corruption)
// (asserted in drm_test.go TestUapiStructSizes)

// ---- backend ----

type dumbBuffer struct {
	handle uint32
	fbID   uint32
	data   []byte // mmapped
	stride int
}

// KMSBackend presents on the HDMI output via KMS dumb buffers.
type KMSBackend struct {
	card   *os.File
	w, h   int
	bufs   [2]dumbBuffer
	draw   int // index of the buffer NOT being scanned out (next Present writes)
	crtc   uint32
	cons   []uint32
	mode   drmModeModeInfo
	master bool
	tok    uint64 // flip user_data monotonic token
}

// NewKMSBackend constructs an unopened backend (env: DRM_CARD).
func NewKMSBackend() *KMSBackend { return &KMSBackend{} }

func (b *KMSBackend) devPath() string {
	if p := os.Getenv("DRM_CARD"); p != "" {
		return p
	}
	return "/dev/dri/card0"
}

// Open claims the card as DRM master, resolves the display chain and
// allocates the double-buffered scanout.
func (b *KMSBackend) Open() error {
	path := b.devPath()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("kms: opening %s: %w", path, err)
	}
	b.card = f

	cardCap := drmGetCap{Capability: capDumbBuffer}
	if err := ioctlPtr(f, uintptr(ioctlGetCap), unsafe.Pointer(&cardCap)); err != nil {
		return failOpen(f, "GET_CAP", err)
	}
	if cardCap.Value == 0 {
		return failOpen(f, "driver has no dumb buffer support", nil)
	}

	// Become DRM master. Fails with EACCES/EBUSY if another client
	// holds it — per CuTePi: this service must be FIRST on the card.
	if err := ioctlPtr(f, uintptr(ioctlSetMaster), nil); err != nil {
		if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EBUSY) {
			return failOpen(f,
				path+" is held by another DRM master (permission denied) — "+
					"the service must be started before any card-holding tool "+
					"(this is a CuTePi service contract)",
				nil)
		}
		return failOpen(f, "SET_MASTER", err)
	}
	b.master = true

	if err := b.pickChain(); err != nil {
		return failOpen(f, err.Error(), err)
	}
	if err := b.allocScanout(); err != nil {
		return failOpen(f, err.Error(), err)
	}
	return nil
}

// failOpen closes the card and returns the annotated error (msg is
// pre-formatted; err is optionally attached).
func failOpen(f *os.File, msg string, err error) error {
	f.Close()
	if err == nil {
		return errors.New("kms: " + msg)
	}
	return fmt.Errorf("kms: %s: %w", msg, err)
}

// pickChain walks resources → connectors → encoder → CRTC and selects
// the mode. Preference order (documented in README):
//  1. connector: HDMI-A-1, then any HDMI, then any connected, honoring
//     the DRM_CONNECTOR name filter when set.
//  2. mode: 1920x1080 @ 50, then 1920x1080 preferred, then preferred,
//     then largest area.
func (b *KMSBackend) pickChain() error {
	res, err := b.resources()
	if err != nil {
		return err
	}
	connFilter := os.Getenv("DRM_CONNECTOR")

	var chain *chainResult
	errs := []string{}
	for _, id := range res.connectorIDs {
		c, err := b.connector(id)
		if err != nil {
			errs = append(errs, fmt.Sprintf("connector %d: %v", id, err))
			continue
		}
		if c.Connection != connConnected {
			continue
		}
		alias := fmt.Sprintf("%s-%d", connectorName(c.ConnectorType), c.ConnectorTypeID)
		if connFilter != "" && alias != connFilter {
			continue
		}
		chain, err = b.matchChain(res, c)
		if err == nil {
			break
		}
		errs = append(errs, fmt.Sprintf("connector %s: %v", alias, err))
	}
	if chain == nil {
		return fmt.Errorf("kms: no connector with an encodable CRTC found (%v)", errs)
	}

	b.crtc = chain.crtcID
	b.cons = []uint32{chain.connectorID}
	b.mode = chain.mode
	b.w, b.h = int(chain.mode.HDisplay), int(chain.mode.VDisplay)
	return nil
}

// chainResult is a resolved connector→encoder→CRTC chain.
type chainResult struct {
	crtcID, connectorID uint32
	encoderID           uint32
	mode                drmModeModeInfo
}

// connData carries one connector's uapi record plus its arrays.
type connData struct {
	*drmModeGetConnector
	mode     drmModeModeInfo
	encoders []uint32
}

type resources struct {
	crtcIDs, connectorIDs []uint32
}

func (b *KMSBackend) resources() (*resources, error) {
	var res drmModeCardRes
	if err := ioctlPtr(b.card, uintptr(ioctlModeGetResources), unsafe.Pointer(&res)); err != nil {
		return nil, fmt.Errorf("GETRESOURCES: %w", err)
	}
	if res.CountCrtc == 0 || res.CountConn == 0 {
		return nil, fmt.Errorf("GETRESOURCES: %d crtc / %d connectors is empty",
			res.CountCrtc, res.CountConn)
	}
	crtcs := make([]uint32, res.CountCrtc)
	conns := make([]uint32, res.CountConn)
	res.CrtcPtr = uint64(uintptr(unsafe.Pointer(&crtcs[0])))
	res.ConnPtr = uint64(uintptr(unsafe.Pointer(&conns[0])))
	if err := ioctlPtr(b.card, uintptr(ioctlModeGetResources), unsafe.Pointer(&res)); err != nil {
		return nil, fmt.Errorf("GETRESOURCES(2): %w", err)
	}
	return &resources{crtcIDs: crtcs, connectorIDs: conns}, nil
}

func (b *KMSBackend) connector(id uint32) (*connData, error) {
	var c drmModeGetConnector
	c.ConnectorID = id
	if err := ioctlPtr(b.card, uintptr(ioctlModeGetConnector), unsafe.Pointer(&c)); err != nil {
		return nil, fmt.Errorf("GETCONNECTOR: %w", err)
	}
	// Two-pass: counts first, then the arrays (encoders + modes).
	encoders := make([]uint32, c.CountEncoders)
	modes := make([]drmModeModeInfo, c.CountModes)
	if c.CountEncoders > 0 {
		c.EncodersPtr = uint64(uintptr(unsafe.Pointer(&encoders[0])))
	}
	if c.CountModes > 0 {
		c.ModesPtr = uint64(uintptr(unsafe.Pointer(&modes[0])))
	}
	if err := ioctlPtr(b.card, uintptr(ioctlModeGetConnector), unsafe.Pointer(&c)); err != nil {
		return nil, fmt.Errorf("GETCONNECTOR(2): %w", err)
	}
	if c.CountModes == 0 {
		return nil, fmt.Errorf("no modes")
	}
	return &connData{
		drmModeGetConnector: &drmModeGetConnector{
			ConnectorID:     c.ConnectorID,
			ConnectorType:   c.ConnectorType,
			ConnectorTypeID: c.ConnectorTypeID,
			Connection:      c.Connection,
			EncoderID:       c.EncoderID,
			CountEncoders:   c.CountEncoders,
		},
		mode:     modes[modeIndex(&c, modes)],
		encoders: encoders,
	}, nil
}

// entry point picks the 1920×1080@50 mode, then 1080 preferred, then
// preferred, then largest.
func modeIndex(c *drmModeGetConnector, modes []drmModeModeInfo) int {
	best := modeByPredicate(modes, func(m drmModeModeInfo) bool {
		return m.HDisplay == 1920 && m.VDisplay == 1080 && m.VRefresh == 50 &&
			m.Flags&modeTypeInterlace == 0
	}, 0)
	if best >= 0 {
		return best
	}
	// 1920×1080 at any refresh (progressive first).
	if i := modeByPredicate(modes, func(m drmModeModeInfo) bool {
		return m.HDisplay == 1920 && m.VDisplay == 1080 && m.Flags&modeTypeInterlace == 0
	}, modeTypePreferred); i >= 0 {
		return i
	}
	if i := modeByPredicate(modes, func(m drmModeModeInfo) bool {
		return m.Type&modeTypePreferred != 0
	}, 0); i >= 0 {
		return i
	}
	// Largest area.
	best = 0
	for i, m := range modes {
		if int(m.HDisplay)*int(m.VDisplay) > int(modes[best].HDisplay)*int(modes[best].VDisplay) {
			best = i
		}
	}
	return best
}

const modeTypeInterlace = 1 << 4 // DRM_MODE_FLAG_INTERLACE

func modeByPredicate(modes []drmModeModeInfo, pred func(drmModeModeInfo) bool, prefer uint32) int {
	first := -1
	best := -1
	for i, m := range modes {
		if !pred(m) {
			continue
		}
		if first < 0 {
			first = i
		}
		if prefer != 0 && m.Type&prefer != 0 && best < 0 {
			best = i
		}
	}
	if best >= 0 {
		return best
	}
	return first
}

func connectorName(t uint32) string {
	switch t {
	case connectorHDMIA:
		return "HDMI-A"
	case connectorHDMIB:
		return "HDMI-B"
	default:
		return fmt.Sprintf("connector-%d", t)
	}
}

func (b *KMSBackend) matchChain(res *resources, c *connData) (*chainResult, error) {
	// Candidate encoder: currently assigned one first, else any.
	candidates := []uint32{}
	if c.EncoderID != 0 {
		candidates = append(candidates, c.EncoderID)
	}
	candidates = append(candidates, c.encoders...)

	for _, encID := range candidates {
		var enc drmModeGetEncoder
		enc.EncoderID = encID
		if err := ioctlPtr(b.card, uintptr(ioctlModeGetEncoder), unsafe.Pointer(&enc)); err != nil {
			continue // stale encoder slot (hotplug); try the next
		}
		for i, crtcID := range res.crtcIDs {
			if enc.PossibleCrtcs&(1<<i) == 0 {
				continue
			}
			// The connector's modes are valid for this chain by EDID;
			// take the chosen display mode.
			return &chainResult{
				crtcID: crtcID, connectorID: c.ConnectorID,
				encoderID: encID, mode: c.mode,
			}, nil
		}
	}
	return nil, fmt.Errorf("no crtc reachable (encoder %d)", c.EncoderID)
}

// allocScanout creates the two dumb buffers + framebuffers and starts
// scanning out the first (black) one.
func (b *KMSBackend) allocScanout() error {
	for i := range b.bufs {
		db, err := b.createDumb(b.w, b.h, 32)
		if err != nil {
			return err
		}
		var fb drmModeFBCmd
		fb.Width, fb.Height, fb.Pitch, fb.BPP, fb.Depth, fb.Handle =
			uint32(b.w), uint32(b.h), db.pitch, 32, 24, db.handle
		if err := ioctlPtr(b.card, uintptr(ioctlModeAddFB), unsafe.Pointer(&fb)); err != nil {
			return fmt.Errorf("ADDFB: %w", err)
		}
		data, err := b.mapDumb(db)
		if err != nil {
			return fmt.Errorf("map dumb: %w", err)
		}
		b.bufs[i] = dumbBuffer{
			handle: db.handle, fbID: fb.FBID, data: data, stride: int(db.pitch),
		}
	}

	// SETCRTC: connectors array + mode + fb #0 (zero-filled = black).
	// From now on buf[0] is scanout; the next Present draws buf[1].
	var crtc drmModeCrtc
	crtc.CrtcID, crtc.FBID = b.crtc, b.bufs[0].fbID
	crtc.SetConnectorsPtr = uint64(uintptr(unsafe.Pointer(&b.cons[0])))
	crtc.CountConnectors = uint32(len(b.cons))
	crtc.ModeValid = 1
	crtc.Mode = b.mode
	if err := ioctlPtr(b.card, uintptr(ioctlModeSetCRTC), unsafe.Pointer(&crtc)); err != nil {
		return fmt.Errorf("SETCRTC(crtc %d, fb %d, mode %q): %w",
			b.crtc, b.bufs[0].fbID, modeName(b.mode), err)
	}
	b.draw = 1
	return nil
}

type dumbInfo struct {
	handle, pitch uint32
	size          uint64
}

func (b *KMSBackend) createDumb(w, h, bpp int) (*dumbInfo, error) {
	var d drmModeCreateDumb
	d.Height, d.Width, d.BPP = uint32(h), uint32(w), uint32(bpp)
	if err := ioctlPtr(b.card, uintptr(ioctlModeCreateDumb), unsafe.Pointer(&d)); err != nil {
		return nil, fmt.Errorf("CREATE_DUMB(%dx%d@%d): %w", w, h, bpp, err)
	}
	return &dumbInfo{handle: d.Handle, pitch: d.Pitch, size: d.Size}, nil
}

func (b *KMSBackend) mapDumb(d *dumbInfo) ([]byte, error) {
	var m drmModeMapDumb
	m.Handle = d.handle
	if err := ioctlPtr(b.card, uintptr(ioctlModeMapDumb), unsafe.Pointer(&m)); err != nil {
		return nil, fmt.Errorf("MAP_DUMB: %w", err)
	}
	data, err := unix.Mmap(int(b.card.Fd()), int64(m.Offset), int(d.size),
		unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap dumb: %w", err)
	}
	return data, nil
}

func modeName(m drmModeModeInfo) string {
	name := m.Name[:]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return string(name)
}

// Size reports the panel geometry.
func (b *KMSBackend) Size() (int, int) { return b.w, b.h }

// Present pushes damage into the back buffer, then page-flips it and
// (blocking) waits for the flip-complete vblank event. That wait is
// the frame pacing; there is no separate WAIT_VBLANK ioctl anywhere.
func (b *KMSBackend) Present(img *Image, dirty []Rect) error {
	if len(dirty) == 0 || img == nil {
		return nil // untouched frame: nothing to scan
	}
	back := &b.bufs[b.draw]
	stride, w, h := back.stride, b.w, b.h
	if stride < w*4 {
		return fmt.Errorf("kms: backend geometry is broken (stride %d < %d)", stride, w*4)
	}
	// The source is checked against ITS OWN stride (a padded destination
	// stride used to fail every frame; BUGLOG RW48).
	if img.Stride < img.W*4 || len(img.Pix) < img.Stride*img.H {
		return fmt.Errorf("kms: source frame is broken (%dx%d, stride %d, %d bytes)", img.W, img.H, img.Stride, len(img.Pix))
	}
	clip := Rect{0, 0, min(w, img.W), min(h, img.H)}
	for _, r := range dirty {
		r = r.Clip(clip)
		if r.Empty() {
			continue
		}
		for dy := 0; dy < r.H; dy++ {
			src := img.Pix[(r.Y+dy)*img.Stride+r.X*4:]
			dst := back.data[(r.Y+dy)*stride+r.X*4:]
			copy(dst[:r.W*4], src[:r.W*4])
		}
	}

	// Flip the just-updated buffer, and wait for the completion event
	// (delivered at the vblank where the flip latched). user_data is a
	// monotonic token that identifies our flip.
	b.tok++
	tok := b.tok
	var flip drmModeCrtcPageFlip
	flip.CrtcID, flip.FBID, flip.Flags = b.crtc, back.fbID, pageFlipEvent
	flip.UserData = tok
	if err := ioctlPtr(b.card, uintptr(ioctlModePageFlip), unsafe.Pointer(&flip)); err != nil {
		// Nothing was queued; the back buffer stays off-screen and we
		// retry next frame without swapping roles.
		return fmt.Errorf("PAGE_FLIP(fb %d): %w", back.fbID, err)
	}
	if err := b.waitFlip(tok, 100*time.Millisecond); err != nil {
		// The flip was queued, so it has almost certainly latched even
		// without its event: swap roles, but surface the anomaly.
		b.draw ^= 1
		return fmt.Errorf("kms: %w", err)
	}
	// Swap roles: the flipped buffer is now scanout, the old one is the
	// off-screen draw target.
	b.draw ^= 1
	return nil
}

// waitFlip reads drm_events from the card fd until this token's flip
// completion arrives, other events are drained, and the deadline —
// reached after several missed vblanks — ends the wait (the flip has
// almost certainly latched anyway; the roles swap defensively).
func (b *KMSBackend) waitFlip(tok uint64, timeout time.Duration) error {
	defer func() { _ = b.card.SetReadDeadline(time.Time{}) }()
	var buf [64]byte
	for {
		if err := b.card.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return fmt.Errorf("set read deadline (pollable fd expected): %w", err)
		}
		n, err := b.card.Read(buf[:])
		if err != nil {
			return fmt.Errorf("read flip event: %w", err)
		}
		if n < 8 {
			continue
		}
		etype := binary.LittleEndian.Uint32(buf[0:4])
		elen := binary.LittleEndian.Uint32(buf[4:8])
		if etype != eventFlipComplete || elen < 16 || elen > 64 {
			continue // vblank or foreign event: drain
		}
		if binary.LittleEndian.Uint64(buf[8:16]) == tok {
			return nil
		}
	}
}

// Close tears the chain down: RMFB both framebuffers, destroy dumb
// buffers, drop master, close the card. The CRTC keeps showing the
// last sane buffer until RMFB lands (splash takes over on shutdown).
func (b *KMSBackend) Close() error {
	if b.card == nil {
		return nil
	}
	for i := range b.bufs {
		db := &b.bufs[i]
		if db.fbID != 0 {
			var fb uint32 = db.fbID
			_ = ioctlPtr(b.card, uintptr(ioctlModeRmFB), unsafe.Pointer(&fb))
			db.fbID = 0
		}
		if len(db.data) > 0 {
			_ = unix.Munmap(db.data)
			db.data = nil
		}
		if db.handle != 0 {
			var d drmModeDestroyDumb
			d.Handle = db.handle
			_ = ioctlPtr(b.card, uintptr(ioctlModeDestroyDumb), unsafe.Pointer(&d))
			db.handle = 0
		}
	}
	if b.master {
		_ = ioctlPtr(b.card, uintptr(ioctlDropMaster), nil)
		b.master = false
	}
	return b.card.Close()
}
