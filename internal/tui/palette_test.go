package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

// M-x is a full-height screen with the standard top bar; the footer keeps
// the M-x input.
func TestPaletteFullScreen(t *testing.T) {
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 100, h: 30}
	m.openPalette()
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if len(lines) != m.h {
		t.Fatalf("view has %d lines, want %d", len(lines), m.h)
	}
	for _, want := range []string{"SERVICEMAN", "Commands", "commands", "Cancel (C-g/ESC ESC)"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("top bar %q lacks %q", lines[0], want)
		}
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, gutter+"M-x ") {
		t.Errorf("last line = %q, want the M-x input", last)
	}
	rows := 0
	for _, l := range lines[2 : len(lines)-2] {
		if strings.TrimSpace(l) != "" {
			rows++
		}
	}
	// Readable names with their keys beside them; the command column is wide
	// enough for every name, so only descriptions get cut off.
	view := strings.Join(lines, "\n")
	for _, want := range []string{"Follow journal (RET)", "Reload unit files ", "Untrack (C-k, d)"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	if want := min(len(m.prompt.candidates), m.h-4); rows != want {
		t.Errorf("%d command rows, want %d", rows, want)
	}
	// Descriptions match too.
	m.prompt.ti.SetValue("daemon-reload")
	m.prompt.refilter()
	if len(m.prompt.matches) != 1 || m.prompt.candidates[m.prompt.matches[0]].name != "Reload unit files" {
		t.Errorf("daemon-reload matches %v", m.prompt.matches)
	}
}

// The command list stops at its ends instead of wrapping around.
func TestPaletteNoWrap(t *testing.T) {
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 100, h: 30}
	m.openPalette()
	p := m.prompt
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlP})
	if p.sel != 0 {
		t.Fatalf("C-p on the first command: sel=%d, want 0", p.sel)
	}
	for range p.matches {
		m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlN})
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlN})
	if last := len(p.matches) - 1; p.sel != last {
		t.Fatalf("C-n past the last command: sel=%d, want %d", p.sel, last)
	}
	// No matches: moving does nothing.
	p.ti.SetValue("zzzz")
	p.refilter()
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlN})
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlP})
	if p.sel != 0 {
		t.Fatalf("no matches: sel=%d, want 0", p.sel)
	}
}
