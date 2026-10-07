package routes

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"timerpi/mesh"
	"timerpi/meshradio"
	"timerpi/views"
)

// The network page shows the mesh radios, the other boxes, peer versions
// and the "update needed" banner (VENUE-CLOUD §11, N19).
func TestNetworkFragShowsMesh(t *testing.T) {
	old := meshStatusFile
	meshStatusFile = filepath.Join(t.TempDir(), "mesh.json")
	t.Cleanup(func() { meshStatusFile = old })
	_ = meshradio.Save(meshStatusFile, meshradio.Status{
		Country: "GB", Wired: true, Gateway: "server",
		Legs: []meshradio.Leg{
			{Iface: "wlan0", Band: "2.4 GHz", Channel: 13},
			{Iface: "wlan1", USB: true, Band: "5 GHz", Channel: 36, Error: "IBSS refused"},
		},
		Originators: []meshradio.Originator{{MAC: "dc:a6:32:00:00:02", TQ: 240, Via: "dc:a6:32:00:00:02", Iface: "wlan0", SeenS: "0.5"}},
	})
	tmpl, err := views.New("../templates")
	if err != nil {
		t.Fatal(err)
	}
	vm := buildNetVM(&NetworkDeps{Tmpl: tmpl}, mesh.Identity{Hostname: "pi-a", UpdateNeeded: true},
		[]mesh.PeerView{{Host: "pi-b", Role: "primary", Ver: "4.0.0", Foreign: true}})
	var buf bytes.Buffer
	if err := tmpl.Render(&buf, "frag-network", vm); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{"wlan0", "built-in", "wlan1", "USB", "5 GHz", "IBSS refused", "dc:a6:32:00:00:02", "240/255", "direct",
		"Wired: this box bridges", "newer TimerPi", "4.0.0", "other version"} {
		if !strings.Contains(html, want) {
			t.Errorf("network fragment missing %q", want)
		}
	}
	if strings.Contains(html, "PRIMARY</span>") {
		t.Error("a foreign box is shown as this mesh's primary")
	}
	buf.Reset()
	if err := tmpl.Render(&buf, "settings", vm); err != nil || !strings.Contains(buf.String(), `id="mesh-form"`) {
		t.Fatalf("settings page: %v", err)
	}
}
