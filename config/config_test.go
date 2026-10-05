package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// LoadConfig must read config.json from TIMERPI_DATA_DIR, apply env
// overrides over the file, and persist a 0600 file on first run.
func TestLoadConfigEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TIMERPI_DATA_DIR", dir)
	t.Setenv("TIMERPI_HTTP_PORT", "8080")

	// Pre-existing config with a stale port the env must override.
	if err := os.WriteFile(filepath.Join(dir, "config.json"),
		[]byte(`{"http_port": 9999, "title": "Friday Night", "allowed_hosts": ["show.example.com"]}`),
		0o600); err != nil {
		t.Fatal(err)
	}

	LoadConfig()
	if got := HTTPPort(); got != 8080 {
		t.Errorf("HTTPPort = %d, want 8080 (env overrides file)", got)
	}
	if got := Title(); got != "Friday Night" {
		t.Errorf("Title = %q, want %q", got, "Friday Night")
	}
	if got := AllowedHosts(); len(got) != 1 || got[0] != "show.example.com" {
		t.Errorf("AllowedHosts = %v, want [show.example.com]", got)
	}
	if DataDir() != dir {
		t.Errorf("DataDir = %q, want %q", DataDir(), dir)
	}
}

// First run writes the defaults as a real file, mode 0600.
func TestFirstRunWritesDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TIMERPI_DATA_DIR", dir)
	os.Unsetenv("TIMERPI_HTTP_PORT")

	LoadConfig()

	path := filepath.Join(dir, "config.json")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("config.json not created on first run: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("config.json mode = %v, want 0600", st.Mode().Perm())
	}

	var raw map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("config.json is not valid JSON: %v", err)
	}
	if raw["http_port"].(float64) != 80 {
		t.Errorf("default http_port = %v, want 80", raw["http_port"])
	}
	// Open-mode default: no allowed_hosts entries in a fresh config.
	if hosts, ok := raw["allowed_hosts"]; ok {
		if list, isList := hosts.([]any); isList && len(list) > 0 {
			t.Errorf("default allowed_hosts = %v, want empty (open mode)", hosts)
		}
	}
	// The in-memory data dir must not leak into the file.
	if _, leak := raw["dataDir"]; leak {
		t.Error("dataDir leaked into config.json")
	}
}

// hostAllowed lives in routes; this covers the config side: SetAllowedHosts
// normalises and persists the list.
func TestSetAllowedHosts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TIMERPI_DATA_DIR", dir)
	LoadConfig()

	if err := SetAllowedHosts("Show.Example.com, other.net ,, \n"); err != nil {
		t.Fatalf("SetAllowedHosts: %v", err)
	}
	got := AllowedHosts()
	if len(got) != 2 || got[0] != "show.example.com" || got[1] != "other.net" {
		t.Errorf("AllowedHosts = %v, want [show.example.com other.net]", got)
	}
}
