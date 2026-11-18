package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

func TestEscIsMetaPrefix(t *testing.T) {
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	x := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}
	newModel := func() *Model {
		return &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}}
	}

	// ESC x opens the M-x prompt.
	m := newModel()
	m.handleKey(esc)
	if m.prefix != "esc" || m.msg != "ESC-" {
		t.Fatalf("after ESC: prefix=%q msg=%q", m.prefix, m.msg)
	}
	m.handleKey(x)
	if m.prompt == nil || m.prompt.label != "M-x " {
		t.Fatalf("ESC x did not open M-x (prompt=%v)", m.prompt)
	}
	if m.prefix != "" {
		t.Fatalf("prefix not cleared: %q", m.prefix)
	}

	// ESC ESC cancels the open prompt.
	m.handleKey(esc)
	m.handleKey(esc)
	if m.prompt != nil || m.prefix != "" || m.msg != "Quit" {
		t.Fatalf("ESC ESC: prompt=%v prefix=%q msg=%q", m.prompt, m.prefix, m.msg)
	}

	// A fast double tap arrives as M-ESC and also cancels.
	m = newModel()
	m.filter = "web"
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc, Alt: true})
	if m.filter != "" || m.prefix != "" {
		t.Fatalf("M-ESC: filter=%q prefix=%q", m.filter, m.prefix)
	}

	// ESC C-g aborts the prefix without doing anything else.
	m = newModel()
	m.filter = "web"
	m.handleKey(esc)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlG})
	if m.filter != "web" || m.prefix != "" || m.msg != "Quit" {
		t.Fatalf("ESC C-g: filter=%q prefix=%q msg=%q", m.filter, m.prefix, m.msg)
	}
}

// M-up / M-down jump to the first / last row; M-< / M-> still do too.
func TestFirstLastKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mUp, mDown := tea.KeyMsg{Type: tea.KeyUp, Alt: true}, tea.KeyMsg{Type: tea.KeyDown, Alt: true}
	mLt := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'<'}, Alt: true}
	mGt := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'>'}, Alt: true}

	// Service list.
	svcs := []config.Service{{Unit: "a.service", Scope: config.User}, {Unit: "b.service", Scope: config.User}, {Unit: "c.service", Scope: config.User}}
	m := &Model{cfg: &config.Config{Services: svcs}, status: map[string]systemd.Status{}, w: 100, h: 30}
	for _, c := range []struct {
		k    tea.KeyMsg
		want int
	}{{mDown, 2}, {mUp, 0}, {mGt, 2}, {mLt, 0}} {
		m.handleKey(c.k)
		if m.cursor != c.want {
			t.Errorf("list %s: cursor=%d, want %d", c.k, m.cursor, c.want)
		}
	}
	// ESC ↑ is M-↑ (ESC is the Meta prefix).
	m.handleKey(mDown)
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("list ESC ↑: cursor=%d, want 0", m.cursor)
	}

	// Track Services: M-down from the filter goes into the list, at the last row.
	m = &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}}
	m.openPicker()
	m.picker.loaded(pickerLoadedMsg{entries: []systemd.UnitEntry{
		{Unit: "web.service", Scope: config.User}, {Unit: "db.service", Scope: config.User}, {Unit: "x.service", Scope: config.User},
	}})
	m.handleKey(mDown)
	if !m.picker.listFocus || m.picker.cursor != len(m.picker.shown)-1 {
		t.Errorf("picker M-down: listFocus=%v cursor=%d", m.picker.listFocus, m.picker.cursor)
	}
	m.handleKey(mUp)
	if m.picker.cursor != 0 {
		t.Errorf("picker M-up: cursor=%d", m.picker.cursor)
	}

	// New Service binaries.
	m = binPickerModel(t, "alpha", "beta", "gamma")
	m.handleKey(mDown)
	if m.bins.cursor != len(m.bins.shown)-1 {
		t.Errorf("binary picker M-down: cursor=%d", m.bins.cursor)
	}
	m.handleKey(mUp)
	if m.bins.cursor != 0 {
		t.Errorf("binary picker M-up: cursor=%d", m.bins.cursor)
	}
}
