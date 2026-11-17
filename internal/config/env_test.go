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
