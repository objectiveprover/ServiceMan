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

func TestParseUnitRoundTrip(t *testing.T) {
	dir := t.TempDir()
	units := []NewUnit{
		{Name: "web.service", Description: "Web app", Scope: config.System, ExecStart: "/srv/web --port 3000",
			WorkingDir: "/srv", RunAs: "www-data", Environment: "PORT=3000 MODE=prod", EnvFile: "-/etc/web.env",
			Restart: "on-failure", Network: true},
		{Name: "hello.service", Description: "hello", Scope: config.User, ExecStart: "/home/u/.local/bin/hello",
			Restart: "no"},
	}
	for _, want := range units {
		p := filepath.Join(dir, want.UnitName())
		os.WriteFile(p, []byte(want.Render()), 0o644)
		got, unsupported, err := ParseUnit(p, want.Scope)
		if err != nil || len(unsupported) > 0 {
			t.Fatalf("%s: err=%v unsupported=%q", want.Name, err, unsupported)
		}
		if got.Render() != want.Render() {
			t.Errorf("%s: round trip differs:\n%s\nwant:\n%s", want.Name, got.Render(), want.Render())
		}
	}
}

func TestParseUnitUnsupported(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"pre":     "[Service]\nExecStartPre=/bin/true\nExecStart=/bin/app\n",
		"forking": "[Service]\nType=forking\nExecStart=/bin/app\n",
		"twice":   "[Service]\nExecStart=/bin/a\nExecStart=/bin/b\n",
		"noexec":  "[Unit]\nDescription=x\n",
		"target":  "[Service]\nExecStart=/bin/app\n[Install]\nWantedBy=graphical.target\n",
		"usernet": "[Unit]\nWants=network-online.target\nAfter=network-online.target\n[Service]\nExecStart=/bin/app\n",
		"restart": "[Service]\nExecStart=/bin/app\nRestart=always\n",
	}
	for name, text := range cases {
		p := filepath.Join(dir, name+".service")
		os.WriteFile(p, []byte(text), 0o644)
		if _, unsupported, err := ParseUnit(p, config.User); err != nil || len(unsupported) == 0 {
			t.Errorf("%s: err=%v unsupported=%q, want some", name, err, unsupported)
		}
	}
}

func TestEditInstallCmd(t *testing.T) {
	u := NewUnit{Name: "hello", Scope: config.User, ExecStart: "/bin/true", Edit: true, Start: true}
	cmd, err := u.InstallCmd("true", false)
	if err != nil {
		t.Fatal(err)
	}
	var script string
	for _, e := range cmd.Env {
		if v, ok := strings.CutPrefix(e, "SERVICEMAN_SCRIPT="); ok {
			script = v
		}
	}
	for _, want := range []string{`disable "$SERVICEMAN_UNIT"`, `restart "$SERVICEMAN_UNIT"`} {
		if !strings.Contains(script, want) {
			t.Errorf("edit script lacks %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, ` start "$SERVICEMAN_UNIT"`) {
		t.Errorf("edit script starts instead of restarting:\n%s", script)
	}
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "SERVICEMAN_FILE=") {
			os.Remove(strings.TrimPrefix(e, "SERVICEMAN_FILE="))
		}
	}
}

func TestResolveExecIgnoresCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "tool"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile("tool", []byte("#!/bin/sh\n"), 0o755) // in the cwd, must not be picked
	t.Setenv("PATH", ".:"+bin)
	cases := map[string]string{
		"bin/app --port 8080": home + "/bin/app --port 8080",
		"./server":            home + "/server",
		"tool -v":             bin + "/tool -v",
		"/opt/x":              "/opt/x",
		"-/opt/x":             "-/opt/x",
	}
	for in, want := range cases {
		if got := resolveExec(in); got != want {
			t.Errorf("resolveExec(%q) = %q, want %q", in, got, want)
		}
	}
}
