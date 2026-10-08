package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// A handheld's tp_orient cookie picks the layout version when the URL has
// no ?orient= (so a phone loads once); ?orient= and the editor win.
func TestBoardPortraitCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pick := func(target, cookie string, rot int) bool {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, target, nil)
		if cookie != "" {
			c.Request.AddCookie(&http.Cookie{Name: "tp_orient", Value: cookie})
		}
		return boardPortrait(c, rot)
	}
	if !pick("/d/X?view=board", "portrait", 0) {
		t.Error("cookie portrait ignored")
	}
	if pick("/d/X?view=board&orient=landscape", "portrait", 0) {
		t.Error("?orient= must win over the cookie")
	}
	if pick("/d/X?view=board&edit=1", "portrait", 0) {
		t.Error("the editor must not follow the cookie")
	}
	if !pick("/d/X?view=board", "", 90) {
		t.Error("no cookie: Mounted rotation decides")
	}
}
