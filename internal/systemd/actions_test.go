package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"serviceman/internal/config"
)

func TestSetEnvFileCmd(t *testing.T) {
	// A fake systemctl so the test never touches the real systemd.
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"$@\" >> \"$0.log\"\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	q := SetEnvFileCmd("myapp.service", "/home/u/.secrets/myapp.env")
	if out, err := q.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	drop := EnvFileDropInPath("myapp.service")
	b, err := os.ReadFile(drop)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "[Service]\nEnvironmentFile=/home/u/.secrets/myapp.env\n") {
		t.Errorf("drop-in:\n%s", b)
	}
	if log, _ := os.ReadFile(filepath.Join(bin, "systemctl.log")); !strings.Contains(string(log), "daemon-reload") {
		t.Errorf("no daemon-reload: %q", log)
	}

	q = SetEnvFileCmd("myapp.service", "")
	if out, err := q.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(drop); !os.IsNotExist(err) {
		t.Error("empty path should remove the drop-in")
	}
}

// ActionCmds hands the unit name to systemctl as one argument, untouched by
// the shell, and RunQuick reports its errors.
func TestActionCmds(t *testing.T) {
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\nfor a; do echo \"[$a]\"; done >> \"$0.log\"\n[ \"$4\" != fail ] || { echo 'boom' >&2; exit 1; }\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	q, i := ActionCmds(config.User, "restart", `a$b'c\x2d.service`)
	if i != nil {
		t.Error("user scope shouldn't need an interactive (sudo) variant")
	}
	if _, err := RunQuick(q); err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(filepath.Join(bin, "systemctl.log"))
	if want := "[--user]\n[restart]\n[--]\n[a$b'c\\x2d.service]\n"; string(log) != want {
		t.Errorf("systemctl got\n%s\nwant\n%s", log, want)
	}

	q, _ = ActionCmds(config.User, "stop", "fail")
	if needAuth, err := RunQuick(q); needAuth || err == nil || err.Error() != "boom" {
		t.Errorf("RunQuick = %v, %v; want false, boom", needAuth, err)
	}
}
