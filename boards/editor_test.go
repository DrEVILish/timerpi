package boards_test

import (
	"os/exec"
	"testing"
)

func TestLayoutEditsWithJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if out, err := exec.Command(node, "--test", "testdata/layout_edit.mjs").CombinedOutput(); err != nil {
		t.Fatalf("layout edits: %v\n%s", err, out)
	}
}
