package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestInstallerDryRunKeepsExistingDirectoriesPrivate(t *testing.T) {
	src, target := t.TempDir(), t.TempDir()
	for _, name := range []string{"bin/timerpi", "bin/splash-draw", "timerpi.service", "timerpi-splash.service", "public/img/timerpi-512.png"} {
		path := filepath.Join(src, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"opt/timerpi", "var/lib/timerpi"} {
		if err := os.MkdirAll(filepath.Join(target, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", "install-pi.sh", "--src", src, "--dry-run", "--no-mesh")
	cmd.Env = append(os.Environ(), "PREFIX="+target)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	for _, name := range []string{"opt/timerpi", "var/lib/timerpi"} {
		info, err := os.Stat(filepath.Join(target, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Errorf("dry run changed %s permissions to %o", name, info.Mode().Perm())
		}
	}
}
