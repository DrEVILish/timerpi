// Major release N17–N21 (VENUE-CLOUD §9–§14): health reports role and
// protocol; every TimerPi serves its signed builds; the radio settings are
// box-password settings.
package routes_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"timerpi/config"
)

func TestHealthRoleAndProto(t *testing.T) {
	ts := newAPITest(t)
	_, b := ts.anon("GET", "/health", nil, "")
	var h struct {
		Proto   int    `json:"proto"`
		Role    string `json:"role"`
		Version string `json:"version"`
	}
	_ = json.Unmarshal(b, &h)
	if h.Proto != 3 || h.Role != "box" || h.Version == "" {
		t.Fatalf("health = %s", b)
	}
	t.Setenv("TIMERPI_ROLE", "cloud")
	_, b = ts.anon("GET", "/health", nil, "")
	if !strings.Contains(string(b), `"role":"cloud"`) {
		t.Fatalf("TIMERPI_ROLE=cloud not reported: %s", b)
	}
}

func TestUpdateServesPublishedBuild(t *testing.T) {
	t.Setenv("TIMERPI_DATA_DIR", t.TempDir())
	ts := newAPITest(t)
	// An earlier test may have pinned the data dir (config.LoadConfig).
	dir := config.DataDir()
	t.Cleanup(func() { os.RemoveAll(filepath.Join(dir, "releases")) })
	if code, _ := ts.anon("GET", "/api/update/manifest?arch=riscv64", nil, ""); code != 404 {
		t.Errorf("unknown arch: %d", code)
	}
	if code, _ := ts.anon("GET", "/api/update/manifest?arch=../../etc", nil, ""); code != 404 {
		t.Errorf("path in arch: %d", code)
	}
	rel := filepath.Join(dir, "releases", "arm64")
	_ = os.MkdirAll(rel, 0o755)
	_ = os.WriteFile(filepath.Join(rel, "timerpi"), []byte("arm64 build"), 0o755)
	_ = os.WriteFile(filepath.Join(rel, "timerpi.manifest.json"), []byte(`{"version":"3.1.0","arch":"arm64","sha256":"x","size":11,"sig":"y"}`), 0o644)
	code, b := ts.anon("GET", "/api/update/manifest?arch=arm64", nil, "")
	if code != 200 || !strings.Contains(string(b), `"version":"3.1.0"`) {
		t.Fatalf("manifest: %d %s", code, b)
	}
	if code, b := ts.anon("GET", "/api/update/binary?arch=arm64", nil, ""); code != 200 || string(b) != "arm64 build" {
		t.Fatalf("binary: %d %q", code, b)
	}
}

func TestMeshRadioSettings(t *testing.T) {
	t.Setenv("TIMERPI_DATA_DIR", t.TempDir())
	ts := newAPITest(t)
	if code, _ := ts.anon("POST", "/api/network/mesh", []byte(`{"country":"GB","ch24":13,"ch5":36}`), "application/json"); code == 200 {
		t.Fatal("radio settings changed without the box password")
	}
	code, b := ts.call("POST", "/api/network/mesh", []byte(`{"country":"ie","ch24":6,"ch5":40}`), "application/json")
	if code != 200 || !strings.Contains(string(b), `"country":"IE"`) || !strings.Contains(string(b), `"ch5":40`) {
		t.Fatalf("save: %d %s", code, b)
	}
	for _, bad := range []string{`{"country":"GB","ch24":14,"ch5":36}`, `{"country":"GB","ch24":13,"ch5":38}`, `{"country":"GBR","ch24":13,"ch5":36}`} {
		if code, _ := ts.call("POST", "/api/network/mesh", []byte(bad), "application/json"); code != 400 {
			t.Errorf("%s accepted: %d", bad, code)
		}
	}
}
