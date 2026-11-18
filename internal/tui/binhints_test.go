package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/systemd"
)

// Binaries with a config dir or env file by the service conventions are
// marked and listed before the others.
func TestBinPickerServiceHints(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := binPickerModel(t, "alpha", "mid", "zeta") // sets XDG_CONFIG_HOME
	cfgHome := os.Getenv("XDG_CONFIG_HOME")
	os.MkdirAll(filepath.Join(cfgHome, "zeta"), 0o755)
	os.WriteFile(filepath.Join(cfgHome, "zeta", "config.toml"), []byte("version = 1\n"), 0o644)
	os.MkdirAll(filepath.Join(home, ".secrets"), 0o700)
	os.WriteFile(filepath.Join(home, ".secrets", "mid.env"), nil, 0o600)
	m.bins.load(m)
	var order []string
	for _, i := range m.bins.shown {
		order = append(order, m.bins.all[i].name)
	}
	if got := strings.Join(order, " "); got != "mid zeta alpha" {
		t.Errorf("order = %q, want likely services first: \"mid zeta alpha\"", got)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"likely a service (has env file)", "likely a service (has config)"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
}

func TestStateCellQuickExit(t *testing.T) {
	st := systemd.Status{Type: "simple", ActiveState: "inactive", SubState: "dead", ExitCode: 1}
	st.RunTime, st.SelfExit = 200_000_000, true // 0.2 s
	if got := ansi.Strip(stateCell(st, true)); got != "○ exited in 0.2s" {
		t.Errorf("clean quick exit: %q", got)
	}
	if !strings.Contains(ansi.Strip(quickExitNote(st)), "Probably not a service") {
		t.Errorf("note: %q", ansi.Strip(quickExitNote(st)))
	}
	st.ExitStatus = 1
	if got := ansi.Strip(stateCell(st, true)); got != "✖ crashed in 0.2s" {
		t.Errorf("crash: %q", got)
	}
	if note := ansi.Strip(quickExitNote(st)); !strings.Contains(note, "exited with status 1 0.2s after starting") {
		t.Errorf("crash note: %q", note)
	}
}
