package scripts_test

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func writeFixture(t *testing.T, name, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func sqliteFixture(t *testing.T, path, marker string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE marker (value TEXT); INSERT INTO marker VALUES (?)", marker); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRollbackRestartsOriginalDatabaseAndConfig(t *testing.T) {
	for _, originalConfig := range []bool{true, false} {
		t.Run(map[bool]string{true: "existing config", false: "no original config"}[originalConfig], func(t *testing.T) {
			testRestoreRollback(t, originalConfig)
		})
	}
}

func TestRestoreKeepsServiceDatabaseOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to simulate restoring an unprivileged service's DB")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("requires SQLite integrity checker")
	}
	root := t.TempDir()
	db := filepath.Join(root, "timerpi.db")
	backup := filepath.Join(t.TempDir(), "backup.db")
	sqliteFixture(t, db, "original")
	sqliteFixture(t, backup, "restored")
	if err := os.Chown(db, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "restore.sh", backup, "--data-dir", root, "--no-service")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	info, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != 65534 || stat.Gid != 65534 || info.Mode().Perm() != 0640 {
		t.Fatalf("restored owner %d:%d mode %o", stat.Uid, stat.Gid, info.Mode().Perm())
	}
}

func TestRestoreRestartsOriginalServiceWhenRollbackCopyFails(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("requires SQLite integrity checker")
	}
	root, bin := t.TempDir(), t.TempDir()
	db := filepath.Join(root, "timerpi.db")
	backup := filepath.Join(t.TempDir(), "backup.db")
	sqliteFixture(t, db, "original")
	sqliteFixture(t, backup, "restored")
	record := filepath.Join(root, "service.log")
	writeFixture(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$OPS_LOG\"\n", 0700)
	writeFixture(t, filepath.Join(bin, "curl"), "#!/bin/sh\nprintf '{\"ok\":true}'\n", 0700)
	writeFixture(t, filepath.Join(bin, "cp"), "#!/bin/sh\ncase \"$*\" in *.pre-restore.*) exit 1;; esac\nexec /bin/cp \"$@\"\n", 0700)
	cmd := exec.Command("bash", "restore.sh", backup, "--data-dir", root)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "OPS_LOG="+record)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("restore succeeded despite copy failure: %s", out)
	}
	got, err := os.ReadFile(record)
	if err != nil || string(got) != "stop\nstart\n" {
		t.Fatalf("service commands were %q (%v), want stop then start", got, err)
	}
}

func TestBackupKeepsOldArchivesWhenConfigCopyFails(t *testing.T) {
	root, bin := t.TempDir(), t.TempDir()
	sqliteFixture(t, filepath.Join(root, "timerpi.db"), "original")
	writeFixture(t, filepath.Join(root, "config.json"), "{}", 0600)
	old := filepath.Join(root, "backups", "20000101T000000Z.db.gz")
	writeFixture(t, old, "previous backup", 0640)
	writeFixture(t, filepath.Join(bin, "cp"), "#!/bin/sh\ncase \"$*\" in *config.json*) exit 1;; esac\nexec /bin/cp \"$@\"\n", 0700)
	cmd := exec.Command("bash", "backup.sh", "--data-dir", root, "--keep", "1")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("backup succeeded despite sidecar failure: %s", out)
	}
	files, err := os.ReadDir(filepath.Dir(old))
	if err != nil || len(files) != 1 || files[0].Name() != filepath.Base(old) {
		t.Fatalf("failed backup changed the archive set: %v, %v", files, err)
	}
}

func testRestoreRollback(t *testing.T, originalConfig bool) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("requires python3 for simulated SQLite service")
	}
	root := t.TempDir()
	data, bin := filepath.Join(root, "data"), filepath.Join(root, "bin")
	if err := os.MkdirAll(data, 0755); err != nil {
		t.Fatal(err)
	}
	sqliteFixture(t, filepath.Join(data, "timerpi.db"), "original")
	backup := filepath.Join(root, "backup.db.gz")
	raw := filepath.Join(root, "backup.db")
	sqliteFixture(t, raw, "restored")
	gz := exec.Command("gzip", "-c", raw)
	bytes, err := gz.Output()
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, backup, string(bytes), 0600)
	if originalConfig {
		writeFixture(t, filepath.Join(data, "config.json"), `{"http_port":8010,"name":"original"}`, 0600)
	}
	writeFixture(t, filepath.Join(root, "backup.config.json"), `{"http_port":8020,"name":"restored"}`, 0600)
	controller := filepath.Join(root, "service.py")
	writeFixture(t, controller, `import os, socket, sqlite3, subprocess, sys, time
root = os.environ['TEST_ROOT']
path = root + '/service.sock'
action = sys.argv[1]
if action == 'serve':
    db = sqlite3.connect(root + '/data/timerpi.db')
    db.execute('PRAGMA journal_mode=WAL')
    db.execute('CREATE TABLE IF NOT EXISTS service_writes (value TEXT)')
    db.execute("INSERT INTO service_writes VALUES ('live')"); db.commit()
    srv = socket.socket(socket.AF_UNIX); srv.bind(path); srv.listen()
    while True:
        c, _ = srv.accept()
        request = c.recv(32)
        if request == b'stop':
            c.close(); break
        c.sendall(db.execute('SELECT value FROM marker').fetchone()[0].encode()); c.close()
    db.close(); srv.close(); os.unlink(path)
elif action == 'start':
    if not os.path.exists(path):
        subprocess.Popen([sys.executable, __file__, 'serve'], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
        for _ in range(500):
            if os.path.exists(path): break
            time.sleep(.01)
        else: raise RuntimeError('service start timed out')
elif action == 'stop':
    if os.path.exists(path):
        c = socket.socket(socket.AF_UNIX); c.connect(path); c.sendall(b'stop'); c.close()
        for _ in range(500):
            if not os.path.exists(path): break
            time.sleep(.01)
        else: raise RuntimeError('service stop timed out')
elif action == 'read':
    c = socket.socket(socket.AF_UNIX); c.connect(path); c.sendall(b'read'); print(c.recv(64).decode()); c.close()
`, 0600)
	writeFixture(t, filepath.Join(bin, "systemctl"), "#!/bin/sh\nexec '"+python+"' '"+controller+"' \"$1\"\n", 0700)
	writeFixture(t, filepath.Join(bin, "seq"), "#!/bin/sh\necho 1\n", 0700)
	writeFixture(t, filepath.Join(bin, "sleep"), "#!/bin/sh\nexit 0\n", 0700)
	writeFixture(t, filepath.Join(bin, "curl"), "#!/bin/sh\nif [ ! -f \"$TEST_ROOT/health-failed\" ]; then touch \"$TEST_ROOT/health-failed\"; exit 1; fi\nprintf '{\"ok\":true}'\n", 0700)
	env := append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TEST_ROOT="+root)
	service := func(action string) string {
		t.Helper()
		cmd := exec.Command(python, controller, action)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("service %s: %v: %s", action, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	service("start")
	defer service("stop")
	cmd := exec.Command("bash", "restore.sh", backup, "--data-dir", data, "--with-config")
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("failed restore must report failure: %s", out)
	}
	if got := service("read"); got != "original" {
		t.Errorf("live service serves %q after rollback, want original\n%s", got, out)
	}
	conf, err := os.ReadFile(filepath.Join(data, "config.json"))
	if !originalConfig {
		if !os.IsNotExist(err) {
			t.Errorf("restored config should be removed, err=%v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(conf), `"original"`) {
		t.Errorf("rollback config = %s, want original", conf)
	}
}

func TestBackupFallbackKeepsVisibleWarningAndCleansTemporaryFiles(t *testing.T) {
	for _, scenario := range []struct {
		name                     string
		failCompression, invalid bool
	}{
		{name: "archive"}, {name: "failed compression", failCompression: true}, {name: "invalid database", invalid: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root, bin := t.TempDir(), t.TempDir()
			sqliteFixture(t, filepath.Join(root, "timerpi.db"), "original")
			if scenario.invalid {
				writeFixture(t, filepath.Join(root, "timerpi.db"), "not sqlite", 0600)
			}
			oldStem := filepath.Join(root, "backups", "20000101T000000Z")
			writeFixture(t, oldStem+".db.gz", "old", 0640)
			writeFixture(t, oldStem+".note", "old warning", 0640)
			oldTime := time.Unix(1, 0)
			if err := os.Chtimes(oldStem+".db.gz", oldTime, oldTime); err != nil {
				t.Fatal(err)
			}
			for _, tool := range []string{"date", "mkdir", "chmod", "rm", "cp", "head", "grep", "du", "cut", "gzip", "mv", "ls", "wc"} {
				path, err := exec.LookPath(tool)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, filepath.Join(bin, tool)); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.failCompression {
				os.Remove(filepath.Join(bin, "gzip"))
				writeFixture(t, filepath.Join(bin, "gzip"), "#!/bin/sh\nexit 1\n", 0700)
			}
			cmd := exec.Command("bash", "backup.sh", "--data-dir", root, "--quiet", "--keep", "1")
			cmd.Env = append(os.Environ(), "PATH="+bin)
			out, err := cmd.CombinedOutput()
			if (err != nil) != (scenario.failCompression || scenario.invalid) {
				t.Fatalf("backup err=%v: %s", err, out)
			}
			files, err := os.ReadDir(filepath.Join(root, "backups"))
			if err != nil {
				t.Fatal(err)
			}
			var archives, notes int
			for _, file := range files {
				if strings.HasPrefix(file.Name(), ".tmp-") {
					t.Errorf("temporary artifact left behind: %s", file.Name())
				}
				if strings.HasSuffix(file.Name(), ".db.gz") {
					archives++
				}
				if strings.HasSuffix(file.Name(), ".note") {
					notes++
					path := filepath.Join(root, "backups", file.Name())
					info, _ := os.Stat(path)
					if info.Mode().Perm() != 0640 {
						t.Errorf("warning mode = %o", info.Mode().Perm())
					}
					if _, err := os.Stat(strings.TrimSuffix(path, ".note") + ".db.gz"); err != nil {
						t.Errorf("warning has no matching archive: %s", path)
					}
				}
			}
			if archives != 1 || notes != 1 {
				t.Errorf("got %d archives and %d warnings, want one pair", archives, notes)
			}
			_, oldErr := os.Stat(oldStem + ".db.gz")
			failed := scenario.failCompression || scenario.invalid
			if (oldErr == nil) != failed {
				t.Errorf("old archive preserved=%v, want %v", oldErr == nil, failed)
			}

		})
	}
}
