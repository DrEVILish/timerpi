package venue

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"timerpi/buildinfo"
	"timerpi/routes"
	"timerpi/timerpi"
)

func TestOfflineRegistrationRequiresMatchingProtocol(t *testing.T) {
	cloud := newSide(t, "cloud")
	box := newSide(t, "box")
	ev, _, err := box.db.CreateEvent("Offline conference", "test-pass", nil)
	if err != nil {
		t.Fatal(err)
	}
	keyHex, err := box.db.EventMeshKey(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := box.deps.ExportEvent(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"meshKey": keyHex, "bundle": json.RawMessage(raw)})
	if err != nil {
		t.Fatal(err)
	}
	wantMajor := strconv.Itoa(buildinfo.Proto)
	for _, major := range []string{"", strconv.Itoa(buildinfo.Proto + 1)} {
		req, err := http.NewRequest(http.MethodPost, cloud.srv.URL+"/api/link/register", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(routes.LinkAuthHeader, routes.LinkAuth(key, "register", ev.Code, time.Now()))
		req.Header.Set(routes.LinkProtoHeader, major)
		resp, err := cloud.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusConflict || resp.Header.Get(routes.LinkProtoHeader) != wantMajor {
			t.Fatalf("register with protocol %q: got %d, want 409 with protocol %s", major, resp.StatusCode, wantMajor)
		}
		if _, ok := cloud.db.ResolveEvent(ev.Code); ok {
			t.Fatal("incompatible registration created an event")
		}
	}
	a := &Agent{Srv: box.deps, CloudURL: func() string { return cloud.srv.URL }}
	a.link.cloudOK = time.Now().Add(-routes.StableAfter)
	if err := a.dial(context.Background(), Pairing{Event: ev.Code, MeshKey: keyHex}, ev); err != nil {
		t.Fatalf("register through compatible cloud: %v", err)
	}
	if got, ok := cloud.db.ResolveEvent(ev.Code); !ok || got.Home != "venue" {
		t.Fatalf("compatible registration did not copy the event: %+v", got)
	}
}

func TestOfflineRegistrationPreservesPreparedEvent(t *testing.T) {
	cloud := newSide(t, "cloud")
	box := newSide(t, "box")
	prepared, _, err := cloud.db.CreateEvent("Prepared conference", "fixture-password", nil)
	if err != nil {
		t.Fatal(err)
	}
	local, err := box.db.CreateEventWithCode(prepared.Code, "Offline conference", "")
	if err != nil {
		t.Fatal(err)
	}
	keyHex, err := box.db.EventMeshKey(local.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{Srv: box.deps, CloudURL: func() string { return cloud.srv.URL }}
	a.link.cloudOK = time.Now().Add(-routes.StableAfter)
	if err := a.register(context.Background(), Pairing{Event: local.Code, MeshKey: keyHex}, local, key); err == nil {
		t.Fatal("offline registration must refuse a prepared cloud event")
	}
	got, err := cloud.db.GetEvent(prepared.ID)
	if err != nil || got.Name != prepared.Name || got.SuperHash != prepared.SuperHash || got.MeshKey != "" {
		t.Fatalf("prepared event changed: %+v, %v", got, err)
	}
}

func TestCloudRejectsIncompatibleVenueBeforeUpgrade(t *testing.T) {
	cloud := newSide(t, "cloud")
	ev, _, err := cloud.db.CreateEvent("Conference", "test-pass", nil)
	if err != nil {
		t.Fatal(err)
	}
	keyHex, err := cloud.db.EventMeshKey(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatal(err)
	}
	for _, major := range []string{"", strconv.Itoa(buildinfo.Proto + 1)} {
		h := http.Header{routes.LinkAuthHeader: {routes.LinkAuth(key, "link", ev.Code, time.Now())}}
		if major != "" {
			h.Set("X-TimerPi-Proto", major)
		}
		conn, resp, err := websocket.DefaultDialer.Dial(wsURL(cloud.srv.URL)+"/api/link?event="+ev.Code, h)
		if conn != nil {
			conn.Close()
		}
		if resp != nil {
			resp.Body.Close()
		}
		if err == nil || resp == nil || resp.StatusCode != http.StatusConflict {
			t.Fatalf("protocol %q accepted, want 409", major)
		}
	}
	got, ok := cloud.db.ResolveEvent(ev.Code)
	if !ok || got.Home != "" {
		t.Fatal("incompatible venue changed event authority")
	}
}

func TestVenueRequiresCloudHandshakeVersion(t *testing.T) {
	for _, major := range []string{"", strconv.Itoa(buildinfo.Proto + 1)} {
		t.Run("cloud-"+major, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			cloud := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h := http.Header{}
				if major != "" {
					h.Set("X-TimerPi-Proto", major)
				}
				conn, err := upgrader.Upgrade(w, r, h)
				if err == nil {
					defer conn.Close()
					conn.ReadMessage()
				}
			}))
			defer cloud.Close()
			a := &Agent{CloudURL: func() string { return cloud.URL }}
			err := a.dial(context.Background(), Pairing{Event: "ABCDEFGH", MeshKey: "abcd"}, timerpi.Event{})
			if err == nil || a.link.current() != nil {
				if conn := a.link.current(); conn != nil {
					conn.Close()
				}
				t.Fatal("venue installed link without matching cloud protocol")
			}
		})
	}
}
