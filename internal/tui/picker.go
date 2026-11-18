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

// openPicker opens Track Services. It shows the units listed earlier (in the
// background since startup) at once, and lists them again for units added
// since.
func (m *Model) openPicker() tea.Cmd {
	ti := newTextInput()
	ti.Focus()
	m.picker = &picker{ti: ti, loading: true}
	m.mode = modePicker
	if m.units != nil {
		m.picker.loaded(*m.units)
	}
	return m.loadUnitsCmd()
}

// loadUnitsCmd lists every service unit of both scopes in the background;
// pickerLoadedMsg brings them. Listing the system's unit files takes
// systemd a few hundred milliseconds, so the result is kept in m.units.
func (m *Model) loadUnitsCmd() tea.Cmd {
	if m.unitsLoading {
		return nil
	}
	m.unitsLoading = true
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

// loaded shows a unit list. When the list is shown again (newer units),
// the selected unit stays selected.
func (p *picker) loaded(msg pickerLoadedMsg) {
	var sel *systemd.UnitEntry
	if p.listFocus && p.cursor < len(p.shown) {
		e := p.all[p.shown[p.cursor]]
		sel = &e
	}
	p.loading = false
	p.all = msg.entries
	p.err = ""
	if msg.err != nil {
		p.err = msg.err.Error()
	}
	p.refilter()
	if sel != nil {
		for i, idx := range p.shown {
			if e := p.all[idx]; e.Unit == sel.Unit && e.Scope == sel.Scope {
				p.cursor = i
				break
			}
		}
	}
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

func (p *picker) focusFilter() {
	p.listFocus = false
	p.ti.Focus()
}

func (p *picker) focusList() {
	p.listFocus = true
	p.ti.Blur()
}

func isPickerNav(k string) bool {
	switch k {
	case "ctrl+n", "down", "ctrl+p", "up", "ctrl+v", "pgdown", "alt+v", "pgup", "alt+up", "alt+down", "alt+<", "alt+>":
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
		p.focusList()
		// The first C-n/C-p just lands on the current row; paging and
		// first/last (M-up/M-down, or M-< / M->) also move.
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
			p.focusFilter()
			return nil
		}
		p.cursor--
	case "ctrl+v", "pgdown":
		p.cursor = min(p.cursor+page, len(p.shown)-1)
	case "alt+v", "pgup":
		p.cursor = max(p.cursor-page, 0)
	case "alt+up", "alt+<":
		p.cursor = 0
	case "alt+down", "alt+>":
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
		p.focusFilter()
		return p.filterKey(m, msg)
	}
	p.cursor = max(p.cursor, 0)
	return nil
}

func (p *picker) view(m *Model, w, h int) string {
	p.h = h
	var b strings.Builder
	b.WriteString(topBar(w, "Track Services", []string{sDim.Render("Track existing systemd services")},
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
	g := len(colGap)
	descW := max(0, w-len(gutter)-40-g-5-g-18-g-10-g-1)
	b.WriteString(sHeader.Render(gutter+pad("UNIT", 40)+colGap+pad("SCOPE", 5)+colGap+pad("STATE", 18)+colGap+pad("FILE", 10)+colGap+"DESCRIPTION") + "\n")
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
		b.WriteString(cursorMark(here) + unit + colGap + pad(scopeLabel(e.Scope), 5) + colGap + stateS + colGap + sDim.Render(pad(e.FileState, 10)) + colGap + sDesc.Render(trunc(e.Description, descW)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
