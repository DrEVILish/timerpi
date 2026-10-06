package mesh

import (
	"testing"

	"timerpi/mdns"
)

// BUGLOG RS22: a peer URL prefers IPv4 and brackets IPv6.
func TestPeerBaseURL(t *testing.T) {
	cases := []struct {
		addrs []string
		want  string
	}{
		{[]string{"fe80::1", "192.168.1.20"}, "http://192.168.1.20:80"},
		{[]string{"fe80::1"}, "http://[fe80::1]:80"},
		{nil, "http://pi-a:80"},
	}
	for _, c := range cases {
		if got := peerBaseURL(mdns.Peer{Host: "pi-a", Addrs: c.addrs, Port: 80}); got != c.want {
			t.Errorf("addrs %v → %q, want %q", c.addrs, got, c.want)
		}
	}
}
