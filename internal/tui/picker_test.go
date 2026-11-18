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
	enter := tea.KeyMsg{Type: tea.KeyEnter}

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

	// C-n moves to the list; SPC does nothing there, RET toggles tracking.
	m.handleKey(cn)
	if !m.picker.listFocus {
		t.Fatal("C-n did not focus the list")
	}
	m.handleKey(space)
	if m.cfg.Index("web.service", config.User) >= 0 || m.picker.ti.Value() != "web run" {
		t.Fatalf("SPC in the list: tracked=%v filter=%q, want nothing to happen",
			m.cfg.Index("web.service", config.User) >= 0, m.picker.ti.Value())
	}
	m.handleKey(enter)
	if m.mode != modePicker || m.cfg.Index("web.service", config.User) < 0 {
		t.Fatalf("RET in the list: mode=%v, want web.service tracked and the picker still open", m.mode)
	}
	m.handleKey(enter)
	if m.cfg.Index("web.service", config.User) >= 0 {
		t.Fatal("second RET did not untrack web.service")
	}

	// RET in the filter goes to the list instead of leaving.
	m = open()
	m.handleKey(enter)
	if m.mode != modePicker || !m.picker.listFocus {
		t.Fatalf("RET in the filter: mode=%v listFocus=%v", m.mode, m.picker.listFocus)
	}
	// C-g leaves from the list, as from the filter.
	m.handleKey(cg)
	if m.mode != modeList || m.picker != nil {
		t.Fatal("C-g in the list did not return to the tracked list")
	}
	m = open()
	m.handleKey(cg)
	if m.mode != modeList || m.picker != nil {
		t.Fatal("C-g in the filter did not return to the tracked list")
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

	// Add Services: M-down from the filter goes into the list, at the last row.
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
