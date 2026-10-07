// STATUS N13 (VENUE-CLOUD §4): a box shows a 6-digit code; the Event
// Technician types it on the event page; the box's next poll carries its
// screen, its key and its event (with the mesh key).
package routes_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"timerpi/routes"
)

func TestPairBoxByCode(t *testing.T) {
	ts := newAPITest(t)
	reg := `{"name":"box-7","host":"box-7","token":"tok-box-7","code":"482913"}`
	if code, b := ts.anon("POST", "/api/waiting/register", []byte(reg), "application/json"); code != 200 {
		t.Fatalf("register: %d %s", code, b)
	}
	// A box registering without a token or with a bad code is refused.
	for _, bad := range []string{`{"name":"x","host":"x","code":"482913"}`, `{"name":"x","host":"x","token":"t","code":"48291"}`} {
		if code, _ := ts.anon("POST", "/api/waiting/register", []byte(bad), "application/json"); code != 400 {
			t.Errorf("%s accepted: %d", bad, code)
		}
	}
	pair := func(code string) (int, []byte) {
		return ts.call("POST", "/api/events/"+ts.eventCode+"/pair",
			[]byte(fmt.Sprintf(`{"pairCode":%q,"code":%q,"kind":"presenter","template":"dsm","rotation":0,"name":"Stage DSM"}`, code, ts.showCode)), "application/json")
	}
	if code, _ := pair("000000"); code != 404 {
		t.Fatalf("a wrong code paired: %d", code)
	}
	if code, _ := ts.anon("POST", "/api/events/"+ts.eventCode+"/pair", []byte(`{"pairCode":"482913","code":"`+ts.showCode+`","kind":"presenter"}`), "application/json"); code == 200 {
		t.Fatal("paired without the Event Technician")
	}
	if code, b := pair("482 913"); code != 200 || !strings.Contains(string(b), `"name":"Stage DSM"`) {
		t.Fatalf("pair: %d %s", code, b)
	}

	// Somebody polling with the box's name but not its token gets nothing.
	if _, b := ts.anon("GET", "/api/waiting/mine?name=box-7&host=box-7&token=guess", nil, ""); strings.Contains(string(b), "meshKey") {
		t.Fatalf("the mesh key leaked to another poller: %s", b)
	}
	_, b := ts.anon("GET", "/api/waiting/mine?name=box-7&host=box-7&token=tok-box-7", nil, "")
	var mine struct {
		Assigned, Screen, Key string
		Pairing               struct {
			Event, MeshKey, Room string
		}
	}
	_ = json.Unmarshal(b, &mine)
	if mine.Assigned != ts.showCode || mine.Screen != "Stage DSM" || mine.Key == "" {
		t.Fatalf("mine = %s", b)
	}
	if mine.Pairing.Event != ts.eventCode || len(mine.Pairing.MeshKey) != 64 || mine.Pairing.Room != ts.showCode {
		t.Fatalf("pairing = %s", b)
	}
	if !ts.db.ScreenKeyValid(ts.showID, "Stage DSM", mine.Key) {
		t.Fatal("the box's screen key isn't valid")
	}

	// An ordinary browser screen captured from the list never gets the key.
	ts.anon("POST", "/api/waiting/register", []byte(`{"name":"tv-1","host":"tv","token":"tok-tv"}`), "application/json")
	_, lb := ts.call("GET", "/api/waiting", nil, "")
	var list struct{ Waiting []struct{ ID int64 } }
	_ = json.Unmarshal(lb, &list)
	if len(list.Waiting) == 0 {
		t.Fatalf("waiting list: %s", lb)
	}
	ts.call("POST", fmt.Sprintf("/api/waiting/%d/capture", list.Waiting[len(list.Waiting)-1].ID),
		[]byte(`{"code":"`+ts.showCode+`","kind":"audience","template":"main"}`), "application/json")
	if _, b := ts.anon("GET", "/api/waiting/mine?name=tv-1&host=tv&token=tok-tv", nil, ""); !strings.Contains(string(b), `"assigned"`) || strings.Contains(string(b), "meshKey") {
		t.Fatalf("browser screen capture: %s", b)
	}
}

// The box's own screen (/d/box) reads its pairing code from
// /api/pairing/self, which only the box itself may call.
func TestBoxScreenSelf(t *testing.T) {
	ts := newAPITest(t)
	ts.deps.BoxSelf = func() any { return map[string]any{"name": "box-7", "code": "482913"} }
	t.Cleanup(func() { ts.deps.BoxSelf = nil })
	if code, b := ts.anon("GET", "/d/box", nil, ""); code != 200 || !strings.Contains(string(b), "tp-box-code") {
		t.Fatalf("/d/box: %d", code)
	}
	if code, b := ts.anon("GET", "/api/pairing/self", nil, ""); code != 200 || !strings.Contains(string(b), "482913") {
		t.Fatalf("loopback self: %d %s", code, b)
	}
	h := routes.New(ts.deps)
	req := httptest.NewRequest("GET", "/api/pairing/self", nil)
	req.Host = "127.0.0.1"
	req.RemoteAddr = "192.168.1.50:51515"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 || strings.Contains(rec.Body.String(), "482913") {
		t.Fatalf("the code leaked to the LAN: %d %s", rec.Code, rec.Body.String())
	}
}
