// Package buildinfo names this build. Version is set at link time
// (make: -ldflags "-X timerpi/buildinfo.Version=3.1.0"); Proto is the
// wire major that boxes and the cloud must share (VENUE-CLOUD §9).
package buildinfo

import (
	"strconv"
	"strings"
)

// Version is the release version ("3.0.0"); "dev" builds never update.
var Version = "3.0.0-dev"

// Proto is the protocol major. Boxes ignore peers with another value and a
// box older than its peers shows "update needed".
const Proto = 3

// Newer reports whether version a is newer than b (dotted numbers; any
// "-suffix" counts as older than the same release without one).
func Newer(a, b string) bool {
	pa, sa := split(a)
	pb, sb := split(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return sa == "" && sb != ""
}

func split(v string) ([3]int, string) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	suffix := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, suffix = v[:i], v[i+1:]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out, suffix
}
