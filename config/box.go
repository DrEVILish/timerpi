package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// Venue mesh defaults (owner, 2026-10-07).
const (
	DefaultWifiCountry   = "GB"
	DefaultMeshChannel24 = 13
	DefaultMeshChannel5  = 36
)

// Mesh5Channels are the 5 GHz channels a box may use: the non-DFS 20 MHz
// channels, so IBSS can start without radar detection.
var Mesh5Channels = []int{36, 40, 44, 48}

// IsCloud reports whether this process is the cloud server.
func IsCloud() bool { return Role() == "cloud" }

// Role is "cloud" or "box"; TIMERPI_ROLE beats the file.
func Role() string {
	r := strings.ToLower(strings.TrimSpace(os.Getenv("TIMERPI_ROLE")))
	if r == "" {
		confMu.RLock()
		r = strings.ToLower(strings.TrimSpace(conf.Role))
		confMu.RUnlock()
	}
	if r == "cloud" {
		return "cloud"
	}
	return "box"
}

// MeshRadio returns the Wi-Fi country and the 2.4 / 5 GHz channels.
func MeshRadio() (country string, ch24, ch5 int) {
	confMu.RLock()
	defer confMu.RUnlock()
	country, ch24, ch5 = conf.WifiCountry, conf.MeshChannel24, conf.MeshChannel5
	if country == "" {
		country = DefaultWifiCountry
	}
	if ch24 == 0 {
		ch24 = DefaultMeshChannel24
	}
	if ch5 == 0 {
		ch5 = DefaultMeshChannel5
	}
	return country, ch24, ch5
}

// SetMeshRadio validates and persists the radio settings.
func SetMeshRadio(country string, ch24, ch5 int) error {
	country = strings.ToUpper(strings.TrimSpace(country))
	if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
		return fmt.Errorf("the Wi-Fi country is a two-letter code, like GB")
	}
	if ch24 < 1 || ch24 > 13 {
		return fmt.Errorf("the 2.4 GHz channel is 1 to 13")
	}
	if !slices.Contains(Mesh5Channels, ch5) {
		return fmt.Errorf("the 5 GHz channel is one of 36, 40, 44 or 48")
	}
	confMu.Lock()
	conf.WifiCountry, conf.MeshChannel24, conf.MeshChannel5 = country, ch24, ch5
	confMu.Unlock()
	return SaveConfig()
}

// CloudURL is the cloud's base URL ("" = none); TIMERPI_CLOUD_URL wins.
func CloudURL() string {
	if u := strings.TrimSpace(os.Getenv("TIMERPI_CLOUD_URL")); u != "" {
		return strings.TrimRight(u, "/")
	}
	confMu.RLock()
	defer confMu.RUnlock()
	return strings.TrimRight(strings.TrimSpace(conf.CloudURL), "/")
}
