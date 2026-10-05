package routes_test

import (
	"net/http"
	"strings"
	"testing"
)

// E2 notice tile end-to-end: PUT accepts the type, the board renders its
// (escaped) text, over-long text truncates at the 256-char opts cap, and
// the edit palette offers it.
func TestNoticeWidgetE2(t *testing.T) {
	ts := newAPITest(t)
	list := ts.boardsList(t)
	bid := itoa(int64(list[0]["id"].(float64)))

	long := strings.Repeat("W", 300)
	upd := `{"layout":{"v":1,"widgets":[` +
		`{"id":"n","type":"notice","x":0,"y":0,"w":6,"h":2,"opts":{"text":"Welcome <b>friends</b>"}},` +
		`{"id":"c","type":"countdown","x":6,"y":0,"w":6,"h":2},` +
		`{"id":"long","type":"notice","x":0,"y":2,"w":6,"h":2,"opts":{"text":"` + long + `"}}]}}`
	if code, body := ts.call("PUT", "/api/shows/"+ts.showCode+"/boards/"+bid, []byte(upd), "application/json"); code != http.StatusOK {
		t.Fatalf("notice layout: %d %s", code, body)
	}

	code, body := ts.call("GET", "/d/"+ts.showCode+"?view=board", nil, "")
	if code != http.StatusOK {
		t.Fatalf("board view: %d", code)
	}
	s := string(body)
	if !strings.Contains(s, `data-widget="notice"`) || !strings.Contains(s, `Welcome &lt;b&gt;friends&lt;/b&gt;`) {
		t.Errorf("notice tile missing or unescaped: %.400s", s)
	}
	if strings.Contains(s, long) {
		t.Error("300-char notice text not truncated to the opts cap")
	}

	code, body = ts.call("GET", "/d/"+ts.showCode+"?view=board&edit=1", nil, "")
	if code != http.StatusOK || !strings.Contains(string(body), `data-add="notice"`) {
		t.Errorf("edit palette missing notice: %d", code)
	}
}
