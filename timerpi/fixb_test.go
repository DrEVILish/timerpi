package timerpi

import (
	"path/filepath"
	"strings"
	"testing"
)

// Screen names keep letters of any script; controls and symbols go;
// 40 characters at most (E2E REPORT minor: "Bühne" became "Bhne").
func TestSanitizeScreenNameUnicode(t *testing.T) {
	for in, want := range map[string]string{
		"Bühne 1":       "Bühne 1",
		"舞台 左":          "舞台 左",
		"Stage\x00‮<b>": "Stageb",
		"  Café-2_B  ":  "Café-2_B",
	} {
		if got := SanitizeScreenName(in); got != want {
			t.Errorf("SanitizeScreenName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SanitizeScreenName(strings.Repeat("ü", 100)); len([]rune(got)) != MaxScreenNameLen {
		t.Errorf("long name kept %d runes", len([]rune(got)))
	}
}

// Spent nonces persist (phone sign-in codes stay single-use across a
// restart) and expire.
func TestSpendNonce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "n.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := db.SpendNonce("n1", 200, 100); !ok || err != nil {
		t.Fatalf("first spend: %v %v", ok, err)
	}
	db.Close()
	db, _ = Open(path)
	defer db.Close()
	if ok, _ := db.SpendNonce("n1", 200, 150); ok {
		t.Error("nonce spent twice across a reopen")
	}
	if ok, _ := db.SpendNonce("n1", 400, 300); !ok {
		t.Error("expired nonce not dropped")
	}
}

// Screens and events on ftl's tokens/core bundles fall back to the
// default theme on open (REPORT #18).
func TestTokensThemeFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ev, rooms, err := db.CreateEvent("E", "pw-pw-pw", []string{"R"})
	if err != nil {
		t.Fatal(err)
	}
	_ = db.SetScreenConfig(rooms[0].ID, "TV", "tokens", 0, "")
	_ = db.SetEventTheme(ev.ID, "tokens")
	db.Close()
	db, _ = Open(path)
	defer db.Close()
	s, _ := db.GetScreenByName(rooms[0].ID, "TV")
	e, _ := db.GetEvent(ev.ID)
	if s.Theme != "" || e.Theme != "" {
		t.Errorf("tokens kept: screen %q event %q", s.Theme, e.Theme)
	}
}
