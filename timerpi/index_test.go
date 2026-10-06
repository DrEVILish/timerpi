package timerpi

import (
	"strings"
	"testing"
)

// BUGLOG RS28: entry lookups by parent use an index, not a table scan.
func TestPollsParentIndexed(t *testing.T) {
	d := openTestDB(t)
	var plan []struct {
		ID     int    `db:"id"`
		Parent int    `db:"parent"`
		NotUse int    `db:"notused"`
		Detail string `db:"detail"`
	}
	if err := d.Select(&plan, `EXPLAIN QUERY PLAN SELECT id FROM polls WHERE parent = 1`); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range plan {
		if p.Detail != "" && strings.Contains(p.Detail, "idx_polls_parent") {
			found = true
		}
	}
	if !found {
		t.Errorf("query plan doesn't use idx_polls_parent: %+v", plan)
	}
}
