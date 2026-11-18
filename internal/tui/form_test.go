package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

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
	// A text field: name and description on one line, the input below
	// after ❯, then a ─ line.
	if !strings.Contains(lines[2], "Name  The unit's name; .service is added for you") || strings.Contains(lines[2], "─") {
		t.Errorf("first field label line = %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "  ❯ ") {
		t.Errorf("first field input line = %q", lines[3])
	}
	if strings.Trim(lines[4], " ─") != "" || !strings.Contains(lines[4], "──") {
		t.Errorf("first field bottom line = %q", lines[4])
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
		if v := ansi.Strip(m.View()); !strings.Contains(v, "▸ "+label) {
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
		status: map[string]systemd.Status{svc.Key(): {FragmentPath: path, ActiveState: "active", UnitFileState: "enabled"}}}
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
	m.showPreview = true
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
		status: map[string]systemd.Status{svc.Key(): {FragmentPath: path}}}
	m.openEditForm(svc)
	if m.form == nil {
		t.Fatalf("mode=%v, want the Edit Service form", m.mode)
	}
	if u := m.form.unit(); u.Scope != config.System || u.RunAs != "www-data" || !u.Network || u.WorkingDir != "/srv" {
		t.Errorf("unit = %+v, want the system unit's settings", u)
	}
	if v := ansi.Strip(m.View()); strings.Contains(v, "Preview: ") || !strings.Contains(v, "Preview (C-c C-p): off") {
		t.Errorf("Edit Service shows the preview by default:\n%s", v)
	}
}

// On a short screen the first focusable field scrolls the form to the top,
// so the read-only Name above it shows instead of a "↑ more" marker; the
// last field scrolls it to the bottom and has no ─ line under it.
func TestFormScrollEnds(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	unit := systemd.NewUnit{Name: "hello", Description: "Hello", Scope: config.User, ExecStart: "/bin/hello", Restart: "on-failure"}
	path := filepath.Join(dir, "hello.service")
	os.WriteFile(path, []byte(unit.Render()), 0o644)
	svc := config.Service{Unit: "hello.service", Scope: config.User}
	for h := 11; h < 30; h++ { // from 11 rows, Name and Description fit with the ↓ marker
		m := &Model{cfg: &config.Config{Services: []config.Service{svc}}, w: 80, h: h,
			status: map[string]systemd.Status{svc.Key(): {FragmentPath: path, ActiveState: "active", UnitFileState: "enabled"}}}
		m.openEditForm(svc)
		for range m.form.fields {
			m.handleKey(keyCN)
		}
		v := ansi.Strip(m.View())
		if strings.Contains(v, "↓ more") {
			t.Errorf("h=%d: last field focused but ↓ more shows:\n%s", h, v)
		}
		lines := strings.Split(v, "\n")
		// The field list ends just above the footer's rule and key hints.
		if l := lines[len(lines)-3]; strings.Trim(l, " ─") == "" {
			t.Errorf("h=%d: last field ends with a ─ line:\n%s", h, v)
		}
		for range m.form.fields {
			m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlP})
		}
		if v := ansi.Strip(m.View()); strings.Contains(v, "↑ more") || !strings.Contains(v, "Name") {
			t.Errorf("h=%d: first editable field focused but the top is hidden:\n%s", h, v)
		}
	}
}

// The unit preview is hidden at first; C-c C-p shows and hides it, the top
// bar says which, and the choice carries over to the next form.
func TestFormPreviewToggle(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cCp := func(m *Model) {
		m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
		m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlP})
	}
	for _, w := range []int{80, 130} {
		m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: w, h: 30}
		m.openForm(nil)
		v := ansi.Strip(m.View())
		top := strings.Split(v, "\n")[0]
		if strings.Contains(v, "Preview: ") || !strings.Contains(top, "Preview (C-c C-p): off") || !strings.Contains(top, "Daemon (D)") {
			t.Fatalf("w=%d: preview shown by default:\n%s", w, v)
		}
		cCp(m)
		v = ansi.Strip(m.View())
		if !strings.Contains(v, "Preview: ") || !strings.Contains(strings.Split(v, "\n")[0], "Preview (C-c C-p): on") {
			t.Fatalf("w=%d: C-c C-p didn't show the preview:\n%s", w, v)
		}
		if m.form.focus != 0 || m.msgErr {
			t.Fatalf("w=%d: C-c C-p moved focus or errored: focus=%d msg=%q", w, m.form.focus, m.msg)
		}
		m.form, m.mode = nil, modeList
		m.openForm(nil)
		if v := ansi.Strip(m.View()); !strings.Contains(v, "Preview: ") {
			t.Fatalf("w=%d: next form hides the preview again:\n%s", w, v)
		}
		cCp(m)
		if v := ansi.Strip(m.View()); strings.Contains(v, "Preview: ") {
			t.Fatalf("w=%d: second C-c C-p didn't hide the preview:\n%s", w, v)
		}
	}
}

// A selector's chosen option is green like its ✓; the top bar sits on a
// heavy rule.
func TestChosenOptionAndTopBarRule(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 100, h: 60}
	m.openForm(nil)
	f := m.form.get("watch")
	if f.choices == nil {
		t.Fatal("watch field is not a selector")
	}
	view := m.View()
	if want := sChosen.Render(choiceLabel(f.value())) + " " + sGreen.Render("✓"); !strings.Contains(view, want) {
		t.Errorf("view lacks the green chosen option %q", want)
	}
	if lines := strings.Split(ansi.Strip(view), "\n"); !strings.HasPrefix(lines[1], gutter+"━") {
		t.Errorf("line under the top bar = %q, want a heavy rule", lines[1])
	}
}
