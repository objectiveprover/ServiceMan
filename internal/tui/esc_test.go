package tui

import (
	"errors"
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

func TestEnterClosesPager(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	for _, title := range []string{"JOURNAL", "HELP", "STATUS"} {
		m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, mode: modePager, pager: newPager(title, "hello.service")}
		m.handleKey(enter)
		if m.mode != modeList || m.pager != nil {
			t.Errorf("RET in %q: mode=%v pager=%v, want back at the list", title, m.mode, m.pager)
		}
	}
}

func TestNoViKeysInPager(t *testing.T) {
	for _, k := range []string{"j", "k", "G", "?"} {
		m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, mode: modePager, pager: newPager("HELP", "")}
		m.pager.setLines(make([]string, 100))
		m.pager.h, m.pager.offset = 10, 50
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		if m.pager.offset != 50 || m.prompt != nil || !m.msgErr {
			t.Errorf("%s: offset=%d prompt=%v msgErr=%v, want it undefined", k, m.pager.offset, m.prompt != nil, m.msgErr)
		}
	}
}

func TestSTogglesStartStop(t *testing.T) {
	svc := config.Service{Unit: "hello.service", Scope: config.User}
	s := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}}
	for state, want := range map[string]string{
		"active": "stop hello.service…", "activating": "stop hello.service…",
		"inactive": "start hello.service…", "failed": "start hello.service…", "": "start hello.service…",
	} {
		m := &Model{cfg: &config.Config{Services: []config.Service{svc}},
			status: map[string]systemd.Status{svcKey(svc): {ActiveState: state}}}
		m.handleKey(s) // only queues the systemctl command; nothing runs here
		if m.msg != want {
			t.Errorf("state %q: msg %q, want %q", state, m.msg, want)
		}
	}
}

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
