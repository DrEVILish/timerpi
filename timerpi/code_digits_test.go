package timerpi

import "testing"

// BUGLOG RS9: NewCode never issues an all-digit code (ResolveShowID would
// refuse it, leaving the room unreachable).
func TestNewCodeNeverAllDigits(t *testing.T) {
	d := openTestDB(t)
	saved := cryptoRand
	defer func() { cryptoRand = saved }()
	calls := 0
	cryptoRand = func(b []byte) (int, error) {
		calls++
		for i := range b {
			if calls == 1 {
				b[i] = byte(indexOf('2')) // first draw: "22222222"
			} else {
				b[i] = byte(indexOf('A'))
			}
		}
		return len(b), nil
	}
	code, err := NewCode(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, numeric := ParseNumericID(code); numeric || calls < 2 {
		t.Errorf("NewCode issued %q after %d draws", code, calls)
	}
}

func indexOf(c byte) int {
	for i := 0; i < len(CodeAlphabet); i++ {
		if CodeAlphabet[i] == c {
			return i
		}
	}
	return -1
}
