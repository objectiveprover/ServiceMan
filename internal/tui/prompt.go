package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// newTextInput returns a textinput with Emacs-flavoured bindings
// (C-a C-e C-f C-b M-f M-b C-k C-u C-w M-d C-d are bubbles defaults; C-y yanks).
func newTextInput() textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.KeyMap.Paste = key.NewBinding(key.WithKeys("ctrl+y"))
	ti.KeyMap.NextSuggestion = key.NewBinding(key.WithDisabled())
	ti.KeyMap.PrevSuggestion = key.NewBinding(key.WithDisabled())
	ti.KeyMap.AcceptSuggestion = key.NewBinding(key.WithDisabled())
	return ti
}

// prompt is the Emacs-style minibuffer shown in the echo area.
type prompt struct {
	label string
	ti    textinput.Model

	// completion (M-x)
	candidates []candidate
	matches    []int
	sel        int

	onSubmit func(m *Model, v string) tea.Cmd
	onChange func(m *Model, v string)
	onCancel func(m *Model)
	onKey    func(m *Model, k string) (bool, tea.Cmd)
}

type candidate struct{ name, annotation string }

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
		case "ctrl+n", "down", "tab":
			if len(p.matches) > 0 {
				p.sel = (p.sel + 1) % len(p.matches)
			}
			return nil
		case "ctrl+p", "up", "shift+tab":
			if len(p.matches) > 0 {
				p.sel = (p.sel - 1 + len(p.matches)) % len(p.matches)
			}
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

func (p *prompt) refilter() {
	p.matches = p.matches[:0]
	q := p.ti.Value()
	for i, c := range p.candidates {
		if matchAll(q, c.name+" "+c.annotation) {
			p.matches = append(p.matches, i)
		}
	}
	p.sel = 0
}

func (p *prompt) view(w int) string {
	return trunc(sKey.Render(p.label)+p.ti.View(), w)
}

func (p *prompt) candidatesView(w, maxH int) string {
	if len(p.candidates) == 0 {
		return ""
	}
	maxH = max(3, maxH)
	start := 0
	if p.sel >= maxH {
		start = p.sel - maxH + 1
	}
	var lines []string
	for i := start; i < len(p.matches) && len(lines) < maxH; i++ {
		c := p.candidates[p.matches[i]]
		line := pad(c.name, 22) + sDim.Render(c.annotation)
		if i == p.sel {
			line = sSelected.Render(pad(c.name, 22)) + " " + c.annotation
		}
		lines = append(lines, trunc(line, w))
	}
	if len(lines) == 0 {
		lines = append(lines, sDim.Render("[No match]"))
	}
	return strings.Join(lines, "\n")
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

// openPalette is M-x: run any command by (fuzzy) name.
func (m *Model) openPalette() tea.Cmd {
	p := newPrompt("M-x ", "")
	for _, c := range commands {
		if c.name == "execute-command" {
			continue
		}
		var ks []string
		for _, k := range strings.Fields(c.keys) {
			ks = append(ks, emacsKey(k))
		}
		ann := c.desc
		if len(ks) > 0 {
			ann = "(" + strings.Join(ks, ", ") + ")  " + ann
		}
		p.candidates = append(p.candidates, candidate{c.name, ann})
	}
	p.refilter()
	p.onSubmit = func(m *Model, name string) tea.Cmd {
		for _, c := range commands {
			if c.name == name {
				return runCommand(m, c)
			}
		}
		return nil
	}
	m.prompt = p
	return nil
}
