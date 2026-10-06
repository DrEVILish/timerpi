package timerpi

import (
	"bytes"
	"sync"
	"testing"
)

// BUGLOG RS3: concurrent first callers agree on one session secret, and it
// survives a reopen (it is stored once, then served from memory).
func TestSessionSecretCreatedOnce(t *testing.T) {
	d := openTestDB(t)
	var wg sync.WaitGroup
	keys := make([][]byte, 16)
	for i := range keys {
		wg.Add(1)
		go func(i int) { defer wg.Done(); keys[i] = d.SessionSecret() }(i)
	}
	wg.Wait()
	for _, k := range keys[1:] {
		if !bytes.Equal(k, keys[0]) {
			t.Fatal("concurrent callers got different secrets")
		}
	}
	d.secret = nil // as after a restart: read back from the store
	if !bytes.Equal(d.SessionSecret(), keys[0]) {
		t.Error("the stored secret changed")
	}
}
