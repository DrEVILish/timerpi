package timerpi

import (
	"strings"
	"testing"
)

// F4 store: truncation, newest-first tail, per-show cap, limit clamps.
func TestClientErrorStoreF4(t *testing.T) {
	d := openTestDB(t)
	show := mustCreateShow(t, d, "Buggy")

	d.LogClientError(show.ID, "error", strings.Repeat("E", 600), strings.Repeat("S", 200))
	errs, err := d.ListClientErrors(show.ID, 10)
	if err != nil || len(errs) != 1 {
		t.Fatalf("log: %+v %v", errs, err)
	}
	if len(errs[0].Message) != 500 || len(errs[0].Source) != 160 {
		t.Errorf("not truncated: msg=%d src=%d", len(errs[0].Message), len(errs[0].Source))
	}

	for i := 0; i < MaxClientErrorsPerShow+3; i++ {
		d.LogClientError(show.ID, "error", "x", "")
	}
	if errs, _ := d.ListClientErrors(show.ID, 99999); len(errs) != MaxClientErrorsPerShow {
		t.Errorf("cap = %d rows, want %d", len(errs), MaxClientErrorsPerShow)
	}
	if errs, _ := d.ListClientErrors(show.ID, 0); len(errs) != 50 {
		t.Errorf("zero limit = %d, want default 50", len(errs))
	}
}
