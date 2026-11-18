package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

// The TUI shows where a service's env file is, never what's in it: not in
// the list, the details box, the Edit Service form or its unit preview.
func TestEnvFileValuesNeverShown(t *testing.T) {
	const secret = "hunter2-s3cret"
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	envFile := filepath.Join(dir, "app.env")
	os.WriteFile(envFile, []byte("TOKEN="+secret+"\n"), 0o600)
	unit := systemd.NewUnit{Name: "app", ExecStart: "/bin/app", EnvFile: envFile, Restart: "no"}
	path := filepath.Join(dir, "app.service")
	os.WriteFile(path, []byte(unit.Render()), 0o644)

	svc := config.Service{Unit: "app.service", Scope: config.User}
	m := &Model{cfg: &config.Config{Services: []config.Service{svc}}, w: 160, h: 40, daemonState: "active",
		status: map[string]systemd.Status{svc.Key(): {FragmentPath: path, ActiveState: "active", EnvFiles: []string{envFile}}}}
	screens := map[string]string{"list": ansi.Strip(m.View())}
	if !strings.Contains(screens["list"], envFile) {
		t.Fatalf("details box doesn't show the env file path:\n%s", screens["list"])
	}
	m.openEditForm(svc)
	if m.mode != modeForm {
		t.Fatalf("Edit Service form didn't open (msg %q)", m.msg)
	}
	screens["form"] = ansi.Strip(m.View())
	for name, s := range screens {
		if strings.Contains(s, secret) {
			t.Errorf("%s screen shows the env file's contents:\n%s", name, s)
		}
	}
}
