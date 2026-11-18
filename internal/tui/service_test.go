package tui

import (
	"os"
	"path/filepath"
	"testing"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

func TestAppConfigDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	svc := config.Service{Unit: "myapp.service", Scope: config.User}
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{svc.Key(): {ExecPath: "/home/u/.local/bin/myapp"}}}
	if p, def := m.appConfig(svc); p != "" || def {
		t.Fatalf("no config yet: %q %v", p, def)
	}
	os.MkdirAll(config.AppDir("myapp"), 0o755)
	want := filepath.Join(config.AppDir("myapp"), "config.toml")
	os.WriteFile(want, nil, 0o644)
	if p, def := m.appConfig(svc); p != want || !def {
		t.Fatalf("default: %q %v, want %q", p, def, want)
	}
	svc.ConfigPath = "/etc/myapp.toml" // set with C: wins over the default
	if p, def := m.appConfig(svc); p != "/etc/myapp.toml" || def {
		t.Fatalf("explicit: %q %v", p, def)
	}
}
