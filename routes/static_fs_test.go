package routes_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	"timerpi/routes"
)

// STATUS C9: with Deps.Public set (the embedded copy in production), static
// files come from it, never from public/ on disk; revisioned paths work.
func TestStaticServesGivenTree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pub := fstest.MapFS{"src/audience.js": {Data: []byte("// built-in copy")}}
	srv := httptest.NewServer(routes.New(&routes.Deps{Public: pub}))
	defer srv.Close()
	for _, p := range []string{"/src/audience.js", "/src/v123/audience.js"} {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || string(b) != "// built-in copy" {
			t.Errorf("%s: %d %q, want the given tree's file", p, res.StatusCode, b)
		}
	}
	if res, _ := http.Get(srv.URL + "/src/timerpi.js"); res.StatusCode != 404 {
		t.Errorf("file only on disk was served: %d", res.StatusCode)
	}
}
