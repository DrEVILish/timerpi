package config

import (
	"testing"
)

// SetTitle/SetDeviceName validation had no tests (only SetAllowedHosts
// was covered): empty and over-long values must be refused without
// touching the persisted config.
func TestSetTitleValidation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TIMERPI_DATA_DIR", dir)
	t.Setenv("CAPACITIMER_HTTP_PORT", "")
	LoadConfig()

	if err := SetTitle(""); err == nil {
		t.Error("SetTitle(\"\") accepted")
	}
	long := make([]byte, 121)
	for i := range long {
		long[i] = 'x'
	}
	if err := SetTitle(string(long)); err == nil {
		t.Error("SetTitle(121 chars) accepted")
	}
	if err := SetTitle("Opening Night"); err != nil {
		t.Fatalf("SetTitle(valid): %v", err)
	}
	if got := Title(); got != "Opening Night" {
		t.Errorf("Title() = %q, want %q", got, "Opening Night")
	}
}

func TestSetDeviceNameValidation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TIMERPI_DATA_DIR", dir)
	t.Setenv("CAPACITIMER_HTTP_PORT", "")
	LoadConfig()

	if err := SetDeviceName("   "); err == nil {
		t.Error("SetDeviceName(whitespace) accepted")
	}
	long := make([]byte, 65)
	for i := range long {
		long[i] = 'y'
	}
	if err := SetDeviceName(string(long)); err == nil {
		t.Error("SetDeviceName(65 chars) accepted")
	}
	if err := SetDeviceName("stage-left"); err != nil {
		t.Fatalf("SetDeviceName(valid): %v", err)
	}
	if got := DeviceName(); got != "stage-left" {
		t.Errorf("DeviceName() = %q, want %q", got, "stage-left")
	}
}
