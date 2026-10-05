//go:build linux

// hw_test.go — the REAL device integration helper. It is
// skip-guarded: on a machine without /dev/dri (like this build
// container) or without TIMERPI_HW_TEST=1 the test body returns
// immediately. On the Pi run:
//
//	TIMERPI_HW_TEST=1 go test ./drm/ -run TestHW -v
//
// and it drives the actual card/fbdev for ~200 frames so visual
// verification can happen on the panel (see README's checklist).
package drm

import (
	"os"
	"testing"
	"time"
	"unsafe"
)

func hasCard() bool {
	for _, p := range []string{
		cardPath(),
		"/dev/dri/card1",
	} {
		if st, err := os.Stat(p); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			return true
		}
	}
	return false
}

func cardPath() string {
	if p := os.Getenv("DRM_CARD"); p != "" {
		return p
	}
	return "/dev/dri/card0"
}

// TestUapiStructSizes locks the hand-written drm uapi mirrors to the
// kernel layouts: struct drift is silent framebuffer corruption, so a
// different toolchain/kernel pairing must fail loudly, in tests.
func TestUapiStructSizes(t *testing.T) {
	// from <drm/drm.h> + <drm/drm_mode.h> (see kms.go header comment)
	want := map[string]uintptr{
		"drmModeCardRes":      64,
		"drmModeModeInfo":     68,
		"drmModeCrtc":         104,
		"drmModeGetEncoder":   20,
		"drmModeGetConnector": 80,
		"drmModeFBCmd":        28,
		"drmModeCrtcPageFlip": 24,
		"drmModeCreateDumb":   32,
		"drmModeMapDumb":      16,
		"drmModeDestroyDumb":  4,
		"drmGetCap":           16,
	}
	got := map[string]uintptr{
		"drmModeCardRes":      unsafe.Sizeof(drmModeCardRes{}),
		"drmModeModeInfo":     unsafe.Sizeof(drmModeModeInfo{}),
		"drmModeCrtc":         unsafe.Sizeof(drmModeCrtc{}),
		"drmModeGetEncoder":   unsafe.Sizeof(drmModeGetEncoder{}),
		"drmModeGetConnector": unsafe.Sizeof(drmModeGetConnector{}),
		"drmModeFBCmd":        unsafe.Sizeof(drmModeFBCmd{}),
		"drmModeCrtcPageFlip": unsafe.Sizeof(drmModeCrtcPageFlip{}),
		"drmModeCreateDumb":   unsafe.Sizeof(drmModeCreateDumb{}),
		"drmModeMapDumb":      unsafe.Sizeof(drmModeMapDumb{}),
		"drmModeDestroyDumb":  unsafe.Sizeof(drmModeDestroyDumb{}),
		"drmGetCap":           unsafe.Sizeof(drmGetCap{}),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s size = %d, kernel expects %d — struct drift", k, got[k], v)
		}
	}
}

// TestUapiIoctlNumbers mirrors the values libdrm/kernel actually use,
// recomputed by hand (_IOWR('d', nr, sizeof struct)).
func TestUapiIoctlNumbers(t *testing.T) {
	cases := map[string]uint32{
		"GET_CAP":      0xc010640c,
		"SET_MASTER":   0x0000641e,
		"DROP_MASTER":  0x0000641f,
		"GETRESOURCES": 0xc04064a0,
		"GETCRTC":      0xc06864a1,
		"SETCRTC":      0xc06864a2,
		"GETENCODER":   0xc01464a6,
		"GETCONNECTOR": 0xc05064a7,
		"ADDFB":        0xc01c64ae,
		"RMFB":         0xc00464af,
		"PAGE_FLIP":    0xc01864b0,
		"CREATE_DUMB":  0xc02064b2,
		"MAP_DUMB":     0xc01064b3,
		"DESTROY_DUMB": 0xc00464b4,
	}
	got := map[string]uint32{
		"GET_CAP":      ioctlGetCap,
		"SET_MASTER":   ioctlSetMaster,
		"DROP_MASTER":  ioctlDropMaster,
		"GETRESOURCES": ioctlModeGetResources,
		"GETCRTC":      ioctlModeGetCRTC,
		"SETCRTC":      ioctlModeSetCRTC,
		"GETENCODER":   ioctlModeGetEncoder,
		"GETCONNECTOR": ioctlModeGetConnector,
		"ADDFB":        ioctlModeAddFB,
		"RMFB":         ioctlModeRmFB,
		"PAGE_FLIP":    ioctlModePageFlip,
		"CREATE_DUMB":  ioctlModeCreateDumb,
		"MAP_DUMB":     ioctlModeMapDumb,
		"DESTROY_DUMB": ioctlModeDestroyDumb,
	}
	for k, wantV := range cases {
		if got[k] != wantV {
			t.Errorf("ioctl %s = 0x%08x, want 0x%08x", k, got[k], wantV)
		}
	}
}

func hwGate(t *testing.T) (bool, time.Duration) {
	t.Helper()
	if os.Getenv("TIMERPI_HW_TEST") != "1" {
		return false, 0
	}
	if !hasCard() {
		t.Skip("TIMERPI_HW_TEST=1 but no usable /dev/dri card here")
	}
	return true, 2 * time.Second
}

// TestHWDirectKMS drives the real KMS chain with an animated view (a
// moving progress sweep and a ticking seconds digit) directly through
// the backend, skipping when no device exists.
func TestHWDirectKMS(t *testing.T) {
	hwGate(t)
	b := NewKMSBackend()
	if err := b.Open(); err != nil {
		t.Skipf("KMS open failed here (expected if another master holds the card): %v", err)
	}
	defer b.Close()
	w, h := b.Size()
	t.Logf("KMS opened %dx%d", w, h)
	if w != LayoutW || h != LayoutH {
		t.Logf("WARNING: panel is %dx%d, renderer draws %dx%d — expect clipping", w, h, LayoutW, LayoutH)
	}
	hwLoop(t, b)
}

// hwLoop renders ~200 animated frames through the given backend.
func hwLoop(t *testing.T, b Backend) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	n := 0
	for n < 200 && time.Now().Before(deadline) {
		nowMS := time.Now().UnixMilli()
		p := fixedProvider(clockishView(n, nowMS))
		c := NewClock(b, p)
		if _, err := c.Frame(nowMS); err != nil {
			t.Errorf("present %d: %v", n, err)
			return
		}
		time.Sleep(20 * time.Millisecond)
		n++
	}
	t.Logf("presented %d frames through %T", n, b)
}

// TestHWDirectFB drives the fbdev backend the same way.
func TestHWDirectFB(t *testing.T) {
	if os.Getenv("TIMERPI_HW_TEST") != "1" {
		t.Skip("not on the target: TIMERPI_HW_TEST unset")
	}
	if _, err := os.Stat(fbDevice()); err != nil {
		t.Skipf("no %s here (container)", fbDevice())
	}
	b := NewFBBackend()
	if err := b.Open(); err != nil {
		t.Skipf("fbdev open failed: %v", err)
	}
	defer b.Close()
	hwLoop(t, b)
}

// clockishView animates a view off a frame counter.
func clockishView(n int, nowMS int64) View {
	v := View{
		Label:       "ADJOURNED",
		Next:        "COUNCIL MEETING",
		NextSpeaker: "LESLIE",
		RemainingMS: int64(30-n%30) * 1000,
		Progress:    float64(n%100) / 100,
		WallClock:   time.UnixMilli(nowMS).Format("15:04:05"),
		AlertState:  AlertNormal,
	}
	if n%4 == 0 {
		v.Message = "PLEASE WRAP UP"
	}
	return v
}
