package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvFileProblem(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "myapp.env")
	if got := EnvFileProblem(p); got != "missing" {
		t.Errorf("missing file: %q", got)
	}
	if got := EnvFileProblem("-" + p); got != "" {
		t.Errorf("missing file with - prefix: %q, want no problem", got)
	}
	os.WriteFile(p, []byte("TOKEN=\n"), 0o644)
	os.Chmod(p, 0o644)
	if got := EnvFileProblem(p); !strings.Contains(got, "readable by others (0644)") {
		t.Errorf("0644: %q", got)
	}
	os.Chmod(p, 0o600)
	if got := EnvFileProblem(p); got != "" {
		t.Errorf("0600: %q", got)
	}
}

func TestExpandEnvFile(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for in, want := range map[string]string{
		"~/.secrets/a.env":  "/home/u/.secrets/a.env",
		"-~/.secrets/a.env": "-/home/u/.secrets/a.env",
		" /etc/a.env ":      "/etc/a.env",
		"":                  "",
	} {
		if got := ExpandEnvFile(in); got != want {
			t.Errorf("ExpandEnvFile(%q) = %q, want %q", in, got, want)
		}
	}
	if got := DefaultEnvFile("myapp"); got != "/home/u/.secrets/myapp.env" {
		t.Errorf("DefaultEnvFile = %q", got)
	}
}

func TestBinaryName(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"/":                      "",
		"myapp":                  "myapp",
		"/home/u/.local/bin/app": "app",
	} {
		if got := BinaryName(in); got != want {
			t.Errorf("BinaryName(%q) = %q, want %q", in, got, want)
		}
	}
	if DefaultAppConfig("") != "" || DefaultAppConfig("a/b") != "" {
		t.Error("DefaultAppConfig accepts only a bare binary name")
	}
}

func TestTildeEnvFile(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for in, want := range map[string]string{
		"/home/u/.secrets/a.env":  "~/.secrets/a.env",
		"-/home/u/.secrets/a.env": "-~/.secrets/a.env",
		"-/etc/a.env":             "-/etc/a.env",
	} {
		if got := TildeEnvFile(in); got != want {
			t.Errorf("TildeEnvFile(%q) = %q, want %q", in, got, want)
		}
		if got := ExpandEnvFile(TildeEnvFile(in)); got != in {
			t.Errorf("ExpandEnvFile(TildeEnvFile(%q)) = %q", in, got)
		}
	}
}

func TestDefaultAppConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if p := DefaultAppConfig("myapp"); p != "" {
		t.Fatalf("no dir: %q", p)
	}
	dir := AppDir("myapp")
	os.MkdirAll(dir, 0o755)
	for _, f := range []string{"config.toml.20261117-103000.bak", "config.example.toml", ".env"} {
		os.WriteFile(filepath.Join(dir, f), nil, 0o644)
	}
	if p := DefaultAppConfig("myapp"); p != "" {
		t.Fatalf("picked a backup/example: %q", p)
	}
	os.WriteFile(filepath.Join(dir, "config.toml"), nil, 0o644)
	if p := DefaultAppConfig("myapp"); p != filepath.Join(dir, "config.toml") {
		t.Fatalf("got %q", p)
	}
}
