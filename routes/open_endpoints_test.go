package routes_test

import (
	"fmt"
	"net/http"
	"testing"

	"timerpi/routes"
)

// BUGLOG RW16: the open screen-register and client-log endpoints are
// budgeted per client IP.
func TestOpenEndpointsAreBudgeted(t *testing.T) {
	ts := newAPITest(t)
	oldR, oldC := routes.SetOpenEndpointBudgets(2, 2)
	defer routes.SetOpenEndpointBudgets(oldR, oldC)
	var reg, logs []int
	for i := 0; i < 3; i++ {
		code, _ := ts.anon("POST", "/api/waiting/register", []byte(fmt.Sprintf(`{"name":"Flood-%d","host":"x"}`, i)), "application/json")
		reg = append(reg, code)
		code, _ = ts.anon("POST", "/api/shows/"+ts.showCode+"/client-log", []byte(`{"entries":[{"kind":"error","message":"boom"}]}`), "application/json")
		logs = append(logs, code)
	}
	if reg[1] != 200 || reg[2] != http.StatusTooManyRequests {
		t.Errorf("register codes %v, want 200,200,429", reg)
	}
	if logs[1] != 200 || logs[2] != http.StatusTooManyRequests {
		t.Errorf("client-log codes %v, want 200,200,429", logs)
	}
}
