package views

import (
	"bytes"
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
	if !strings.Contains(html, "r.ok && out.ok !== false") {
		t.Error("role save does not branch on the HTTP status")
	}
}
