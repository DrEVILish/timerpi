// Package config loads and saves TimerPi's JSON configuration.
//
// The config lives in the data directory (/var/lib/timerpi on the Pi,
// ./data in development via TIMERPI_DATA_DIR) so settings survive on the
// persistent data disk. Writes are atomic (temp file + rename, mode 0600)
// so a crash or power cut mid-write can never leave a truncated
// config.json behind.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Config is the on-disk configuration. JSON tags use snake_case; unknown
// fields are ignored on load so a config written by a newer build keeps
// working.
type Config struct {
	// HTTPPort is the single HTTP+WS port. Default 80; the
	// TIMERPI_HTTP_PORT env var overrides it at launch (legacy name
	// CAPACITIMER_HTTP_PORT is still honoured as a fallback).
	HTTPPort int `json:"http_port"`
	// Title is the operator-facing show/site title shown in the UI.
	Title string `json:"title,omitempty"`
	// DeviceName is this appliance's display name (mesh/UI identity). The
	// hostname is the default; changing DeviceName in the UI is the
	// operator's rename request (the hostname/mDNS work in the mesh agent
	// applies it).
	DeviceName string `json:"device_name,omitempty"`
	// Deprecated: the old appliance operator password. Ignored since the
	// event model (supervisor + room passwords); kept so older config
	// files still load.
	AuthPassword string `json:"auth_password,omitempty"`
	// DefaultTheme is the appliance-wide ftl theme used by surfaces that
	// have nothing stored in the browser (B7: server-side defaults).
	DefaultTheme string `json:"default_theme,omitempty"`
	// AllowedHosts is the DNS-rebinding guard's Host allow-list
	// (routes/origin.go). EMPTY (the default) = OPEN mode: the server
	// answers to ANY Host, so reverse proxies and arbitrary custom domains
	// work out of the box. Non-empty = STRICT mode: only the listed names
	// (plus always-local hosts: IP literals, localhost, dotless names,
	// *.local/*.lan-style suffixes, the machine hostname) pass; anything
	// else gets HTTP 421. List proxy domains here when the appliance is
	// exposed to a network where rebinding matters.
	AllowedHosts []string `json:"allowed_hosts,omitempty"`

	// dataDir is the resolved data directory this config was loaded from.
	// Unexported (JSON ignores it) and never persisted: the data dir is
	// chosen at launch (env / default), not by the file.
	dataDir string
}

// conf is read by handlers (and the origin middleware) on their own
// goroutines while settings POSTs mutate it, so all access rides an
// RWMutex. LoadConfig/SaveConfig take the lock themselves; setters mutate
// under Lock, then persist via SaveConfig.
var (
	conf   Config
	confMu sync.RWMutex
)

const (
	defaultDataDir = "/var/lib/timerpi" // systemd unit's WorkingDirectory/data home
	defaultPort    = 80                 // single port; dev overrides via env
	defaultTitle   = "TimerPi"
	defaultDevDir  = "data"
)

// configFile returns the config.json path inside dir.
func configFile(dir string) string {
	return filepath.Join(dir, "config.json")
}

// resolveDataDir picks the data directory before anything else is loaded:
// TIMERPI_DATA_DIR (dev override) beats the /var/lib/timerpi default.
func resolveDataDir(getenv func(string) string) string {
	if d := strings.TrimSpace(getenv("TIMERPI_DATA_DIR")); d != "" {
		return d
	}
	return defaultDataDir
}

// defaults computes the in-memory default Config (before the file load).
func defaults(getenv func(string) string) Config {
	var c Config
	c.HTTPPort = defaultPort
	if raw := portEnv(getenv); raw != "" {
		if p, err := strconv.Atoi(raw); err == nil && p > 0 && p < 65536 {
			c.HTTPPort = p
		}
	}
	c.Title = defaultTitle
	if hn, err := os.Hostname(); err == nil && hn != "" {
		c.DeviceName = hn
	}
	return c
}

// LoadConfig reads config.json from the data directory (creating both on
// first run) and applies launch-time env overrides. Environment overrides
// are intentional launch settings and are reapplied after the file load so
// a stale persisted port cannot silently replace them.
func LoadConfig() {
	dir := resolveDataDir(os.Getenv)

	confMu.Lock()
	defer confMu.Unlock()

	conf = defaults(os.Getenv)
	conf.dataDir = dir

	if err := os.MkdirAll(dir, 0o755); err != nil {
		println("timerpi: creating data dir:", err.Error())
	}

	loaded := false
	if b, err := os.ReadFile(configFile(dir)); err == nil {
		if jerr := json.Unmarshal(b, &conf); jerr != nil {
			println("timerpi: reading config file:", jerr.Error())
		}
		loaded = true
	}

	// Env overrides win over the file.
	if raw := portEnv(os.Getenv); raw != "" {
		if p, err := strconv.Atoi(raw); err == nil && p > 0 && p < 65536 {
			conf.HTTPPort = p
		}
	}
	if d := strings.TrimSpace(os.Getenv("TIMERPI_DATA_DIR")); d != "" {
		conf.dataDir = d
	}

	// Defaults for fields an old/partial config may not carry.
	if conf.HTTPPort < 1 || conf.HTTPPort > 65535 {
		conf.HTTPPort = defaultPort
	}
	if strings.TrimSpace(conf.Title) == "" {
		conf.Title = defaultTitle
	}

	// First run: persist the defaults so the operator has a file to edit.
	if !loaded {
		if err := saveLocked(); err != nil {
			println("timerpi: writing default config:", err.Error())
		}
	}
}

// SaveConfig atomically writes the current in-memory configuration.
func SaveConfig() error {
	confMu.RLock()
	defer confMu.RUnlock()
	return saveLocked()
}

// saveLocked is the lock-free body of SaveConfig; callers already holding
// confMu use it directly.
//
// Atomic: encode to a unique temp sidecar in the same directory and rename
// over config.json, so a crash mid-write cannot leave a truncated file.
// os.CreateTemp gives the sidecar 0600, and the rename preserves it — the
// password lives in here, so the file must not be world-readable.
func saveLocked() error {
	dir := conf.dataDir
	if dir == "" {
		dir = resolveDataDir(os.Getenv)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating data dir: %w", err)
	}

	file, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating config file: %w", err)
	}
	tmpPath := file.Name()
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	err = enc.Encode(sanitized(conf))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("writing config file: %w", err)
	}
	// The password lives in here: 0600 before the rename makes it land
	// with the right mode atomically.
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod config file: %w", err)
	}
	if err := os.Rename(tmpPath, configFile(dir)); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replacing config file: %w", err)
	}
	return nil
}

// sanitized returns a copy safe to persist: the in-memory dataDir (launch
// decision) must not leak into config.json.
func sanitized(c Config) Config {
	c.dataDir = ""
	if c.AllowedHosts == nil {
		c.AllowedHosts = []string{}
	}
	return c
}

// DataDir returns the resolved data directory (config + db live here).
func DataDir() string {
	confMu.RLock()
	defer confMu.RUnlock()
	if conf.dataDir != "" {
		return conf.dataDir
	}
	return resolveDataDir(os.Getenv)
}

// HTTPPort returns the configured port.
func HTTPPort() int {
	confMu.RLock()
	defer confMu.RUnlock()
	return conf.HTTPPort
}

// Title returns the operator-facing title.
func Title() string {
	confMu.RLock()
	defer confMu.RUnlock()
	return conf.Title
}

// SetTitle validates and persists the UI title.
func SetTitle(t string) error {
	t = strings.TrimSpace(t)
	if t == "" {
		return fmt.Errorf("title must not be empty")
	}
	if len(t) > 120 {
		return fmt.Errorf("title too long (max 120 characters)")
	}
	confMu.Lock()
	conf.Title = t
	confMu.Unlock()
	return SaveConfig()
}

// DeviceName returns this appliance's display name (defaults to hostname).
func DeviceName() string {
	confMu.RLock()
	defer confMu.RUnlock()
	return conf.DeviceName
}

// SetDeviceName validates and persists the device display name (persist
// ONLY; renaming the host + re-announcing mDNS is the network agent's job).
func SetDeviceName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("device name must not be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("device name too long (max 64 characters)")
	}
	confMu.Lock()
	conf.DeviceName = name
	confMu.Unlock()
	return SaveConfig()
}



// DefaultTheme returns the appliance default theme - what operator pages
// fall back to when the browser has nothing stored.
// Product default is BLUE-FUTURE (deep-space navy HUD; the owner asked for
// it over the catalog's base "xbmc" always): empty config → blue-future.
func DefaultTheme() string {
	confMu.RLock()
	defer confMu.RUnlock()
	if name := sanitizeTheme(conf.DefaultTheme); name != "" {
		return name
	}
	return "blue-future"
}

// SetDefaultTheme persists the appliance default theme (” resets); the name
// is sanity-checked to the same charset the ftl dist bundle filenames use.
func SetDefaultTheme(name string) error {
	confMu.Lock()
	conf.DefaultTheme = sanitizeTheme(name)
	confMu.Unlock()
	return SaveConfig()
}

func sanitizeTheme(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "xbmc" {
		return ""
	}
	if len(name) > 32 {
		return ""
	}
	// Charset matches the ftl dist bundle filenames (lowercase alnum - _).
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return ""
		}
	}
	return name
}


// AllowedHosts returns the operator-configured extra Host names (copy).
func AllowedHosts() []string {
	confMu.RLock()
	defer confMu.RUnlock()
	return append([]string(nil), conf.AllowedHosts...)
}

// ---------------------------------------------------------------------------
// Operator-password session tokens (routes/auth.go + ws join frames)

// authEntropy is the token domain separator: changing it (deliberately, per
// release) invalidates every issued session cookie at once.
const authEntropy = "timerpi/auth/v1"





// SetAllowedHosts validates and persists the Host allow-list
// (whitespace/comma separated input, as from a settings form).
//
// An EMPTY result switches the origin guard to open mode (any Host is
// accepted — the default); a non-empty list switches to strict mode where
// only listed names (plus always-local hosts) pass and everything else
// gets HTTP 421.
func SetAllowedHosts(raw string) error {
	var hosts []string
	for _, h := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		hosts = append(hosts, h)
	}
	confMu.Lock()
	conf.AllowedHosts = hosts
	confMu.Unlock()
	return SaveConfig()
}

// ConfigFilePath returns where config.json lives (for help text/UI).
func ConfigFilePath() string {
	return configFile(DataDir())
}

// portEnv reads the port override: TIMERPI_HTTP_PORT, else the legacy
// CAPACITIMER_HTTP_PORT (older unit files still set it).
func portEnv(getenv func(string) string) string {
	if v := getenv("TIMERPI_HTTP_PORT"); v != "" {
		return v
	}
	return getenv("CAPACITIMER_HTTP_PORT")
}
