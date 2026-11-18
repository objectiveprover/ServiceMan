package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// prompt is the Emacs-style minibuffer shown in the echo area.
type prompt struct {
	label string
	ti    textinput.Model

	// completion (M-x)
	candidates []candidate
	matches    []int
	sel        int
	nameW      int // width of the command column: the widest label + keys

	onSubmit func(m *Model, v string) tea.Cmd
	onChange func(m *Model, v string)
	onCancel func(m *Model)
	onKey    func(m *Model, k string) (bool, tea.Cmd)
}

func newPrompt(label, initial string) *prompt {
	ti := newTextInput()
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.Focus()
	return &prompt{label: label, ti: ti}
}

func (m *Model) promptKey(msg tea.KeyMsg) tea.Cmd {
	p := m.prompt
	k := msg.String()
	if p.onKey != nil {
		if handled, cmd := p.onKey(m, k); handled {
			return cmd
		}
	}
	switch k {
	case "ctrl+g", "esc":
		m.prompt = nil
		if p.onCancel != nil {
			p.onCancel(m)
		}
		m.setMsg("Quit")
		return nil
	case "enter":
		v := p.ti.Value()
		if len(p.candidates) > 0 {
			if len(p.matches) == 0 {
				m.setErr("[No match]")
				return nil
			}
			v = p.candidates[p.matches[p.sel]].name
		}
		m.prompt = nil
		m.msg = ""
		if p.onSubmit != nil {
			return p.onSubmit(m, v)
		}
		return nil
	}
	if len(p.candidates) > 0 {
		switch k {
		// Stop at the first and last commands; lists never wrap around.
		case "ctrl+n", "down", "tab":
			p.sel = max(0, min(p.sel+1, len(p.matches)-1))
			return nil
		case "ctrl+p", "up", "shift+tab":
			p.sel = max(p.sel-1, 0)
			return nil
		}
	}
	before := p.ti.Value()
	var cmd tea.Cmd
	p.ti, cmd = p.ti.Update(msg)
	if v := p.ti.Value(); v != before {
		if len(p.candidates) > 0 {
			p.refilter()
		}
		if p.onChange != nil {
			p.onChange(m, v)
		}
	}
	return cmd
}

func (p *prompt) view(w int) string {
	return trunc(gutter+sKey.Render(p.label)+p.ti.View(), w)
}

// yesOrNo asks an Emacs-style yes-or-no question; only "yes" confirms.
func (m *Model) yesOrNo(question string, onYes func(m *Model) tea.Cmd) {
	p := newPrompt(question+" (yes or no) ", "")
	p.onSubmit = func(m *Model, v string) tea.Cmd {
		if strings.EqualFold(strings.TrimSpace(v), "yes") {
			return onYes(m)
		}
		m.setMsg("Cancelled")
		return nil
	}
	m.prompt = p
}
