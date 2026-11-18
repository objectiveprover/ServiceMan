package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

func TestEnterClosesPager(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	for _, title := range []string{"Journal", "Keybindings", "Status"} {
		m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, mode: modePager, pager: newPager(title, "hello.service")}
		m.handleKey(enter)
		if m.mode != modeList || m.pager != nil {
			t.Errorf("RET in %q: mode=%v pager=%v, want back at the list", title, m.mode, m.pager)
		}
	}
}

func TestNoViKeysInPager(t *testing.T) {
	for _, k := range []string{"j", "k", "G", "?"} {
		m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, mode: modePager, pager: newPager("Keybindings", "")}
		m.pager.setLines(make([]string, 100))
		m.pager.h, m.pager.offset = 10, 50
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
		if m.pager.offset != 50 || m.prompt != nil || !m.msgErr {
			t.Errorf("%s: offset=%d prompt=%v msgErr=%v, want it undefined", k, m.pager.offset, m.prompt != nil, m.msgErr)
		}
	}
}

func TestPagerTopBottomKeys(t *testing.T) {
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, mode: modePager, w: 120, pager: newPager("Journal", "hello.service")}
	m.pager.live = true
	m.pager.setLines(make([]string, 100))
	m.pager.h, m.pager.offset = 10, 50
	m.handleKey(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	if m.pager.offset != 0 || m.pager.follow {
		t.Errorf("M-up: offset=%d follow=%v, want the top with follow off", m.pager.offset, m.pager.follow)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	if m.pager.offset != m.pager.bottomOffset() || !m.pager.follow {
		t.Errorf("M-down: offset=%d follow=%v, want the bottom with follow on", m.pager.offset, m.pager.follow)
	}

	// Follow and Wrap show their keys in the top bar, not in the bottom bar.
	hints := ansi.Strip(m.keyHints())
	if strings.Contains(hints, "Follow") || strings.Contains(hints, "Wrap") || !strings.Contains(hints, "Top/Bottom (M-↑/M-↓)") {
		t.Errorf("journal bottom bar = %q", hints)
	}
}

func TestPagerHeader(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	p := newPager("Journal", "hello.service")
	p.live = true
	p.setLines([]string{"a", "b", "c"})
	p.view(100, 10)
	h := p.header(100)
	if !strings.Contains(h, sGreen.Render("● Live")) {
		t.Errorf("live journal header lacks a green ● Live: %q", ansi.Strip(h))
	}
	for _, want := range []string{"SERVICEMAN", "Journal", "hello.service", "Follow (f): on", "Wrap (w): on", "Lines 1–3 of 3", "All"} {
		if !strings.Contains(ansi.Strip(h), want) {
			t.Errorf("header %q lacks %q", ansi.Strip(h), want)
		}
	}
	if w := ansi.StringWidth(strings.Split(h, "\n")[0]); w != 100 {
		t.Errorf("header is %d wide, want 100", w)
	}

	p.live, p.ended = false, "stream ended"
	if h := ansi.Strip(p.header(80)); !strings.Contains(h, "○ Ended") || strings.Contains(h, "Live") {
		t.Errorf("ended journal header %q", h)
	}

	help := newPager("Keybindings", "")
	help.setLines([]string{"x"})
	help.view(100, 10)
	if h := ansi.Strip(help.header(80)); strings.Contains(h, "Follow") || strings.Contains(h, "Live") {
		t.Errorf("help header shows journal-only state: %q", h)
	}
}

// On the Keybindings page, C-s filters: only matching rows stay, under their
// section headings; C-g clears the filter before it goes back.
func TestHelpFilter(t *testing.T) {
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 120, h: 40}
	m.showHelp()
	all := len(m.pager.lines)
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.prompt == nil || m.prompt.label != "Filter: " {
		t.Fatalf("C-s on Keybindings: prompt = %+v", m.prompt)
	}
	m.handleKey(runes("unit delete"))
	view := ansi.Strip(m.View())
	for _, want := range []string{"New Service (c): pick a binary", "C-d on a binary", "Filter: unit delete"} {
		if !strings.Contains(view, want) {
			t.Errorf("filtered view lacks %q:\n%s", want, view)
		}
	}
	for _, gone := range []string{"Navigation", "Next service", "Log / status viewer"} {
		if strings.Contains(view, gone) {
			t.Errorf("filtered view still shows %q:\n%s", gone, view)
		}
	}
	m.handleKey(keyEnter)
	m.handleKey(runes("x")) // undefined, but the filter stays
	if m.pager.filter != "unit delete" || len(m.pager.lines) >= all {
		t.Fatalf("after RET: filter=%q lines=%d of %d", m.pager.filter, len(m.pager.lines), all)
	}
	m.handleKey(keyCG)
	if m.mode != modePager || m.pager.filter != "" || len(m.pager.lines) != all {
		t.Fatalf("C-g: mode=%v filter=%q lines=%d of %d, want the whole page", m.mode, m.pager.filter, len(m.pager.lines), all)
	}
	m.handleKey(keyCG)
	if m.mode != modeList {
		t.Fatalf("second C-g: mode=%v, want the list", m.mode)
	}

	m.showHelp()
	m.handleKey(runes("/")) // C-s is the only filter key
	if m.prompt != nil {
		t.Fatal("/ opened a prompt on Keybindings")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	m.handleKey(runes("zzzz"))
	if v := ansi.Strip(m.View()); !strings.Contains(v, "No keybindings match the filter (C-g clears it).") {
		t.Errorf("no matches:\n%s", v)
	}
}
