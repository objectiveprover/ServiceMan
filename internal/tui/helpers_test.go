package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
