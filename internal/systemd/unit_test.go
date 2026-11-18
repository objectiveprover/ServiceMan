package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUnitRoundTrip(t *testing.T) {
	dir := t.TempDir()
	units := []NewUnit{
		{Name: "web.service", Description: "Web app", ExecStart: "/srv/web --port 3000",
			WorkingDir: "/srv", Environment: "PORT=3000 MODE=prod", EnvFile: "-/etc/web.env",
			Restart: "on-failure"},
		{Name: "hello.service", Description: "hello", ExecStart: "/home/u/.local/bin/hello",
			Restart: "no"},
	}
	for _, want := range units {
		p := filepath.Join(dir, want.UnitName())
		os.WriteFile(p, []byte(want.Render()), 0o644)
		got, unsupported, err := ParseUnit(p)
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
		"runas":   "[Service]\nUser=www-data\nExecStart=/bin/app\n",
		"system":  "[Service]\nExecStart=/bin/app\n[Install]\nWantedBy=multi-user.target\n",
		"restart": "[Service]\nExecStart=/bin/app\nRestart=always\n",
	}
	for name, text := range cases {
		p := filepath.Join(dir, name+".service")
		os.WriteFile(p, []byte(text), 0o644)
		if _, unsupported, err := ParseUnit(p); err != nil || len(unsupported) == 0 {
			t.Errorf("%s: err=%v unsupported=%q, want some", name, err, unsupported)
		}
	}
}

func TestEditInstallCmd(t *testing.T) {
	u := NewUnit{Name: "hello", ExecStart: "/bin/true", Edit: true, Start: true}
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

// After= is reported as it is.
func TestParseUnitOtherAfter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.service")
	os.WriteFile(p, []byte("[Unit]\nAfter=syslog.target\n[Service]\nExecStart=/bin/app\n"), 0o644)
	_, unsupported, err := ParseUnit(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(unsupported) != 1 || unsupported[0] != "After=syslog.target" {
		t.Errorf("unsupported = %q, want [After=syslog.target]", unsupported)
	}
}
