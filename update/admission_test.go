package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestApplyRefusesTimerStartedDuringDownload(t *testing.T) {
	_, sk, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("new build")
	m := signed(t, sk, "3.1.0", bin)
	var busy, installed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		busy.Store(true)
		_, _ = w.Write(bin)
	}))
	defer srv.Close()
	c := &Checker{Arch: "arm64", Busy: busy.Load, Install: func(Manifest, io.Reader) error { installed.Store(true); return nil }}
	if err := c.Apply(context.Background(), srv.URL, m); err == nil || installed.Load() {
		t.Fatalf("update installed after GO during download: installed=%v err=%v", installed.Load(), err)
	}
}

func TestApplySerializesBootAndManualUpdate(t *testing.T) {
	_, sk, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("new build")
	m := signed(t, sk, "3.1.0", bin)
	srv := source(t, m, bin)
	entered, release := make(chan struct{}), make(chan struct{})
	var installs atomic.Int32
	c := &Checker{Arch: "arm64", Install: func(Manifest, io.Reader) error {
		if installs.Add(1) == 1 {
			close(entered)
			<-release
		}
		return errors.New("test install failure")
	}}
	done := make(chan error, 1)
	go func() { done <- c.Apply(context.Background(), srv.URL, m) }()
	<-entered
	err := c.Apply(context.Background(), srv.URL, m)
	close(release)
	<-done
	if err == nil || installs.Load() != 1 {
		t.Fatalf("overlapping installs: count=%d err=%v", installs.Load(), err)
	}
	if err := c.Apply(context.Background(), srv.URL, m); err == nil || installs.Load() != 2 {
		t.Fatalf("failed install did not permit retry: count=%d err=%v", installs.Load(), err)
	}
}

func TestApplyReservationReleasedOnlyOnFailure(t *testing.T) {
	_, sk, _ := ed25519.GenerateKey(rand.Reader)
	bin := []byte("new build")
	m := signed(t, sk, "3.1.0", bin)
	srv := source(t, m, bin)
	reserved, cancelCount := false, 0
	fail := true
	c := &Checker{Arch: "arm64", Reserve: func() (func(), error) {
		if reserved {
			t.Fatal("overlapping reservations")
		}
		reserved = true
		return func() { reserved = false; cancelCount++ }, nil
	}, Install: func(_ Manifest, r io.Reader) error {
		if !reserved {
			t.Fatal("install without reservation")
		}
		got, err := io.ReadAll(r)
		if err != nil || string(got) != string(bin) {
			t.Fatalf("staged bytes = %q, %v", got, err)
		}
		if fail {
			return errors.New("storage failure")
		}
		return nil
	}}
	if err := c.Apply(context.Background(), srv.URL, m); err == nil || reserved || cancelCount != 1 {
		t.Fatalf("failed install reservation: reserved=%v cancels=%d err=%v", reserved, cancelCount, err)
	}
	fail = false
	if err := c.Apply(context.Background(), srv.URL, m); err != nil || !reserved || cancelCount != 1 {
		t.Fatalf("successful install released restart reservation: reserved=%v cancels=%d err=%v", reserved, cancelCount, err)
	}
	if err := c.Apply(context.Background(), srv.URL, m); !errors.Is(err, ErrInstalling) {
		t.Fatalf("second install admitted before restart: %v", err)
	}
}
