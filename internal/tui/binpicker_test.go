package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

func TestBinPickerFilterAndFocus(t *testing.T) {
	m := binPickerModel(t, "alpha", "beta", "gamma")
	p := m.bins
	if p.focus != focusFilter || len(p.shown) != 3 {
		t.Fatalf("focus=%v shown=%d, want the filter and 3 binaries", p.focus, len(p.shown))
	}
	m.handleKey(runes("a"))
	m.handleKey(runes("l"))
	if len(p.shown) != 1 {
		t.Fatalf("typing filters: shown=%d, want 1", len(p.shown))
	}
	// Filter → list → custom field; C-p goes back the same way.
	m.handleKey(keyCN)
	if p.focus != focusList || p.cursor != 0 {
		t.Fatalf("C-n from the filter: focus=%v cursor=%d", p.focus, p.cursor)
	}
	m.handleKey(keyCN)
	if p.focus != focusCustom || !p.custom.Focused() {
		t.Fatalf("C-n past the last binary: focus=%v", p.focus)
	}
	m.handleKey(runes("x"))
	if p.custom.Value() != "x" || p.ti.Value() != "al" {
		t.Fatalf("typing in the custom field: custom=%q filter=%q", p.custom.Value(), p.ti.Value())
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlP})
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlP})
	if p.focus != focusFilter {
		t.Fatalf("C-p twice from the custom field: focus=%v", p.focus)
	}
	// Typing while on a binary goes back to the filter.
	m.handleKey(keyCN)
	m.handleKey(runes("p"))
	if p.focus != focusFilter || p.ti.Value() != "alp" {
		t.Fatalf("typing on a row: focus=%v filter=%q", p.focus, p.ti.Value())
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"SERVICEMAN", "New Service", "Filter: alp", "alpha", "Custom command or path", "1/3 binaries"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if strings.Index(view, "alpha") > strings.Index(view, "Custom command or path") {
		t.Errorf("the list should come before the custom field:\n%s", view)
	}
}

func TestBinPickerOpensForm(t *testing.T) {
	custom := func(m *Model) {
		m.handleKey(keyCN) // past the filter (and the list, when empty)
		for m.bins.focus != focusCustom {
			m.handleKey(keyCN)
		}
	}
	// Empty custom field: a blank form; C-g goes back to the picker.
	m := binPickerModel(t, "alpha")
	custom(m)
	m.handleKey(keyEnter)
	if m.mode != modeForm || m.form.get("exec").value() != "" {
		t.Fatalf("RET on an empty custom field: mode=%v", m.mode)
	}
	m.handleKey(keyCG)
	if m.mode != modeBinPicker {
		t.Fatalf("C-g in the form: mode=%v, want the picker", m.mode)
	}

	// A custom command prefills ExecStart and Name.
	m = binPickerModel(t)
	custom(m)
	m.handleKey(runes("/opt/app/server --port 80"))
	m.handleKey(keyEnter)
	if m.mode != modeForm {
		t.Fatalf("RET on a command: mode=%v", m.mode)
	}
	if got := m.form.get("exec").value(); got != "/opt/app/server --port 80" {
		t.Errorf("exec = %q", got)
	}
	if got := m.form.get("name").value(); got != "server" {
		t.Errorf("name = %q", got)
	}

	// A binary without a service: the prefilled, watched user service.
	m = binPickerModel(t, "alpha")
	m.handleKey(keyCN)
	m.handleKey(keyEnter)
	if m.mode != modeForm || m.form.get("name").value() != "alpha" || m.form.unit().Path() != filepath.Join(systemd.UserUnitDir(), "alpha.service") || m.form.get("watch").value() != "yes" {
		t.Fatalf("RET on a binary: mode=%v", m.mode)
	}
}
