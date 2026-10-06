package main

import (
	"io/fs"
	"testing"

	"timerpi/views"
)

// STATUS C9 / B8: the binary carries the pages and scripts it was built
// with, and they parse.
func TestEmbeddedWebFiles(t *testing.T) {
	for _, p := range []string{"templates/base.html", "templates/fragments", "public/src/audience.js", "public/css/app.css"} {
		if _, err := fs.Stat(webFiles, p); err != nil {
			t.Errorf("not embedded: %s (%v)", p, err)
		}
	}
	if _, err := views.NewFS(webFiles); err != nil {
		t.Fatalf("embedded templates don't parse: %v", err)
	}
}
