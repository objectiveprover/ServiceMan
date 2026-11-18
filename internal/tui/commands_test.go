package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

func TestSTogglesStartStop(t *testing.T) {
	svc := config.Service{Unit: "hello.service", Scope: config.User}
	s := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}
	for state, want := range map[string]string{
		"active": "stop hello.service…", "activating": "stop hello.service…",
		"inactive": "start hello.service…", "failed": "start hello.service…", "": "start hello.service…",
	} {
		m := &Model{cfg: &config.Config{Services: []config.Service{svc}},
			status: map[string]systemd.Status{svc.Key(): {ActiveState: state}}}
		m.handleKey(s) // only queues the systemctl command; nothing runs here
		if m.msg != want {
			t.Errorf("state %q: msg %q, want %q", state, m.msg, want)
		}
	}
}
