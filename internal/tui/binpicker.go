package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

// binPicker is the first step of New Service: a filter, the executables
// in the bin dir, then a field for any other command or path.
type binPicker struct {
	ti             textinput.Model // the filter
	custom         textinput.Model // Custom command or path
	focus          binFocus
	all            []binEntry
	shown          []int
	cursor, offset int // row in shown while the list has focus
	err            string
	h              int
	self           string // serviceman's own binary
}

type binFocus int

const (
	focusFilter binFocus = iota
	focusList
	focusCustom
)

type binEntry struct {
	name, path, resolved string
	size                 int64
	mtime                time.Time
}

func (m *Model) openBinPicker() tea.Cmd {
	ti := newTextInput()
	ti.Focus()
	p := &binPicker{ti: ti, custom: newTextInput()}
	if exe, err := os.Executable(); err == nil {
		p.self, _ = filepath.EvalSymlinks(exe)
	}
	p.load(m.cfg.BinDir)
	m.bins, m.mode = p, modeBinPicker
	return m.refreshCmd()
}

func (p *binPicker) load(dir string) {
	p.all, p.err = nil, ""
	des, err := os.ReadDir(dir)
	if err != nil {
		p.err = err.Error()
	}
	for _, de := range des {
		path := filepath.Join(dir, de.Name())
		fi, err := os.Stat(path) // follows symlinks
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = path
		}
		p.all = append(p.all, binEntry{name: de.Name(), path: path, resolved: resolved, size: fi.Size(), mtime: fi.ModTime()})
	}
	sort.Slice(p.all, func(i, j int) bool { return strings.ToLower(p.all[i].name) < strings.ToLower(p.all[j].name) })
	p.refilter()
}

func (p *binPicker) refilter() {
	p.shown = p.shown[:0]
	q := p.ti.Value()
	for i, e := range p.all {
		if matchAll(q, e.name) {
			p.shown = append(p.shown, i)
		}
	}
	p.cursor = max(0, min(p.cursor, len(p.shown)-1))
	if p.focus == focusList && len(p.shown) == 0 {
		p.setFocus(focusFilter)
	}
}

func (p *binPicker) setFocus(f binFocus) {
	p.focus = f
	p.ti.Blur()
	p.custom.Blur()
	switch f {
	case focusFilter:
		p.ti.Focus()
	case focusCustom:
		p.custom.Focus()
	}
}

// step moves the focus one place: filter → binaries → custom field.
func (p *binPicker) step(d int) {
	switch {
	case p.focus == focusFilter && d > 0:
		if len(p.shown) > 0 {
			p.cursor = 0
			p.setFocus(focusList)
		} else {
			p.setFocus(focusCustom)
		}
	case p.focus == focusCustom && d < 0:
		if len(p.shown) > 0 {
			p.cursor = len(p.shown) - 1
			p.setFocus(focusList)
		} else {
			p.setFocus(focusFilter)
		}
	case p.focus == focusList:
		switch c := p.cursor + d; {
		case c < 0:
			p.setFocus(focusFilter)
		case c >= len(p.shown):
			p.setFocus(focusCustom)
		default:
			p.cursor = c
		}
	}
}

// watchedPath is the file watching follows for s ("" = unknown yet).
func (m *Model) watchedPath(s config.Service) string {
	if s.WatchPath != "" {
		return config.ExpandTilde(s.WatchPath)
	}
	return m.status[svcKey(s)].ExecPath
}

func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// binLink finds the service that runs e: a tracked service whose binary is
// e, else a user unit file named after it that exists but isn't tracked.
func (m *Model) binLink(e binEntry) (svc config.Service, tracked, onDisk bool) {
	for _, s := range m.cfg.Services {
		if p := m.watchedPath(s); p != "" && sameFile(p, e.path) {
			return s, true, true
		}
	}
	unit := config.NormalizeUnit(e.name)
	if config.ValidUnitName(unit) {
		if _, err := os.Stat(filepath.Join(systemd.UnitDir(config.User), unit)); err == nil {
			return config.Service{Unit: unit, Scope: config.User}, false, true
		}
	}
	return config.Service{}, false, false
}

func (p *binPicker) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	page := max(1, p.h-9)
	k := msg.String()
	switch k {
	case "ctrl+g", "esc":
		m.bins, m.mode = nil, modeList
		m.setMsg("")
		return m.refreshCmd()
	case "ctrl+n", "down":
		p.step(1)
		return nil
	case "ctrl+p", "up":
		p.step(-1)
		return nil
	case "ctrl+v", "pgdown", "alt+v", "pgup", "alt+up", "alt+down", "alt+<", "alt+>":
		if len(p.shown) == 0 {
			return nil
		}
		if p.focus != focusList {
			p.setFocus(focusList)
		}
		switch k {
		case "ctrl+v", "pgdown":
			p.cursor = min(p.cursor+page, len(p.shown)-1)
		case "alt+v", "pgup":
			p.cursor = max(p.cursor-page, 0)
		case "alt+up", "alt+<":
			p.cursor = 0
		default:
			p.cursor = len(p.shown) - 1
		}
		return nil
	case "enter":
		switch p.focus {
		case focusFilter:
			p.step(1)
			return nil
		case focusCustom:
			return p.usePath(m)
		}
		return p.choose(m, p.all[p.shown[p.cursor]])
	}
	var cmd tea.Cmd
	if p.focus == focusCustom {
		p.custom, cmd = p.custom.Update(msg)
		return cmd
	}
	// Typing on a binary goes back to the filter.
	p.setFocus(focusFilter)
	before := p.ti.Value()
	p.ti, cmd = p.ti.Update(msg)
	if p.ti.Value() != before {
		p.cursor = 0
		p.refilter()
	}
	return cmd
}

// usePath opens the form for the custom command or path (a blank form when
// it's empty).
func (p *binPicker) usePath(m *Model) tea.Cmd {
	v := strings.TrimSpace(p.custom.Value())
	if v == "" {
		return m.openForm(nil)
	}
	if f := strings.Fields(v); strings.HasPrefix(f[0], "~") {
		v = config.ExpandTilde(f[0]) + strings.TrimPrefix(v, f[0])
	}
	name := filepath.Base(strings.Fields(v)[0])
	return m.openForm(&formPrefill{name: name, exec: v})
}

func (p *binPicker) choose(m *Model, e binEntry) tea.Cmd {
	if p.self != "" && e.resolved == p.self {
		m.setErr("That's serviceman itself — the serviceman daemon restarts itself on reinstall (D turns it on)")
		return nil
	}
	svc, tracked, onDisk := m.binLink(e)
	switch {
	case tracked:
		return m.openEditForm(svc)
	case onDisk:
		m.yesOrNo(fmt.Sprintf("%s already exists in %s. Track it with watching on?", svc.Unit, config.Tilde(systemd.UnitDir(config.User))),
			func(m *Model) tea.Cmd {
				svc.Watch = true
				if m.updateConfig(func(c *config.Config) {
					if i := c.Index(svc.Unit, svc.Scope); i >= 0 {
						c.Services[i].Watch = true
					} else {
						c.Track(svc)
					}
				}) == nil {
					m.setMsg("Tracking %s with watching on", svc.Unit)
				}
				return m.refreshCmd()
			})
		return nil
	}
	return m.openForm(&formPrefill{name: e.name, exec: e.path, watch: true})
}

func (p *binPicker) view(m *Model, w, h int) string {
	p.h = h
	var b strings.Builder
	b.WriteString(topBar(w, "New Service", []string{sDim.Render("Binaries in " + config.Tilde(m.cfg.BinDir))},
		sDim.Render(fmt.Sprintf("%d/%d executables", len(p.shown), len(p.all)))) + "\n")

	b.WriteString(gutter + sKey.Render("Filter: ") + p.ti.View() + "\n")
	// Below the list: blank line, then the custom field's label and input.
	listH := h - 7 // top bar (2), filter, column header, custom field (3)
	if p.err != "" {
		b.WriteString(sRed.Render(trunc("  "+p.err, w)) + "\n")
		listH--
	}
	nameW := 24
	svcW := max(0, w-len(gutter)-nameW-1-7-1-12-1)
	b.WriteString(sHeader.Render(gutter+pad("BINARY", nameW)+" "+pad("SIZE", 7)+" "+pad("MODIFIED", 12)+" SERVICE") + "\n")
	listH = max(1, listH)
	switch {
	case len(p.all) == 0 && p.err == "":
		b.WriteString(gutter + sDim.Render("No executables here. Deploy one (e.g. go build -o "+config.Tilde(m.cfg.BinDir)+"/myapp); this list updates by itself.") + "\n")
	case len(p.shown) == 0:
		b.WriteString(gutter + sDim.Render("No binaries match the filter.") + "\n")
	}
	sel := -1
	if p.focus == focusList {
		sel = p.cursor
	}
	if sel >= 0 && sel < p.offset {
		p.offset = sel
	}
	if sel >= p.offset+listH {
		p.offset = sel - listH + 1
	}
	p.offset = max(0, min(p.offset, len(p.shown)-listH))
	for i := p.offset; i < len(p.shown) && i < p.offset+listH; i++ {
		e := p.all[p.shown[i]]
		svc, tracked, onDisk := m.binLink(e)
		mark, svcCell := "", sDim.Render("-")
		switch {
		case p.self != "" && e.resolved == p.self:
			svcCell = sDim.Render("serviceman itself (serviceman daemon self-restarts on reinstall)")
		case tracked:
			mark = " " + sGreen.Render("✓")
			st, known := m.status[svcKey(svc)]
			svcCell = svc.Unit + " " + stateCell(st, known)
			if svc.Watch {
				svcCell += sDim.Render(" · watching")
			}
		case onDisk:
			svcCell = sYellow.Render(svc.Unit + " exists, not tracked")
		}
		name := markedName(e.name, mark, nameW, i == sel)
		b.WriteString(cursorMark(i == sel) + name + " " + sDim.Render(pad(humanBytes(uint64(e.size)), 7)) + " " +
			sDim.Render(pad(e.mtime.Format("Jan 02 15:04"), 12)) + " " + trunc(svcCell, svcW) + "\n")
	}

	custom := p.focus == focusCustom
	label := sBold.Render("Custom command or path")
	if custom {
		label = sHighlight.Render("Custom command or path")
	}
	if tw := max(10, min(80, w-6)); p.custom.Width != tw {
		p.custom.Width = tw
	}
	b.WriteString("\n" + cursorMark(custom) + label + "\n")
	b.WriteString(gutter + inputLead(custom) + p.custom.View())
	return b.String()
}
