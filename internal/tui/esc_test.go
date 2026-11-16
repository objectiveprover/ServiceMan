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
