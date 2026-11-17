package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"serviceman/internal/config"
)

func TestParseExecPath(t *testing.T) {
	cases := map[string]string{
		"{ path=/usr/sbin/sshd ; argv[]=/usr/sbin/sshd -D $SSHD_OPTS ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }": "/usr/sbin/sshd",
		"{ path=/home/u/.local/bin/hello ; argv[]=/home/u/.local/bin/hello ; ignore_errors=no }":                                                                   "/home/u/.local/bin/hello",
		"": "",
	}
	for in, want := range cases {
		if got := parseExecPath(in); got != want {
			t.Errorf("parseExecPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEnvFile(t *testing.T) {
	cases := map[string]string{
		"/etc/default/ssh (ignore_errors=yes)": "/etc/default/ssh",
		"/srv/app/.env (ignore_errors=no)":     "/srv/app/.env",
		"/plain":                               "/plain",
	}
	for in, want := range cases {
		if got := parseEnvFile(in); got != want {
			t.Errorf("parseEnvFile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetEnvFileCmds(t *testing.T) {
	// A fake systemctl so the test never touches the real systemd.
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "systemctl"), []byte("#!/bin/sh\necho \"$@\" >> \"$0.log\"\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	q, i := SetEnvFileCmds(config.User, "myapp.service", "/home/u/.secrets/myapp.env")
	if i != nil {
		t.Error("user scope shouldn't need an interactive (sudo) variant")
	}
	if out, err := q.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	drop := EnvFileDropInPath(config.User, "myapp.service")
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

	q, _ = SetEnvFileCmds(config.User, "myapp.service", "")
	if out, err := q.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(drop); !os.IsNotExist(err) {
		t.Error("empty path should remove the drop-in")
	}
}
