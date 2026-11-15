package boards_test

import (
	"os/exec"
	"testing"
)

func TestDisplaySessionWithJS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	if out, err := exec.Command(node, "--test", "testdata/display_session.mjs").CombinedOutput(); err != nil {
		t.Fatalf("display session: %v\n%s", err, out)
	}
}
