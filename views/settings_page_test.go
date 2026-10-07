package views

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// BUGLOG RW51: the device settings page starts with its doctype (a script
// before it put the page in quirks mode), never writes server text through
// innerHTML, and treats an HTTP error as a failed save.
func TestSettingsPageStandardsAndSafeNotes(t *testing.T) {
	set, err := New("../templates")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	data := map[string]any{"Page": "settings", "Title": "Box", "Override": "auto", "Peers": nil}
	if err := set.Render(&buf, "settings", data); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := strings.TrimSpace(buf.String())
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Errorf("page starts with %.40q, want the doctype first", html)
	}
	if strings.Contains(html, ".innerHTML =") || strings.Contains(html, ".innerHTML=") {
		t.Error("settings page writes innerHTML")
	}
	// The page script lives in settings.js; api() throws on !ok or ok:false.
	js, err := os.ReadFile("../public/src/settings.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "settings.js") || !strings.Contains(string(js), "await api('POST', '/api/network/role'") {
		t.Error("role save does not go through api() (which fails on an HTTP error)")
	}
	if strings.Contains(string(js), "innerHTML") {
		t.Error("settings.js writes innerHTML")
	}
}
