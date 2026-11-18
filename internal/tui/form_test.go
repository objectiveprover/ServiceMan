package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var (
	keyEnter = tea.KeyMsg{Type: tea.KeyEnter}
	keyCN    = tea.KeyMsg{Type: tea.KeyCtrlN}
	keyCG    = tea.KeyMsg{Type: tea.KeyCtrlG}
)

func binPickerModel(t *testing.T, bins ...string) *Model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	for _, b := range bins {
		os.WriteFile(filepath.Join(dir, b), []byte("#!/bin/sh\n"), 0o755)
	}
	m := &Model{cfg: &config.Config{BinDir: dir}, status: map[string]systemd.Status{}, w: 100, h: 30}
	m.openBinPicker()
	return m
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
	for _, want := range []string{"SERVICEMAN", "New Service", "Filter: alp", "alpha", "Custom command or path", "1/3 executables"} {
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
	if m.mode != modeForm || m.form.get("name").value() != "alpha" || m.form.scope != config.User || m.form.get("watch").value() != "yes" {
		t.Fatalf("RET on a binary: mode=%v", m.mode)
	}
}

func TestFormView(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 80, h: 20}
	m.openForm(nil)
	view := ansi.Strip(m.View())
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[0], "SERVICEMAN") || !strings.Contains(lines[0], "New Service") {
		t.Errorf("top bar = %q", lines[0])
	}
	if strings.Contains(view, "C-n next") || strings.Contains(view, "C-c C-c install") {
		t.Errorf("form repeats the key hints:\n%s", view)
	}
	// Label and description on one line, the input below.
	if !strings.Contains(lines[2], "Name") || !strings.Contains(lines[2], "unit name; .service is appended") {
		t.Errorf("first field label line = %q", lines[2])
	}
	if len(lines) != m.h {
		t.Errorf("view is %d lines, want %d", len(lines), m.h)
	}

	// C-p on the first field stays there.
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlP})
	if m.form.focus != 0 {
		t.Fatalf("C-p on the first field moved to %d", m.form.focus)
	}
	// The focused field stays visible while moving down a short screen.
	for range m.form.fields {
		m.handleKey(keyCN)
		label := m.form.fields[m.form.focus].label
		if !strings.Contains(ansi.Strip(m.View()), "▸ "+label) {
			t.Fatalf("focused %q not on screen:\n%s", label, ansi.Strip(m.View()))
		}
	}
	// C-n on the last field stays there instead of wrapping to the first.
	if last := len(m.form.fields) - 1; m.form.focus != last {
		t.Fatalf("after moving down past the end: focus=%d, want %d", m.form.focus, last)
	}
}

func TestEditFormReadOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	unit := systemd.NewUnit{Name: "hello", Description: "Hello", Scope: config.User, ExecStart: "/bin/hello", Restart: "on-abnormal"}
	path := filepath.Join(dir, "hello.service")
	// Restart=on-abnormal needs RestartSec=3 to round-trip; Render adds it.
	os.WriteFile(path, []byte(unit.Render()), 0o644)
	svc := config.Service{Unit: "hello.service", Scope: config.User, URL: "http://x", Watch: true}
	m := &Model{cfg: &config.Config{Services: []config.Service{svc}}, w: 80, h: 30,
		status: map[string]systemd.Status{svcKey(svc): {FragmentPath: path, ActiveState: "active", UnitFileState: "enabled"}}}
	m.openEditForm(svc)
	if m.mode != modeForm || m.form.edit == nil {
		t.Fatalf("mode=%v, want the Edit Service form", m.mode)
	}
	f := m.form
	for id, want := range map[string]string{"name": "hello.service", "desc": "Hello", "exec": "/bin/hello",
		"restart": "on-abnormal", "url": "http://x", "watch": "yes", "enable": "yes", "start": "yes"} {
		if got := f.get(id).value(); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
	// The name is read-only: navigation skips it.
	for range f.fields {
		f.move(1)
		if id := f.fields[f.focus].id; id == "name" {
			t.Fatalf("focus landed on read-only %s", id)
		}
	}
	if err := f.validate(); err != nil {
		t.Errorf("validate while editing an existing unit: %v", err)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Edit Service") || f.get("start").label != "Restart now" {
		t.Errorf("edit view:\n%s", v)
	}
}

func TestNewFormIsUserScope(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 300, h: 40}
	m.openForm(&formPrefill{name: "server", exec: "/opt/app/server"})
	f := m.form
	for _, id := range []string{"scope", "user", "network", "wd"} {
		if f.get(id) != nil {
			t.Errorf("New Service form has a %q field", id)
		}
	}
	if u := f.unit(); u.Scope != config.User || u.RunAs != "" || u.Network {
		t.Errorf("unit = %+v, want a user unit", u)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Preview: "+config.Tilde(systemd.UnitDir(config.User))) {
		t.Errorf("preview isn't a user unit:\n%s", v)
	}
	// No WorkingDirectory=: the unit runs in the home directory, wherever
	// serviceman was started.
	if r := f.unit().Render(); strings.Contains(r, "WorkingDirectory=") {
		t.Errorf("new unit sets a working directory:\n%s", r)
	}
}

func TestEditSystemUnitKeepsSystemFields(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	unit := systemd.NewUnit{Name: "web", Scope: config.System, ExecStart: "/bin/web", WorkingDir: "/srv", RunAs: "www-data", Restart: "on-failure", Network: true}
	path := filepath.Join(dir, "web.service")
	os.WriteFile(path, []byte(unit.Render()), 0o644)
	svc := config.Service{Unit: "web.service", Scope: config.System}
	m := &Model{cfg: &config.Config{Services: []config.Service{svc}}, w: 80, h: 30,
		status: map[string]systemd.Status{svcKey(svc): {FragmentPath: path}}}
	m.openEditForm(svc)
	if m.form == nil {
		t.Fatalf("mode=%v, want the Edit Service form", m.mode)
	}
	if u := m.form.unit(); u.Scope != config.System || u.RunAs != "www-data" || !u.Network || u.WorkingDir != "/srv" {
		t.Errorf("unit = %+v, want the system unit's settings", u)
	}
}
