package drm

import "testing"

// xf86drmMode.h drmModeConnection: connected=1, disconnected=2, unknown=3.
func TestUapiConnectedConnectorValue(t *testing.T) {
	if connConnected != 1 {
		t.Fatalf("connected wire value=%d, want DRM_MODE_CONNECTED(1)", connConnected)
	}
}
