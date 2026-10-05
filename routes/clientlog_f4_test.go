package routes_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// F4 client error reports: batched entries store (capped tail) and serve;
// empty/garbage bodies are 400s.
func TestClientLogF4(t *testing.T) {
	ts := newAPITest(t)
	path := "/api/shows/" + ts.showCode + "/client-log"

	if code, _ := ts.call("POST", path, []byte(`{}`), "application/json"); code != http.StatusBadRequest {
		t.Errorf("empty entries: %d, want 400", code)
	}
	if code, _ := ts.call("POST", path, []byte(`{"entries":[]}`), "application/json"); code != http.StatusBadRequest {
		t.Errorf("zero entries: %d, want 400", code)
	}
	if code, _ := ts.call("POST", path, []byte(`{"entries":[{"kind":"","message":"  "}]}`), "application/json"); code != http.StatusOK {
		t.Errorf("blank-message batch: %d, want 200 (skipped, not fatal)", code)
	}

	body := `{"entries":[
		{"kind":"error","message":"TypeError: x is null","source":"timerpi.js:100:5"},
		{"kind":"unhandledrejection","message":"boom","source":""}]}`
	code, resp := ts.call("POST", path, []byte(body), "application/json")
	if code != http.StatusOK || !strings.Contains(string(resp), `"logged":2`) {
		t.Fatalf("report: %d %s", code, resp)
	}

	code, resp = ts.call("GET", path+"?limit=5", nil, "")
	if code != http.StatusOK {
		t.Fatalf("tail: %d %s", code, resp)
	}
	var out struct {
		Errors []struct {
			Kind    string `json:"kind"`
			Message string `json:"message"`
			Source  string `json:"source"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Errors) != 2 || out.Errors[0].Kind != "unhandledrejection" || out.Errors[1].Message != "TypeError: x is null" {
		t.Fatalf("tail = %+v, want newest-first both rows", out.Errors)
	}
}
