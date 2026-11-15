//go:build linux

package drm

import (
	"syscall"
	"testing"
	"unsafe"
)

func TestResourcesDoNotOfferNullUnusedArrays(t *testing.T) {
	calls := 0
	b := &KMSBackend{query: func(req uintptr, arg unsafe.Pointer) error {
		if req != uintptr(ioctlModeGetResources) {
			t.Fatalf("unexpected ioctl %x", req)
		}
		r := (*drmModeCardRes)(arg)
		calls++
		if calls%2 == 1 {
			r.CountFB = 2
			r.CountEnc = 2
			r.CountCrtc = 1
			r.CountConn = 2
			return nil
		}
		if r.CountFB != 0 || r.CountEnc != 0 {
			return syscall.EFAULT
		}
		if r.CrtcPtr == 0 || r.ConnPtr == 0 {
			return syscall.EFAULT
		}
		return nil
	}}
	got, err := b.resources()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.crtcIDs) != 1 || len(got.connectorIDs) != 2 {
		t.Fatalf("resources=%+v", got)
	}
}

func TestConnectorDoesNotOfferNullPropertyArrays(t *testing.T) {
	calls := 0
	b := &KMSBackend{query: func(req uintptr, arg unsafe.Pointer) error {
		if req != uintptr(ioctlModeGetConnector) {
			t.Fatalf("unexpected ioctl %x", req)
		}
		c := (*drmModeGetConnector)(arg)
		calls++
		if calls%2 == 1 {
			c.CountProps = 4
			c.CountModes = 1
			c.CountEncoders = 1
			c.EncoderID = 7
			return nil
		}
		if c.CountProps != 0 || c.ModesPtr == 0 || c.EncodersPtr == 0 {
			return syscall.EFAULT
		}
		return nil
	}}
	got, err := b.connector(17)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConnectorID != 17 || got.EncoderID != 7 || len(got.encoders) != 1 {
		t.Fatalf("connector=%+v", got)
	}
}

func TestResourceEnumerationRetriesGrowthAndTrimsShrink(t *testing.T) {
	for _, grow := range []bool{true, false} {
		calls := 0
		b := &KMSBackend{query: func(_ uintptr, arg unsafe.Pointer) error {
			r := (*drmModeCardRes)(arg)
			calls++
			switch calls {
			case 1:
				r.CountCrtc = 1
				r.CountConn = 3
			case 2:
				if grow {
					r.CountCrtc = 2
				} else {
					r.CountConn = 1
				}
			default:
				r.CountCrtc = 2
				r.CountConn = 2
			}
			return nil
		}}
		got, err := b.resources()
		if err != nil {
			t.Fatal(err)
		}
		if grow && (calls != 4 || len(got.crtcIDs) != 2 || len(got.connectorIDs) != 2) {
			t.Fatalf("growth calls=%d resources=%+v", calls, got)
		}
		if !grow && (calls != 2 || len(got.connectorIDs) != 1) {
			t.Fatalf("shrink calls=%d resources=%+v", calls, got)
		}
	}
}

func TestConnectorEnumerationRetriesGrowth(t *testing.T) {
	calls := 0
	b := &KMSBackend{query: func(_ uintptr, arg unsafe.Pointer) error {
		c := (*drmModeGetConnector)(arg)
		calls++
		c.CountEncoders = 1
		if calls == 1 {
			c.CountModes = 1
		} else {
			c.CountModes = 2
		}
		return nil
	}}
	if _, err := b.connector(5); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("growth returned incomplete mode array after%d calls", calls)
	}
}

func TestEnumerationStopsWhenTopologyNeverSettles(t *testing.T) {
	calls := 0
	b := &KMSBackend{query: func(_ uintptr, arg unsafe.Pointer) error {
		r := (*drmModeCardRes)(arg)
		calls++
		r.CountCrtc = 1
		r.CountConn = 1
		if calls%2 == 0 {
			r.CountConn = 2
		}
		return nil
	}}
	if _, err := b.resources(); err == nil || calls != 6 {
		t.Fatalf("unstable resources: calls=%d err=%v", calls, err)
	}
	calls = 0
	b.query = func(_ uintptr, arg unsafe.Pointer) error {
		c := (*drmModeGetConnector)(arg)
		calls++
		c.CountModes = 1
		if calls%2 == 0 {
			c.CountModes = 2
		}
		return nil
	}
	if _, err := b.connector(5); err == nil || calls != 6 {
		t.Fatalf("unstable connector: calls=%d err=%v", calls, err)
	}
}
