// views_test.go — formatter unit tests (package had none).
package views

import "testing"

func TestFmtDurSigned(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{400, "0"},        // rounds to zero
		{-400, "0"},       //
		{1000, "+0:01"},   //
		{35000, "+0:35"},  //
		{35499, "+0:35"},  // to-nearest
		{35500, "+0:36"},  //
		{-62000, "−1:02"}, // true minus sign
		{-3_600_000, "−1:00:00"},
	}
	for _, c := range cases {
		if got := FmtDurSigned(c.in); got != c.want {
			t.Errorf("FmtDurSigned(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
