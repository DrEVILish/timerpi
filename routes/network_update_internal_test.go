package routes

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/update"
)

// The UPDATE button (VENUE-CLOUD §14): offered when a newer signed build
// exists, refused while a timer runs, installs and restarts otherwise.
func TestUpdateButton(t *testing.T) {
	pk, sk, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("timerpi 3.1.0")
	h := sha256.Sum256(bin)
	m := update.Manifest{Version: "3.1.0", Arch: "arm64", SHA256: hex.EncodeToString(h[:]), Size: int64(len(bin))}
	m.Sig = base64.StdEncoding.EncodeToString(ed25519.Sign(sk, m.SignedBytes()))
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			_ = json.NewEncoder(w).Encode(m)
		} else {
			_, _ = w.Write(bin)
		}
	}))
	defer src.Close()

	var busy, restarted atomic.Bool
	var installed []byte
	sources := []string{}
	u := &update.Checker{
		Current: "3.0.0", Arch: "arm64", Key: pk,
		Sources: func() []string { return sources },
		Busy:    busy.Load,
		Restart: func() { restarted.Store(true) },
		Install: func(_ update.Manifest, r io.Reader) error { installed, _ = io.ReadAll(r); return nil },
	}
	old := networkDeps.Load()
	InstallNetwork(&NetworkDeps{Updater: u})
	t.Cleanup(func() { networkDeps.Store(old) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/network/update", networkUpdateCheck)
	r.POST("/api/network/update", networkUpdateApply)
	call := func(method string) (int, map[string]any) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/api/network/update", nil))
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	if _, out := call("GET"); out["available"] != "" || out["current"] != "3.0.0" {
		t.Fatalf("nothing published, check says %v", out)
	}
	if code, _ := call("POST"); code != http.StatusNotFound {
		t.Fatalf("UPDATE with nothing newer: %d", code)
	}
	sources = []string{src.URL}
	if _, out := call("GET"); out["available"] != "3.1.0" {
		t.Fatalf("check = %v", out)
	}
	busy.Store(true)
	if code, out := call("POST"); code != http.StatusConflict || installed != nil {
		t.Fatalf("installed under a running timer: %d %v", code, out)
	}
	busy.Store(false)
	if code, out := call("POST"); code != http.StatusOK || out["installed"] != "3.1.0" || string(installed) != string(bin) {
		t.Fatalf("UPDATE: %d %v (%q)", code, out, installed)
	}
	for i := 0; i < 30 && !restarted.Load(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !restarted.Load() {
		t.Fatal("the box didn't restart after the update")
	}
	if code, _ := call("POST"); code != http.StatusConflict {
		t.Fatalf("a second press started another install: %d", code)
	}
	updating.Store(false)
}
