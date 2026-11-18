package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

func TestMessagesExpire(t *testing.T) {
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 80}
	m.setMsg("first")
	first := m.msgSeq
	m.setMsg("second")

	// A timer from an older message doesn't clear a newer one.
	m.Update(clearMsgMsg{first})
	if m.msg != "second" {
		t.Fatalf("stale timer cleared the message: %q", m.msg)
	}
	m.Update(clearMsgMsg{m.msgSeq})
	if m.msg != "" {
		t.Fatalf("message not cleared: %q", m.msg)
	}

	// A message set while handling an event schedules its own timer.
	if _, cmd := m.Update(actionDoneMsg{desc: "restart hello.service"}); cmd == nil || m.msg != "restart hello.service: done" {
		t.Fatalf("msg %q cmd %v", m.msg, cmd)
	}

	// Errors stay until the next key.
	if _, cmd := m.Update(actionDoneMsg{desc: "restart hello.service", err: errors.New("boom")}); cmd == nil || !m.msgErr {
		t.Fatalf("error not shown: msg %q", m.msg)
	}
	m.Update(clearMsgMsg{m.msgSeq})
	if m.msg != "restart hello.service: boom" {
		t.Fatalf("error expired: %q", m.msg)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.msg != "" || m.msgErr {
		t.Fatalf("next key didn't clear the error: %q", m.msg)
	}

	// Prefix prompts (C-x-) wait for the next key instead of expiring.
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlX})
	m.Update(clearMsgMsg{m.msgSeq})
	if m.msg != "C-x-" {
		t.Fatalf("prefix prompt expired: %q", m.msg)
	}
}
