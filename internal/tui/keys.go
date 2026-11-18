package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()

	// As in Emacs, the echo area is cleared by the next key, which brings the
	// key reference back.
	if m.prefix == "" {
		m.msg, m.msgErr = "", false
	}

	// ESC is a Meta prefix, as in Emacs: ESC x == M-x. ESC ESC (or a fast
	// double tap, which arrives as M-ESC) falls through as a plain ESC, which
	// cancels like C-g.
	if k == "alt+esc" {
		msg.Alt, k = false, "esc"
		if m.prefix == "esc" {
			m.prefix, m.msg = "", ""
		}
	} else if m.prefix == "esc" {
		m.prefix, m.msg = "", ""
		if k == "ctrl+g" {
			m.setMsg("Quit")
			return nil
		}
		if k != "esc" {
			msg.Alt = true
			k = msg.String()
		}
	} else if k == "esc" && m.prefix == "" {
		m.prefix = k
		m.setMsg("ESC-")
		return nil
	}

	if m.prefix != "" {
		p := m.prefix
		m.prefix = ""
		combo := p + " " + k
		if combo == "ctrl+x ctrl+c" {
			return m.quit()
		}
		if k == "ctrl+g" {
			m.setMsg("Quit")
			return nil
		}
		if m.mode == modeForm && m.prompt == nil {
			if cmd, ok := m.form.prefixKey(m, combo); ok {
				return cmd
			}
		}
		m.setErr("%s is undefined", emacsKey(combo))
		return nil
	}
	if k == "ctrl+x" || (k == "ctrl+c" && m.prompt == nil) {
		m.prefix = k
		m.setMsg("%s-", emacsKey(k))
		return nil
	}

	if m.prompt != nil {
		return m.promptKey(msg)
	}

	switch m.mode {
	case modePager:
		return m.pager.key(m, msg)
	case modePicker:
		return m.picker.key(m, msg)
	case modeBinPicker:
		return m.bins.key(m, msg)
	case modeForm:
		return m.form.key(m, msg)
	default:
		return m.listKey(msg)
	}
}

// emacsKey turns bubbletea key names into Emacs notation.
func emacsKey(k string) string {
	r := strings.NewReplacer("ctrl+", "C-", "alt+", "M-", "shift+", "S-", "enter", "RET",
		"esc", "ESC", "tab", "TAB", "backspace", "DEL")
	return r.Replace(k)
}
