// Package update is the boot-time updater (VENUE-CLOUD §14, owner
// 2026-10-07).
//
//   - A box looks for a newer build only during the first 5 minutes after
//     the machine boots (/proc/uptime), from the cloud and from other boxes
//     on the mesh. After that it doesn't look again until the next reboot.
//   - Every build carries a manifest signed with the release key (ed25519);
//     the public half is embedded here, so a stranger on the open mesh can't
//     push a binary. A box never installs an older build.
//   - Install: the new binary is written next to the running one, swapped in
//     (the old one kept as .prev) and the process restarts (systemd
//     Restart=always). If the new build fails to come up twice, the next
//     start puts .prev back (Rollback).
//
// Every TimerPi (box or cloud) serves its builds to others:
// GET /api/update/manifest?arch=arm64 and GET /api/update/binary?arch=arm64.
package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"timerpi/buildinfo"
)

// Window is how long after boot a box looks for updates.
const Window = 5 * time.Minute

// releasePub is the base64 ed25519 public key of the release signer
// (tools/release keygen writes it). Empty = this build never updates.
//
//go:embed release.pub
var releasePub string

// Manifest describes one signed build.
type Manifest struct {
	Version string `json:"version"`
	Arch    string `json:"arch"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Sig     string `json:"sig"` // base64 ed25519 over SignedBytes
}

// SignedBytes is what the release key signs.
func (m Manifest) SignedBytes() []byte {
	return []byte("timerpi-update/1\n" + m.Version + "\n" + m.Arch + "\n" + m.SHA256 + "\n" + strconv.FormatInt(m.Size, 10))
}

// Verify checks the signature against key.
func (m Manifest) Verify(key ed25519.PublicKey) error {
	sig, err := base64.StdEncoding.DecodeString(m.Sig)
	if err != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, m.SignedBytes(), sig) {
		return errors.New("update: bad signature")
	}
	return nil
}

// PublicKey returns the embedded release key (nil when none).
func PublicKey() ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(releasePub))
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return b
}

// ManifestPath is where a binary's manifest lives: next to it.
func ManifestPath(bin string) string { return bin + ".manifest.json" }

// ReadManifest loads a manifest file.
func ReadManifest(path string) (Manifest, error) {
	var m Manifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// ---------------------------------------------------------------- serve --

// Build finds the binary and manifest this TimerPi serves for arch: a
// published release in <data>/releases/<arch>/ (the cloud keeps one per
// architecture), else its own binary when the arch matches.
func Build(dataDir, self, arch string) (bin string, m Manifest, err error) {
	if arch == "" || strings.ContainsAny(arch, "/\\.") {
		return "", m, errors.New("update: bad arch")
	}
	if rel := filepath.Join(dataDir, "releases", arch, "timerpi"); fileExists(rel) {
		m, err = ReadManifest(ManifestPath(rel))
		return rel, m, err
	}
	if arch == runtime.GOARCH && self != "" {
		m, err = ReadManifest(ManifestPath(self))
		return self, m, err
	}
	return "", m, os.ErrNotExist
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// ---------------------------------------------------------------- check --

// Checker runs the boot-time check.
type Checker struct {
	Self    string                          // path of the running binary
	Current string                          // running version (buildinfo.Version)
	Arch    string                          // runtime.GOARCH
	Key     ed25519.PublicKey               // release key; nil = disabled
	Sources func() []string                 // base URLs: the cloud, then mesh peers
	Busy    func() bool                     // a timer is running: don't swap now
	Uptime  func() time.Duration            // time since the machine booted
	Restart func()                          // ask the process to restart
	Client  *http.Client                    // nil → 10 s timeout client
	Every   time.Duration                   // poll interval inside the window (20 s)
	Logf    func(string, ...any)            // nil → log.Printf
	Install func(Manifest, io.Reader) error // nil → c.install (tests swap)
}

// Run looks for updates until the boot window closes, installs the newest
// build it finds and restarts. It returns when there is nothing to do.
func (c *Checker) Run(ctx context.Context) {
	logf := c.Logf
	if logf == nil {
		logf = log.Printf
	}
	switch {
	case c.Key == nil:
		logf("update: no release key in this build; boot-time updates are off")
		return
	case strings.Contains(c.Current, "dev"):
		logf("update: development build %s; boot-time updates are off", c.Current)
		return
	}
	every := c.Every
	if every <= 0 {
		every = 20 * time.Second
	}
	for {
		if up := c.Uptime(); up >= Window {
			logf("update: %s since boot; no update until the next reboot", up.Round(time.Second))
			return
		}
		if src, m, ok := c.Find(ctx); ok {
			if c.Busy != nil && c.Busy() {
				logf("update: %s found, waiting for the running timer to stop", m.Version)
			} else if err := c.Apply(ctx, src, m); err != nil {
				logf("update: %s from %s: %v", m.Version, src, err)
			} else {
				logf("update: installed %s from %s; restarting", m.Version, src)
				if c.Restart != nil {
					c.Restart()
				}
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (c *Checker) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Find asks every source and returns the newest valid build above ours
// (also used by the network page's UPDATE button, outside the boot window).
func (c *Checker) Find(ctx context.Context) (string, Manifest, bool) {
	if c.Key == nil {
		return "", Manifest{}, false
	}
	var best Manifest
	bestSrc := ""
	for _, src := range c.Sources() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, src+"/api/update/manifest?arch="+c.Arch, nil)
		if err != nil {
			continue
		}
		resp, err := c.client().Do(req)
		if err != nil {
			continue
		}
		var m Manifest
		err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&m)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || m.Arch != c.Arch || m.Verify(c.Key) != nil {
			continue
		}
		if buildinfo.Newer(m.Version, c.Current) && (bestSrc == "" || buildinfo.Newer(m.Version, best.Version)) {
			best, bestSrc = m, src
		}
	}
	return bestSrc, best, bestSrc != ""
}

// Apply downloads build m from src and installs it (the caller restarts).
func (c *Checker) Apply(ctx context.Context, src string, m Manifest) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src+"/api/update/binary?arch="+c.Arch, nil)
	if err != nil {
		return err
	}
	cl := *c.client()
	cl.Timeout = 5 * time.Minute // a binary over a slow mesh
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	install := c.Install
	if install == nil {
		install = c.install
	}
	return install(m, resp.Body)
}

// install writes the download beside the running binary, checks size and
// hash against the signed manifest, and swaps it in (old one → .prev).
func (c *Checker) install(m Manifest, body io.Reader) error {
	dir := filepath.Dir(c.Self)
	tmp, err := os.CreateTemp(dir, ".timerpi-new-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(body, m.Size+1))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return errors.New("download doesn't match the signed manifest")
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(ManifestPath(tmp.Name()), mb, 0o644); err != nil {
		return err
	}
	defer os.Remove(ManifestPath(tmp.Name()))
	return Swap(c.Self, tmp.Name())
}

// Swap installs newBin as bin, keeping the old binary (and manifest) as
// .prev and marking the update pending until Confirm.
func Swap(bin, newBin string) error {
	prev := bin + ".prev"
	if err := copyFile(bin, prev); err != nil {
		return fmt.Errorf("keep previous: %w", err)
	}
	if fileExists(ManifestPath(bin)) {
		_ = copyFile(ManifestPath(bin), ManifestPath(prev))
	} else {
		_ = os.Remove(ManifestPath(prev))
	}
	if err := os.WriteFile(pendingPath(bin), []byte("0"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(newBin, bin); err != nil {
		os.Remove(pendingPath(bin))
		return err
	}
	if fileExists(ManifestPath(newBin)) {
		_ = copyFile(ManifestPath(newBin), ManifestPath(bin))
	}
	return nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, st.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func pendingPath(bin string) string { return bin + ".pending" }

// MaxTries is how many starts a new build gets before it is rolled back.
const MaxTries = 2

// Rollback runs first thing at startup. After an update the pending marker
// counts starts; a build that hasn't been confirmed after MaxTries starts is
// replaced by .prev, and the caller exits so systemd starts the old build.
func Rollback(bin string) (rolledBack bool, err error) {
	raw, err := os.ReadFile(pendingPath(bin))
	if err != nil {
		return false, nil // no update pending
	}
	tries, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if tries < MaxTries {
		return false, os.WriteFile(pendingPath(bin), []byte(strconv.Itoa(tries+1)), 0o644)
	}
	prev := bin + ".prev"
	if !fileExists(prev) {
		os.Remove(pendingPath(bin))
		return false, errors.New("update: new build failing and no previous build to restore")
	}
	if err := copyFile(prev, bin); err != nil {
		return false, err
	}
	if fileExists(ManifestPath(prev)) {
		_ = copyFile(ManifestPath(prev), ManifestPath(bin))
	}
	os.Remove(pendingPath(bin))
	return true, nil
}

// Confirm marks the running build good (call once it has served for a
// while).
func Confirm(bin string) { os.Remove(pendingPath(bin)) }

// Uptime reads the time since the machine booted.
func Uptime() time.Duration {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return Window // unknown: treat the window as closed
	}
	fields := bytes.Fields(b)
	if len(fields) == 0 {
		return Window
	}
	f, err := strconv.ParseFloat(string(fields[0]), 64)
	if err != nil {
		return Window
	}
	return time.Duration(f * float64(time.Second))
}
