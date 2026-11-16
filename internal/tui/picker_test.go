package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

func TestPickerFocus(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	runes := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	space := tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	cg := tea.KeyMsg{Type: tea.KeyCtrlG}
	cn := tea.KeyMsg{Type: tea.KeyCtrlN}

	open := func() *Model {
		m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}}
		m.openPicker()
		m.picker.loaded(pickerLoadedMsg{entries: []systemd.UnitEntry{
			{Unit: "web.service", Scope: config.User, ActiveState: "active", SubState: "running"},
			{Unit: "db.service", Scope: config.User, ActiveState: "inactive", SubState: "dead"},
		}})
		return m
	}

	// Space types into the filter; nothing is highlighted while filtering.
	m := open()
	for _, k := range []tea.KeyMsg{runes("web"), space, runes("run")} {
		m.handleKey(k)
	}
	if got := m.picker.ti.Value(); got != "web run" {
		t.Fatalf("filter = %q, want %q", got, "web run")
	}
	if m.picker.listFocus || len(m.picker.shown) != 1 {
		t.Fatalf("listFocus=%v shown=%d", m.picker.listFocus, len(m.picker.shown))
	}

	// C-n moves to the list; Space then toggles tracking.
	m.handleKey(cn)
	if !m.picker.listFocus {
		t.Fatal("C-n did not focus the list")
	}
	m.handleKey(space)
	if m.cfg.Index("web.service", config.User) < 0 {
		t.Fatal("SPC in the list did not track web.service")
	}
	if m.picker.ti.Value() != "web run" {
		t.Fatalf("SPC in the list changed the filter: %q", m.picker.ti.Value())
	}

	// C-g returns to the filter, a second C-g leaves the picker.
	m.handleKey(cg)
	if m.mode != modePicker || m.picker.listFocus {
		t.Fatalf("first C-g: mode=%v listFocus=%v", m.mode, m.picker.listFocus)
	}
	m.handleKey(cg)
	if m.mode != modeList || m.picker != nil {
		t.Fatal("second C-g did not return to the tracked list")
	}

	// ESC ESC from the list leaves the picker directly.
	m = open()
	m.handleKey(cn)
	m.handleKey(esc)
	m.handleKey(esc)
	if m.mode != modeList || m.picker != nil {
		t.Fatal("ESC ESC did not return to the tracked list")
	}

	// Typing while in the list goes back to the filter.
	m = open()
	m.handleKey(cn)
	m.handleKey(runes("d"))
	if m.picker.listFocus || m.picker.ti.Value() != "d" {
		t.Fatalf("typing in list: listFocus=%v filter=%q", m.picker.listFocus, m.picker.ti.Value())
	}
}
