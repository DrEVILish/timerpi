package main

import (
	"testing"

	"timerpi/venue"
)

// The updater reaches mesh peers over IPv4 when it can (link-local works
// without a router), IPv6 otherwise.
func TestPeerURL(t *testing.T) {
	for _, c := range []struct {
		addrs []string
		want  string
	}{
		{[]string{"fe80::1", "169.254.10.4"}, "http://169.254.10.4:80"},
		{[]string{"2001:db8::5"}, "http://[2001:db8::5]:80"},
		{[]string{"not-an-ip"}, ""},
		{nil, ""},
	} {
		if got := venue.PeerURL(c.addrs, 80); got != c.want {
			t.Errorf("peerURL(%v) = %q, want %q", c.addrs, got, c.want)
		}
	}
}
