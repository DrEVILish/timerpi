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

func TestInstallerSupportsStagingInApplicationDirectory(t *testing.T) {
	for _, arm64 := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "arm64"}[arm64], func(t *testing.T) {
			target := t.TempDir()
			src := filepath.Join(target, "opt/timerpi")
			suffix := ""
			if arm64 {
				suffix = "-arm64"
			}
			for _, name := range []string{"bin/timerpi" + suffix, "bin/splash-draw" + suffix, "timerpi.service", "timerpi-splash.service", "public/img/timerpi-512.png", "deploy/box/network/fixture.network", "deploy/box/systemd/fixture.service", "deploy/box/90-timerpi-mesh.rules", "deploy/box/batman-adv.conf", "deploy/box/90-timerpi-mesh.nm.conf", "deploy/box/mesh-filter.nft"} {
				writeFixture(t, filepath.Join(src, name), "fixture", 0600)
			}
			for run := 0; run < 2; run++ {
				cmd := exec.Command("bash", "install-pi.sh", "--src", src)
				cmd.Env = append(os.Environ(), "PREFIX="+target)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("install run %d: %v\n%s", run, err, out)
				}
			}
			for name, mode := range map[string]os.FileMode{"bin/timerpi": 0755, "bin/splash-draw": 0755, "public/img/timerpi-512.png": 0644, "deploy/box/mesh-filter.nft": 0644} {
				info, err := os.Stat(filepath.Join(src, name))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != mode {
					t.Errorf("%s permissions = %o, want %o", name, info.Mode().Perm(), mode)
				}
			}
		})
	}
}
