package routes_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// BUGLOG RW8: images belong to an event. Event B's SuperOperator can't
// see or delete event A's venue map, and a moderator of A can list A's
// images but can't delete them.
func TestAssetsBelongToTheirEvent(t *testing.T) {
	ts := newAPITest(t)
	code, raw := uploadAsset(t, ts, "map-a.png", pngBytes(t))
	if code != 200 {
		t.Fatalf("upload: %d %s", code, raw)
	}
	var up struct{ ID int64 }
	_ = json.Unmarshal([]byte(raw), &up)

	// A stranger runs event B.
	b := newPersona(ts)
	_, body := b.do("POST", "/api/events", `{"name":"B","password":"bbbbbb","rooms":["B1"]}`)
	var evB struct{ Code string }
	_ = json.Unmarshal([]byte(body), &evB)
	if code, list := b.do("GET", "/api/assets?event="+evB.Code, ""); code != 200 || strings.Contains(list, "map-a.png") {
		t.Errorf("event B sees A's image: %d %s", code, list)
	}
	if code, _ := b.do("GET", "/api/assets?event="+ts.eventCode, ""); code != http.StatusUnauthorized {
		t.Errorf("event B listing A: %d, want 401", code)
	}
	if code, _ := b.do("DELETE", fmt.Sprintf("/api/assets/%d", up.ID), ""); code != http.StatusUnauthorized {
		t.Errorf("event B deleting A's map: %d, want 401", code)
	}
	// B can't point its map at A's image either.
	if code, _ := b.do("POST", "/api/events/"+evB.Code+"/map", fmt.Sprintf(`{"assetId":%d}`, up.ID)); code != http.StatusNotFound {
		t.Errorf("event B adopting A's image: %d, want 404", code)
	}
	// A moderator of A lists A's images but can't delete them.
	mod := newPersona(ts)
	mod.do("POST", "/api/events/"+ts.eventCode+"/rooms/"+ts.showCode+"/login", `{"pw":""}`)
	if code, list := mod.do("GET", "/api/assets?room="+ts.showCode, ""); code != 200 || !strings.Contains(list, "map-a.png") {
		t.Errorf("moderator list: %d %s", code, list)
	}
	if code, _ := mod.do("DELETE", fmt.Sprintf("/api/assets/%d", up.ID), ""); code != http.StatusUnauthorized {
		t.Errorf("moderator delete: %d, want 401", code)
	}
	// Uploads must name their scope.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "x.png")
	fw.Write(pngBytes(t))
	w.Close()
	if code, _ := ts.callType("POST", "/api/assets", buf.Bytes(), w.FormDataContentType()); code != http.StatusBadRequest {
		t.Errorf("unscoped upload: %d, want 400", code)
	}
}
