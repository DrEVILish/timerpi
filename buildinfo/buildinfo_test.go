package buildinfo

import "testing"

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"3.0.1", "3.0.0", true},
		{"3.0.0", "3.0.1", false},
		{"3.10.0", "3.9.9", true},
		{"3.0.0", "3.0.0", false},
		{"3.0.0", "3.0.0-dev", true},
		{"3.0.0-dev", "3.0.0", false},
		{"v4.0.0", "3.99.0", true},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
