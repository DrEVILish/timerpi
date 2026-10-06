package ws

import (
	"math"
	"testing"
)

// BUGLOG RW30: a huge or NaN JSON number is refused, not converted with
// the platform-defined float→int64 rule (MinInt64 on amd64, MaxInt64 on
// the Pi's arm64).
func TestInt64ArgRefusesOutOfRange(t *testing.T) {
	for _, v := range []float64{1e30, -1e30, math.NaN(), math.Inf(1)} {
		if got, ok := int64Arg(map[string]any{"durationMS": v}, "durationMS"); ok {
			t.Errorf("int64Arg(%v) = %d, ok; want refused", v, got)
		}
	}
	if got, ok := int64Arg(map[string]any{"durationMS": 90000.0}, "durationMS"); !ok || got != 90000 {
		t.Errorf("int64Arg(90000) = %d, %v", got, ok)
	}
}
