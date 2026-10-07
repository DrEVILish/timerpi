package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func signed(t *testing.T, sk ed25519.PrivateKey, ver string, bin []byte) Manifest {
	t.Helper()
	h := sha256.Sum256(bin)
	m := Manifest{Version: ver, Arch: "arm64", SHA256: hex.EncodeToString(h[:]), Size: int64(len(bin))}
	m.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(sk, m.SignedBytes()))
	return m
}

// source serves one build the way /api/update/* does.
func source(t *testing.T, m Manifest, bin []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/update/manifest":
			_ = json.NewEncoder(w).Encode(m)
		case "/api/update/binary":
			_, _ = w.Write(bin)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestManifestSignature(t *testing.T) {
	pk, sk, _ := ed25519.GenerateKey(rand.Reader)
	m := signed(t, sk, "3.1.0", []byte("bin"))
	if err := m.Verify(pk); err != nil {
		t.Fatal(err)
	}
	m.Version = "9.9.9" // tampered
	if m.Verify(pk) == nil {
		t.Fatal("a tampered manifest verified")
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if signed(t, sk, "3.1.0", []byte("bin")).Verify(other) == nil {
		t.Fatal("verified with the wrong key")
	}
}

// The newest valid build wins; unsigned, older and other-arch builds are
// ignored; the install gets the right bytes; then the process restarts.
func TestCheckerInstallsNewest(t *testing.T) {
	pk, sk, _ := ed25519.GenerateKey(rand.Reader)
	_, rogue, _ := ed25519.GenerateKey(rand.Reader)
	newBin := []byte("timerpi 3.2.0")
	cloud := source(t, signed(t, sk, "3.1.0", []byte("timerpi 3.1.0")), []byte("timerpi 3.1.0"))
	peer := source(t, signed(t, sk, "3.2.0", newBin), newBin)
	evil := source(t, signed(t, rogue, "9.0.0", []byte("evil")), []byte("evil"))
	older := source(t, signed(t, sk, "2.9.0", []byte("old")), []byte("old"))

	var got []byte
	var gotVer string
	restarted := false
	c := &Checker{
		Current: "3.0.0", Arch: "arm64", Key: pk,
		Sources: func() []string { return []string{cloud.URL, evil.URL, peer.URL, older.URL, "http://127.0.0.1:1"} },
		Uptime:  func() time.Duration { return time.Minute },
		Restart: func() { restarted = true },
		Install: func(m Manifest, r io.Reader) error {
			gotVer = m.Version
			got, _ = io.ReadAll(r)
			return nil
		},
		Logf: t.Logf,
	}
	c.Run(context.Background())
	if gotVer != "3.2.0" || !bytes.Equal(got, newBin) || !restarted {
		t.Fatalf("installed %q (%q), restarted=%v", gotVer, got, restarted)
	}
}

// After the boot window nothing is fetched at all.
func TestCheckerWindowClosed(t *testing.T) {
	pk, sk, _ := ed25519.GenerateKey(rand.Reader)
	asked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { asked = true }))
	defer srv.Close()
	_ = sk
	c := &Checker{Current: "3.0.0", Arch: "arm64", Key: pk,
		Sources: func() []string { return []string{srv.URL} },
		Uptime:  func() time.Duration { return Window + time.Second }, Logf: t.Logf}
	c.Run(context.Background())
	if asked {
		t.Fatal("checked for updates after the boot window")
	}
}

// The window closes while searching: Run returns on its own.
func TestCheckerStopsAtWindowEnd(t *testing.T) {
	pk, _, _ := ed25519.GenerateKey(rand.Reader)
	up := 4*time.Minute + 59*time.Second
	calls := 0
	c := &Checker{Current: "3.0.0", Arch: "arm64", Key: pk, Every: time.Millisecond,
		Sources: func() []string { calls++; return nil },
		Uptime:  func() time.Duration { up += time.Second / 2; return up }, Logf: t.Logf}
	done := make(chan struct{})
	go func() { c.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run kept going after the window")
	}
	if calls == 0 {
		t.Fatal("never looked inside the window")
	}
}

// A real install swaps the binary, keeps .prev, and a download that doesn't
// match the manifest is refused.
func TestInstallSwapAndRollback(t *testing.T) {
	_, sk, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	bin := filepath.Join(dir, "timerpi")
	if err := os.WriteFile(bin, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := &Checker{Self: bin}
	m := signed(t, sk, "3.1.0", []byte("new build"))
	if err := c.install(m, bytes.NewReader([]byte("tampered!"))); err == nil {
		t.Fatal("installed a download that doesn't match the manifest")
	}
	if b, _ := os.ReadFile(bin); string(b) != "old build" {
		t.Fatal("a refused download touched the binary")
	}
	if err := c.install(m, bytes.NewReader([]byte("new build"))); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "new build" {
		t.Fatalf("binary = %q", b)
	}
	if b, _ := os.ReadFile(bin + ".prev"); string(b) != "old build" {
		t.Fatalf("prev = %q", b)
	}
	if got, err := ReadManifest(ManifestPath(bin)); err != nil || got.Version != "3.1.0" {
		t.Fatalf("manifest beside the binary = %+v, %v", got, err)
	}

	// Two failed starts are allowed; the third start rolls back.
	for i := 0; i < MaxTries; i++ {
		if rb, err := Rollback(bin); rb || err != nil {
			t.Fatalf("start %d rolled back early (%v)", i+1, err)
		}
	}
	if rb, err := Rollback(bin); !rb || err != nil {
		t.Fatalf("no rollback after %d failed starts (%v)", MaxTries, err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "old build" {
		t.Fatalf("after rollback binary = %q", b)
	}
	if rb, _ := Rollback(bin); rb {
		t.Fatal("rolled back again with nothing pending")
	}
}

// A confirmed build is never rolled back.
func TestConfirmClearsPending(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "timerpi")
	_ = os.WriteFile(bin, []byte("a"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "new"), []byte("b"), 0o755)
	if err := Swap(bin, filepath.Join(dir, "new")); err != nil {
		t.Fatal(err)
	}
	Confirm(bin)
	for i := 0; i < MaxTries+2; i++ {
		if rb, _ := Rollback(bin); rb {
			t.Fatal("rolled back a confirmed build")
		}
	}
}

func TestBuildPrefersPublishedRelease(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "self")
	_ = os.WriteFile(self, []byte("self"), 0o755)
	_ = os.WriteFile(ManifestPath(self), []byte(`{"version":"3.0.0","arch":"arm64"}`), 0o644)
	if _, _, err := Build(dir, self, "../etc"); err == nil {
		t.Fatal("accepted a path in arch")
	}
	rel := filepath.Join(dir, "releases", "arm64")
	_ = os.MkdirAll(rel, 0o755)
	_ = os.WriteFile(filepath.Join(rel, "timerpi"), []byte("rel"), 0o755)
	_ = os.WriteFile(ManifestPath(filepath.Join(rel, "timerpi")), []byte(`{"version":"3.1.0","arch":"arm64"}`), 0o644)
	bin, m, err := Build(dir, self, "arm64")
	if err != nil || m.Version != "3.1.0" || bin != filepath.Join(rel, "timerpi") {
		t.Fatalf("Build = %q %+v %v", bin, m, err)
	}
	if _, _, err := Build(dir, self, "riscv64"); err == nil {
		t.Fatal("served a build for an arch it doesn't have")
	}
}
