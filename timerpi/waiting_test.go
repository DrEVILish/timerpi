package timerpi

import "testing"

// RegisterWaiting upserts per (name, host): one row for re-registers, name
// sanitized, host clipped to 80.
func TestRegisterWaitingUpsert(t *testing.T) {
	d := openTestDB(t)
	if err := d.RegisterWaiting("Stage Left", "10.0.0.5"); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Same (name, host) again → upsert, still one row.
	if err := d.RegisterWaiting(" Stage Left ", "10.0.0.5"); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	longHost := make([]byte, 200)
	for i := range longHost {
		longHost[i] = 'h'
	}
	if err := d.RegisterWaiting("Stage Left", string(longHost)); err != nil {
		t.Fatalf("register other host: %v", err)
	}
	ws, err := d.ListWaiting()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Identity is (name, host): same pair collapses, other host is its own row.
	if len(ws) != 2 {
		t.Fatalf("want 2 rows (duplicate collapsed + distinct host), got %d: %+v", len(ws), ws)
	}
	if ws[0].Name != "Stage Left" {
		t.Errorf("name not trimmed: %q", ws[0].Name)
	}
	if len(ws[1].Host) != 80 {
		t.Errorf("host clipped to 80, got %d", len(ws[1].Host))
	}
	if err := d.RegisterWaiting(" <> ", "h"); err == nil {
		t.Error("name that sanitizes to empty should be refused")
	}
}

// ClaimWaiting hands the captured code back exactly once: first claim gets
// it, later polls get "", and an unassigned pair never matches anything.
func TestClaimWaitingConsumesOnce(t *testing.T) {
	d := openTestDB(t)
	if err := d.RegisterWaiting("tv-a", "host1"); err != nil {
		t.Fatalf("register: %v", err)
	}
	ws, _ := d.ListWaiting()
	if err := d.AssignWaiting(ws[0].ID, "SH12"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	// Same identity under a different host must NOT receive the code.
	if code, _ := d.ClaimWaiting("tv-a", "host2"); code != "" {
		t.Errorf("other host claimed %+q", code)
	}
	code, err := d.ClaimWaiting("tv-a", "host1")
	if err != nil || code != "SH12" {
		t.Fatalf("first claim: got %q err %v", code, err)
	}
	if code, _ := d.ClaimWaiting("tv-a", "host1"); code != "" {
		t.Errorf("re-poll re-served code %q", code)
	}
	if code, _ := d.ClaimWaiting("nobody", "nowhere"); code != "" {
		t.Errorf("unknown pair claimed %q", code)
	}
}
