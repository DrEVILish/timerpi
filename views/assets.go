package views

// assets.go — the ONE static-asset cache-busting scheme. Every page links
// JS/CSS through the `asset` template func, which inserts a content-derived
// revision directory: /src/timerpi.js → /src/v<rev>/timerpi.js. The static
// handler (routes.registerStatic) strips the /v<rev>/ segment again. Because
// the revision is a PATH segment, relative ES-module imports inside the JS
// (`./mesh.js`) inherit it automatically, and caches that ignore query
// strings still see a new URL after every update.

import (
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var (
	revOnce sync.Once
	rev     string
)

// SetPublicDir computes the revision from the public/ tree. Called by New
// (templates/ and public/ are siblings in the checkout); safe to call again.
func SetPublicDir(dir string) {
	revOnce.Do(func() { rev = computeRev(dir) })
}

// AssetRev is the current revision segment ("v1234567890").
func AssetRev() string {
	if rev == "" {
		return "v0"
	}
	return rev
}

var assetPath = regexp.MustCompile(`^/(css|src|img)/(.+)$`)

// Asset maps a public path to its revisioned URL. Paths outside
// /css, /src, /img pass through untouched.
func Asset(p string) string {
	m := assetPath.FindStringSubmatch(p)
	if m == nil {
		return p
	}
	return "/" + m[1] + "/" + AssetRev() + "/" + m[2]
}

func computeRev(dir string) string {
	h := fnv.New64a()
	var files []string
	for _, sub := range []string{"src", "css"} {
		_ = filepath.WalkDir(filepath.Join(dir, sub), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files = append(files, p)
			}
			return nil
		})
	}
	sort.Strings(files)
	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(h, strings.TrimPrefix(p, dir))
		_, _ = io.Copy(h, f)
		f.Close()
	}
	return fmt.Sprintf("v%d", h.Sum64()%10000000000)
}
