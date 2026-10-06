package routes_test

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"testing"
)

// A 1×1 PNG.
var tinyPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

// BUGLOG RC4: a show bundle's declared asset mime is never trusted. An
// HTML "asset" is dropped on import; a real image is stored with its
// sniffed type; and /assets/* is never served as a page, including rows
// stored before the fix.
func TestBundleAssetMimeIsSniffed(t *testing.T) {
	ts := newAPITest(t)
	html := base64.StdEncoding.EncodeToString([]byte("<html><script>alert(1)</script></html>"))
	png := base64.StdEncoding.EncodeToString(tinyPNG)
	bundle := `{"manifestVersion":2,"show":{"title":"Evil"},"cues":[],
		"assets":[
			{"id":1,"name":"x.html","mime":"text/html","data":"data:text/html;base64,` + html + `"},
			{"id":2,"name":"logo","mime":"text/html","data":"data:image/png;base64,` + png + `"}
		]}`
	if code, b := ts.call("POST", "/api/events/"+ts.eventCode+"/rooms/import", []byte(bundle), ""); code != http.StatusCreated {
		t.Fatalf("import: %d %.200s", code, b)
	}
	list, err := ts.db.ListAssets()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Mime != "image/png" {
		t.Fatalf("stored assets = %+v, want only the png, typed image/png", list)
	}
	res, err := http.Get(ts.srv.URL + "/assets/" + strconv.FormatInt(list[0].ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("served type %q", ct)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}

	// A row stored by the old import path still can't render as a page.
	legacy, err := ts.db.CreateAsset("old.html", "text/html", []byte("<script>alert(1)</script>"))
	if err != nil {
		t.Fatal(err)
	}
	res, err = http.Get(ts.srv.URL + "/assets/" + strconv.FormatInt(legacy.ID, 10))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "application/octet-stream" || res.Header.Get("Content-Disposition") != "attachment" {
		t.Errorf("legacy html asset served as %q (disposition %q)", ct, res.Header.Get("Content-Disposition"))
	}
}
