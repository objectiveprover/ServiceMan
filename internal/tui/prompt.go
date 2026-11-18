package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
	nameW      int // width of the command column: the widest label + keys

	onSubmit func(m *Model, v string) tea.Cmd
	onChange func(m *Model, v string)
	onCancel func(m *Model)
	onKey    func(m *Model, k string) (bool, tea.Cmd)
}

// candidate is one M-x entry: the command's name ("Set env file") and its
// keys in Emacs notation ("C-s, /").
type candidate struct{ name, keys, desc string }

// cell is the command column's text: the label, then its keys.
func (c candidate) cell(here bool) string {
	label := c.name
	if here {
		label = sHighlight.Render(label)
	}
	if c.keys == "" {
		return label
	}
	return label + " " + sDim.Render("(") + sKey.Render(c.keys) + sDim.Render(")")
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

func (p *prompt) refilter() {
	p.matches = p.matches[:0]
	q := p.ti.Value()
	for i, c := range p.candidates {
		if matchAll(q, c.name+" "+c.keys+" "+c.desc) {
			p.matches = append(p.matches, i)
		}
	}
	p.sel = 0
}

func (p *prompt) view(w int) string {
	return trunc(gutter+sKey.Render(p.label)+p.ti.View(), w)
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
		// The command column never shrinks; long descriptions get cut off.
		desc := sDim.Render(c.desc)
		if i == p.sel {
			desc = c.desc
		}
		line := cursorMark(i == p.sel) + pad(c.cell(i == p.sel), p.nameW) + "   " + desc
		lines = append(lines, trunc(line, w))
	}
	if len(lines) == 0 {
		lines = append(lines, gutter+sDim.Render("[No match]"))
	}
	return strings.Join(lines, "\n")
}

// paletteOpen reports whether the M-x command list is showing (only the
// palette prompt has candidates); it then takes the whole screen.
func (m *Model) paletteOpen() bool {
	return m.prompt != nil && len(m.prompt.candidates) > 0
}

// paletteView is the full-height M-x screen above the footer: the usual top
// bar, then the matching commands with their keys.
func (m *Model) paletteView(w, h int) string {
	p := m.prompt
	count := sDim.Render(fmt.Sprintf("%d/%d commands", len(p.matches), len(p.candidates)))
	cancel := sDim.Render("Cancel (") + sKey.Render("C-g/ESC ESC") + sDim.Render(")")
	return topBar(w, "Commands", []string{count}, cancel) + "\n" + p.candidatesView(w, h-2)
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
		if c.name == "Run command" {
			continue
		}
		var ks []string
		for _, k := range strings.Fields(c.keys) {
			ks = append(ks, emacsKey(k))
		}
		cd := candidate{name: c.name, keys: strings.Join(ks, ", "), desc: c.desc}
		p.candidates = append(p.candidates, cd)
		p.nameW = max(p.nameW, ansi.StringWidth(cd.cell(false)))
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
