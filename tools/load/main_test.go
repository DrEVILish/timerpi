package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestProbeRejectsBadTargetsAndServerFailures(t *testing.T) {
	for _, base := range []string{"", "file:///tmp/a", "http://user:secret@localhost", "http://localhost/a?key=secret"} {
		if _, err := probe(context.Background(), base, 1); err == nil {
			t.Fatalf("accepted invalid target %q", base)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	if _, err := probe(context.Background(), server.URL, 1); err == nil {
		t.Fatal("server failure reported as success")
	}
}

func TestProbeExercisesRealServer(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "timerpi")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, out)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	cmd := exec.Command(bin)
	cmd.Dir = root
	_, port, _ := net.SplitHostPort(addr)
	cmd.Env = append(os.Environ(), "TIMERPI_ROLE=cloud", "TIMERPI_MESH=off", "TIMERPI_DISPLAY=off", "TIMERPI_DATA_DIR="+filepath.Join(dir, "data"), "TIMERPI_HTTP_PORT="+port)
	log, err := os.Create(filepath.Join(dir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	base := "http://" + addr
	deadline := time.Now().Add(15 * time.Second)
	for {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	n := 8
	if raw := os.Getenv("TP_LOAD_N"); raw != "" {
		n, err = strconv.Atoi(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	report, err := probe(ctx, base, n)
	t.Logf("external probe: %+v", report)
	if err != nil {
		t.Fatalf("real probe: %v; %+v", err, report)
	}
	if report.Joined != n || report.Votes != n || report.FinalTotal != n || report.Delivered != n || report.OperatorSamples < 2 || !report.Cleaned {
		t.Fatalf("incomplete proof: %+v", report)
	}
	if err := assess(report); err != nil {
		t.Fatalf("real probe thresholds: %v; %+v", err, report)
	}

	operator := newClient()
	var existing struct {
		Code string `json:"code"`
	}
	if err := request(ctx, operator, base, "POST", "/api/events", map[string]any{"name": "Owner supplied disposable test event", "password": "test-pw1", "rooms": []string{"Preserve this room"}}, &existing); err != nil {
		t.Fatal(err)
	}
	existingReport, err := probe(ctx, base, 8, eventAccess{Code: existing.Code, Password: "test-pw1"})
	if err != nil {
		t.Fatalf("existing event probe: %v", err)
	}
	if err := assess(existingReport); err != nil {
		t.Fatalf("existing event probe thresholds: %v %+v", err, existingReport)
	}
	var lobby struct {
		Event struct {
			Rooms []struct {
				Name string `json:"name"`
			} `json:"rooms"`
		} `json:"event"`
	}
	if err := request(ctx, operator, base, "GET", "/api/events/"+existing.Code, nil, &lobby); err != nil {
		t.Fatalf("owner event was deleted: %v", err)
	}
	if len(lobby.Event.Rooms) != 1 || lobby.Event.Rooms[0].Name != "Preserve this room" {
		t.Fatalf("owner rooms changed: %+v", lobby)
	}
}

func TestAssessmentRejectsMissingEvidence(t *testing.T) {
	good := report{Clients: 100, Joined: 100, Votes: 100, FinalTotal: 100, Delivered: 100, DeliveryP95MS: 200, OperatorSamples: 4, OperatorP95MS: 20, Cleaned: true}
	if err := assess(good); err != nil {
		t.Fatal(err)
	}
	for _, alter := range []func(*report){
		func(r *report) { r.Joined = 99 }, func(r *report) { r.Disconnected = 1 },
		func(r *report) { r.Votes = 99 }, func(r *report) { r.FinalTotal = 99 },
		func(r *report) { r.Delivered = 98 }, func(r *report) { r.DeliveryP95MS = 0 },
		func(r *report) { r.DeliveryP95MS = 1001 }, func(r *report) { r.OperatorErrors = 1 },
		func(r *report) { r.OperatorP95MS = 251 }, func(r *report) { r.Cleaned = false },
	} {
		r := good
		alter(&r)
		if err := assess(r); err == nil {
			t.Fatalf("missing evidence accepted: %+v", r)
		}
	}
	good.Delivered = 99
	if err := assess(good); err != nil {
		t.Fatalf("99%% gate rejected: %v", err)
	}
}
