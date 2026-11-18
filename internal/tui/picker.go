package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

type picker struct {
	ti             textinput.Model
	all            []systemd.UnitEntry
	shown          []int
	cursor, offset int
	loading        bool
	err            string
	h              int
	// listFocus is true while navigating the unit list; false while typing
	// in the filter, during which no row is highlighted.
	listFocus bool
}

type pickerLoadedMsg struct {
	entries []systemd.UnitEntry
	err     error
}

func (m *Model) openPicker() tea.Cmd {
	ti := newTextInput()
	ti.Focus()
	m.picker = &picker{ti: ti, loading: true}
	m.mode = modePicker
	return func() tea.Msg {
		var all []systemd.UnitEntry
		var firstErr error
		for _, sc := range []config.Scope{config.System, config.User} {
			e, err := systemd.ListAll(sc)
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", sc, err)
			}
			all = append(all, e...)
		}
		return pickerLoadedMsg{all, firstErr}
	}
}

func (p *picker) loaded(msg pickerLoadedMsg) {
	p.loading = false
	p.all = msg.entries
	if msg.err != nil {
		p.err = msg.err.Error()
	}
	p.refilter()
}

func (p *picker) refilter() {
	p.shown = p.shown[:0]
	q := p.ti.Value()
	for i, e := range p.all {
		if matchAll(q, e.Unit+" "+e.Description+" "+string(e.Scope)+" "+e.ActiveState+" "+e.SubState+" "+e.FileState) {
			p.shown = append(p.shown, i)
		}
	}
	p.cursor = min(p.cursor, max(0, len(p.shown)-1))
}

func (p *picker) close(m *Model) tea.Cmd {
	m.picker, m.mode = nil, modeList
	m.setMsg("")
	return m.refreshCmd()
}

func (p *picker) focusFilter(m *Model) {
	p.listFocus = false
	p.ti.Focus()
}

func (p *picker) focusList(m *Model) {
	p.listFocus = true
	p.ti.Blur()
}

func isPickerNav(k string) bool {
	switch k {
	case "ctrl+n", "down", "ctrl+p", "up", "ctrl+v", "pgdown", "alt+v", "pgup", "alt+<", "alt+>":
		return true
	}
	return false
}

func (p *picker) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	// Only C-g and ESC ESC leave the picker, from either focus. (ESC ESC
	// reaches here as a plain "esc": a single ESC is the Meta prefix.)
	if k == "esc" || k == "ctrl+g" {
		return p.close(m)
	}
	if !p.listFocus {
		return p.filterKey(m, msg)
	}
	return p.listKey(m, msg)
}

func (p *picker) filterKey(m *Model, msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	switch {
	case k == "enter" || isPickerNav(k): // RET goes to the list too
		if p.loading || len(p.shown) == 0 {
			return nil
		}
		p.focusList(m)
		// The first C-n/C-p just lands on the current row; paging and
		// M-< / M-> also move.
		switch k {
		case "ctrl+n", "down", "ctrl+p", "up", "enter":
			return nil
		}
		return p.listKey(m, msg)
	}
	before := p.ti.Value()
	var cmd tea.Cmd
	p.ti, cmd = p.ti.Update(msg)
	if p.ti.Value() != before {
		p.cursor = 0
		p.refilter()
	}
	return cmd
}

func (p *picker) listKey(m *Model, msg tea.KeyMsg) tea.Cmd {
	page := max(1, p.h-3)
	switch msg.String() {
	case " ", "ctrl+@", "ctrl+ ":
		return nil // SPC does nothing; RET toggles
	case "ctrl+n", "down":
		p.cursor = min(p.cursor+1, len(p.shown)-1)
	case "ctrl+p", "up":
		if p.cursor == 0 { // past the first row: back to the filter
			p.focusFilter(m)
			return nil
		}
		p.cursor--
	case "ctrl+v", "pgdown":
		p.cursor = min(p.cursor+page, len(p.shown)-1)
	case "alt+v", "pgup":
		p.cursor = max(p.cursor-page, 0)
	case "alt+<":
		p.cursor = 0
	case "alt+>":
		p.cursor = len(p.shown) - 1
	case "enter":
		if p.cursor < 0 || p.cursor >= len(p.shown) {
			return nil
		}
		e := p.all[p.shown[p.cursor]]
		tracked := m.cfg.Index(e.Unit, e.Scope) >= 0
		if m.updateConfig(func(c *config.Config) {
			if tracked {
				c.Untrack(e.Unit, e.Scope)
			} else {
				c.Track(config.Service{Unit: e.Unit, Scope: e.Scope})
			}
		}) == nil {
			if tracked {
				m.setMsg("Untracked %s", e.Unit)
			} else {
				m.setMsg("Tracking %s (%s) — u in the list sets a health URL", e.Unit, e.Scope)
			}
		}
		p.cursor = min(p.cursor+1, len(p.shown)-1)
	default:
		// Anything else (typing, DEL, …) goes back to editing the filter.
		p.focusFilter(m)
		return p.filterKey(m, msg)
	}
	p.cursor = max(p.cursor, 0)
	return nil
}

func (p *picker) view(m *Model, w, h int) string {
	p.h = h
	var b strings.Builder
	b.WriteString(topBar(w, "Add Services", []string{sDim.Render("Track existing systemd services")},
		sDim.Render(fmt.Sprintf("%d/%d units", len(p.shown), len(p.all)))) + "\n")
	h -= 2
	b.WriteString(gutter + sKey.Render("Filter: ") + p.ti.View() + "\n")
	if p.loading {
		b.WriteString(sDim.Render("  loading units…"))
		return b.String()
	}
	if p.err != "" {
		b.WriteString(sRed.Render(trunc("  "+p.err, w)) + "\n")
		h--
	}
	descW := max(0, w-2-40-1-5-1-18-1-10-1)
	b.WriteString(sHeader.Render(gutter+pad("UNIT", 40)+" "+pad("SCOPE", 5)+" "+pad("STATE", 18)+" "+pad("FILE", 10)+" DESCRIPTION") + "\n")
	listH := max(1, h-2) // filter line and column header
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+listH {
		p.offset = p.cursor - listH + 1
	}
	for i := p.offset; i < len(p.shown) && i < p.offset+listH; i++ {
		e := p.all[p.shown[i]]
		mark := ""
		if m.cfg.Index(e.Unit, e.Scope) >= 0 {
			mark = " " + sGreen.Render("✓")
		}
		state := "-"
		if e.ActiveState != "" {
			state = e.ActiveState + "/" + e.SubState
		}
		sc := "sys"
		if e.Scope == config.User {
			sc = "user"
		}
		here := p.listFocus && i == p.cursor
		unit := markedName(e.Unit, mark, 40, here)
		stateS := pad(state, 18)
		switch e.ActiveState {
		case "active":
			stateS = sGreen.Render(stateS)
		case "failed":
			stateS = sRed.Render(stateS)
		default:
			stateS = sDim.Render(stateS)
		}
		b.WriteString(cursorMark(here) + unit + " " + pad(sc, 5) + " " + stateS + " " + sDim.Render(pad(e.FileState, 10)) + " " + sDim.Render(trunc(e.Description, descW)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
