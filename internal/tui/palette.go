package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// candidate is one M-x entry: the command's name ("Set env file") and its
// keys in Emacs notation ("C-k, d").
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
		desc := sDesc.Render(c.desc)
		if i == p.sel {
			desc = sDescSel.Render(c.desc)
		}
		line := cursorMark(i == p.sel) + pad(c.cell(i == p.sel), p.nameW) + colGap + " " + desc
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
