// Phase 2 route tests (PLAN §11.2): assets upload/serve/delete with
// sniffed mimes and size guards, the board-templates endpoint, board PUT
// accepting the new widget types, and the zone-map pointer reaching the
// walk-in page.
package routes_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"strings"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	// 1×1 PNG (crafted once; QR helper proves the encoder exists, this just
	// needs a valid image signature for the sniff).
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1F, 0x15, 0xC4, 0x89, 0, 0, 0, 0x0D, 0x49, 0x44,
		0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D,
		0xB4, 0, 0, 0, 0, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
}

func uploadAsset(t *testing.T, ts *apiTest, name string, body []byte) (int, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", name)
	fw.Write(body)
	w.Close()
	code, raw := ts.callType("POST", "/api/assets", buf.Bytes(), w.FormDataContentType())
	return code, string(raw)
}

func TestAssetsLifecycle(t *testing.T) {
	ts := newAPITest(t)
	// Upload a PNG.
	code, raw := uploadAsset(t, ts, "floorplan.png", pngBytes(t))
	if code != 200 {
		t.Fatalf("upload: %d %s", code, raw)
	}
	var out struct {
		OK  bool   `json:"ok"`
		ID  int64  `json:"id"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || !out.OK || out.ID <= 0 {
		t.Fatalf("upload body: %s", raw)
	}
	// Public read: no auth, immutable cache, sniffed mime.
	code, raw2 := ts.call("GET", out.URL, nil, "")
	if code != 200 || !bytes.Equal(raw2, pngBytes(t)) {
		t.Fatalf("asset get: %d %dB", code, len(raw2))
	}
	// Non-image refused (sniffed, not extension-trusted).
	if code, _ := uploadAsset(t, ts, "notanimage.png", []byte("hello, this is text")); code != 400 {
		t.Errorf("text upload accepted: %d", code)
	}
	// Oversize refused.
	if code, _ := uploadAsset(t, ts, "big.png", bytes.Repeat(pngBytes(t), 70000)); code != 400 {
		t.Errorf("oversize accepted: %d", code)
	}
	// Delete; then 404 on re-read AND re-delete.
	if code, _ := ts.call("DELETE", fmt.Sprintf("/api/assets/%d", out.ID), nil, ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := ts.call("GET", out.URL, nil, ""); code != 404 {
		t.Errorf("deleted asset still served: %d", code)
	}
	if code, _ := ts.call("DELETE", fmt.Sprintf("/api/assets/%d", out.ID), nil, ""); code != 404 {
		t.Errorf("double delete: %d", code)
	}
	// Bad ids never 500.
	if code, _ := ts.call("GET", "/assets/0", nil, ""); code != 404 {
		t.Errorf("zero id: %d", code)
	}
}

func TestBoardTemplatesEndpoint(t *testing.T) {
	ts := newAPITest(t)
	code, b := ts.call("GET", "/api/board-templates", nil, "")
	if code != 200 {
		t.Fatalf("templates: %d %s", code, b)
	}
	s := string(b)
	for _, name := range []string{`"event"`, `"room"`, `"main"`, `"dsm"`, `"joinqr"`, `"poll"`} {
		if !strings.Contains(s, name) {
			t.Errorf("templates missing %s", name)
		}
	}
}

func TestBoardPUTAcceptsRoomsTypes(t *testing.T) {
	ts := newAPITest(t)
	// Create a board, then store a layout using the new types.
	code, b := ts.call("POST", "/api/shows/"+ts.showCode+"/boards", []byte(`{"name":"Main"}`), "")
	if code != 201 {
		t.Fatalf("board create: %d %s", code, b)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(b, &created); err != nil || created.ID <= 0 {
		t.Fatalf("create body: %s", b)
	}
	layout := `{"layout":{"v":1,"widgets":[
		{"id":"poll","type":"poll","x":0,"y":0,"w":7,"h":5},
		{"id":"qa","type":"qa","x":7,"y":0,"w":5,"h":2},
		{"id":"wordcloud","type":"wordcloud","x":7,"y":2,"w":5,"h":3},
		{"id":"joinqr","type":"joinqr","x":0,"y":5,"w":3,"h":3},
		{"id":"map","type":"map","x":3,"y":5,"w":9,"h":3,"opts":{"assetId":"12"}}]}}`
	if code, b := ts.call("PUT", fmt.Sprintf("/api/shows/%s/boards/%d", ts.showCode, created.ID),
		[]byte(layout), ""); code != 200 {
		t.Fatalf("board put: %d %s", code, b)
	}
	// The board page renders the new tiles (fragments exist).
	code, b = ts.call("GET", "/d/"+ts.showCode+"?view=board", nil, "")
	if code != 200 {
		t.Fatalf("board page: %d %.200s", code, b)
	}
	s := string(b)
	for _, sub := range []string{`data-widget="poll"`, `data-widget="qa"`,
		`data-widget="wordcloud"`, `data-widget="map"`, `data-widget="joinqr"`} {
		if !strings.Contains(s, sub) {
			t.Errorf("board page missing %s", sub)
		}
	}
	// Overlapping new-type tiles are rejected like any other layout.
	_ = code
	bad := `{"layout":{"v":1,"widgets":[
		{"id":"poll","type":"poll","x":0,"y":0,"w":7,"h":5},
		{"id":"qa","type":"qa","x":0,"y":0,"w":5,"h":2}]}}`
	if ocode, _ := ts.call("PUT", fmt.Sprintf("/api/shows/%s/boards/%d", ts.showCode, created.ID),
		[]byte(bad), ""); ocode != 400 {
		t.Errorf("overlap accepted: %d", ocode)
	}
}

func TestZoneMapReachesPage(t *testing.T) {
	ts := newAPITest(t)
	if err := ts.db.SetShowZone(ts.showID, "Hall B"); err != nil {
		t.Fatalf("zone: %v", err)
	}
	code, raw := uploadAsset(t, ts, "map.png", pngBytes(t))
	if code != 200 {
		t.Fatalf("upload: %d %s", code, raw)
	}
	var up struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal([]byte(raw), &up)
	if code, b := ts.call("POST", "/api/zone-map",
		[]byte(fmt.Sprintf(`{"zone":"Hall B","assetId":%d}`, up.ID)), ""); code != 200 {
		t.Fatalf("zone-map: %d %s", code, b)
	}
	code, b := ts.call("GET", "/zone/Hall%20B", nil, "")
	if code != 200 || !strings.Contains(string(b), fmt.Sprintf(`src="/assets/%d"`, up.ID)) {
		t.Fatalf("zone page map: %d %.200s", code, b)
	}
	// Clearing removes it.
	if code, _ := ts.call("POST", "/api/zone-map", []byte(`{"zone":"Hall B","assetId":0}`), ""); code != 200 {
		t.Fatalf("zone-map clear: %d", code)
	}
	if _, b := ts.call("GET", "/zone/Hall%20B", nil, ""); strings.Contains(string(b), "assets/") {
		t.Fatalf("cleared map still on page: %.200s", b)
	}
}
