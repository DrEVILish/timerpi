package views

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BUGLOG RS38: no doubled conditionals left in the templates.
func TestNoDoubledEditableIf(t *testing.T) {
	files, _ := filepath.Glob("../templates/fragments/*.html")
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "{{if $.P.Editable}}{{if $.P.Editable}}") {
			t.Errorf("%s has a doubled {{if $.P.Editable}}", f)
		}
	}
}
