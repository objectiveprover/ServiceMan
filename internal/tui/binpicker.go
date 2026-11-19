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
	self           string // ServiceMan's own binary
	reloading      bool   // a background rescan (reloadCmd) is running
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
	hints                []string // why it looks like a service: "config", "env file"
	// The service that runs it (binLink), found when the dir is scanned so
	// drawing the list touches no files.
	svc             config.Service
	tracked, onDisk bool
}

// binsLoadedMsg is a background rescan of the bin dir (reloadCmd).
type binsLoadedMsg struct {
	p       *binPicker
	entries []binEntry
	err     string
}

// serviceHints are the signs, by the service conventions, that a binary is
// meant to run as a service: ~/.config/<binary>/config.* and
// ~/.secrets/<binary>.env. Nothing is run to find out.
func serviceHints(name string) []string {
	var h []string
	if config.DefaultAppConfig(name) != "" {
		h = append(h, "config")
	}
	if _, err := os.Stat(config.DefaultEnvFile(name)); err == nil {
		h = append(h, "env file")
	}
	return h
}

func (m *Model) openBinPicker() tea.Cmd {
	ti := newTextInput()
	ti.Focus()
	p := &binPicker{ti: ti, custom: newTextInput()}
	if exe, err := os.Executable(); err == nil {
		p.self, _ = filepath.EvalSymlinks(exe)
	}
	p.load(m)
	m.bins, m.mode = p, modeBinPicker
	return m.refreshCmd()
}

// load lists the bin dir now; the picker uses it when it opens.
func (p *binPicker) load(m *Model) {
	p.apply(scanBins(m.cfg.BinDir, m.cfg.Services, m.status, p.self))
}

// reloadCmd rescans the bin dir in the background (to pick up newly deployed
// binaries and service changes); binsLoadedMsg brings the result. Nothing is
// started while the previous rescan is still running.
func (p *binPicker) reloadCmd(m *Model) tea.Cmd {
	if p.reloading {
		return nil
	}
	p.reloading = true
	// m.status is replaced on every refresh, never changed in place, so the
	// scan can read this one while the UI goes on.
	dir, svcs, status, self := m.cfg.BinDir, append([]config.Service(nil), m.cfg.Services...), m.status, p.self
	return func() tea.Msg {
		entries, err := scanBins(dir, svcs, status, self)
		return binsLoadedMsg{p, entries, err}
	}
}

// apply shows a scan's result. The selected binary stays selected when the
// order changes.
func (p *binPicker) apply(entries []binEntry, err string) {
	selected := ""
	if p.focus == focusList && p.cursor < len(p.shown) {
		selected = p.all[p.shown[p.cursor]].path
	}
	p.all, p.err = entries, err
	p.refilter()
	for i, idx := range p.shown {
		if p.all[idx].path == selected {
			p.cursor = i
		}
	}
}

// scanBins lists the executables in dir: binaries that have a service first,
// then the ones that look like services (serviceHints), then the rest, each
// group by name. It only reads files, so it can run off the UI goroutine.
func scanBins(dir string, svcs []config.Service, status map[string]systemd.Status, self string) (entries []binEntry, errMsg string) {
	des, err := os.ReadDir(dir)
	if err != nil {
		errMsg = err.Error()
	}
	link := binLinker(svcs, status)
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
		e := binEntry{name: de.Name(), path: path, resolved: resolved, size: fi.Size(), mtime: fi.ModTime(),
			hints: serviceHints(de.Name())}
		e.svc, e.tracked, e.onDisk = link(e)
		entries = append(entries, e)
	}
	rank := func(e binEntry) int {
		switch {
		case self != "" && e.resolved == self:
			return 2
		case e.onDisk:
			return 0
		case len(e.hints) > 0:
			return 1
		}
		return 2
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra < rb
		}
		return strings.ToLower(a.name) < strings.ToLower(b.name)
	})
	return entries, errMsg
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

// binLink finds the service that runs e: a tracked service whose binary is
// e (the same path, or the same file behind symlinks), else a user unit file
// named after it that exists but isn't tracked.
func (m *Model) binLink(e binEntry) (svc config.Service, tracked, onDisk bool) {
	return binLinker(m.cfg.Services, m.status)(e)
}

// binLinker is binLink for many binaries: each service's path is resolved
// once, not once per binary.
func binLinker(svcs []config.Service, status map[string]systemd.Status) func(binEntry) (config.Service, bool, bool) {
	byPath := map[string]int{}     // a service's path -> the first service with it
	byResolved := map[string]int{} // the same, with symlinks resolved
	for i, s := range svcs {
		p := watchedPathIn(status, s)
		if p == "" {
			continue
		}
		if _, ok := byPath[p]; !ok {
			byPath[p] = i
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			if _, ok := byResolved[r]; !ok {
				byResolved[r] = i
			}
		}
	}
	unitDir := systemd.UserUnitDir()
	return func(e binEntry) (config.Service, bool, bool) {
		i, ok := byPath[e.path]
		if j, ok2 := byResolved[e.resolved]; ok2 && (!ok || j < i) {
			i, ok = j, true
		}
		if ok {
			return svcs[i], true, true
		}
		unit := systemd.NormalizeUnit(e.name)
		if systemd.ValidUnitName(unit) {
			if _, err := os.Stat(filepath.Join(unitDir, unit)); err == nil {
				return config.Service{Unit: unit, Scope: config.User}, false, true
			}
		}
		return config.Service{}, false, false
	}
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
	case "ctrl+d":
		// On a binary, delete its unit; in the text fields it deletes a character.
		if p.focus == focusList {
			p.remove(m, p.all[p.shown[p.cursor]])
			return nil
		}
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
		m.setErr("That's ServiceMan itself — the ServiceMan daemon restarts itself on reinstall (D turns it on)")
		return nil
	}
	svc, tracked, onDisk := m.binLink(e)
	switch {
	case tracked:
		return m.openEditForm(svc)
	case onDisk:
		m.yesOrNo(fmt.Sprintf("%s already exists in %s. Track it with watching on?", svc.Unit, config.Tilde(systemd.UserUnitDir())),
			func(m *Model) tea.Cmd {
				svc.Watch = true
				if m.updateConfig(func(c *config.Config) {
					if t := c.Find(svc.Unit, svc.Scope); t != nil {
						t.Watch = true
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

// remove deletes the unit that runs e, tracked or not, after a yes or no.
// The rescan after the removal updates the list.
func (p *binPicker) remove(m *Model, e binEntry) {
	if p.self != "" && e.resolved == p.self {
		m.setErr("That's ServiceMan itself — D turns the ServiceMan daemon off")
		return
	}
	svc, _, onDisk := m.binLink(e)
	if !onDisk {
		m.setErr("%s has no systemd unit", e.name)
		return
	}
	m.confirmRemove(svc)
}

func (p *binPicker) view(m *Model, w, h int) string {
	p.h = h
	var b strings.Builder
	b.WriteString(topBar(w, "New Service", []string{sDim.Render("Binaries in " + config.Tilde(m.cfg.BinDir))},
		sDim.Render(fmt.Sprintf("%d/%d binaries", len(p.shown), len(p.all)))) + "\n")

	b.WriteString(gutter + sKey.Render("Filter: ") + p.ti.View() + "\n")
	// Below the list: blank line, then the custom text field.
	listH := h - 7 // top bar (2), filter, column header, blank + custom field (3)
	if p.err != "" {
		b.WriteString(sRed.Render(trunc("  "+p.err, w)) + "\n")
		listH--
	}
	nameW := 24
	g := len(colGap)
	svcW := max(0, w-len(gutter)-nameW-g-7-g-12-g-1)
	b.WriteString(sHeader.Render(gutter+pad("BINARY", nameW)+colGap+pad("SIZE", 7)+colGap+pad("MODIFIED", 12)+colGap+"SERVICE") + "\n")
	listH = max(1, listH)
	switch {
	case len(p.all) == 0 && p.err == "":
		b.WriteString(gutter + sDim.Render("No binaries here. Deploy one (e.g. go build -o "+config.Tilde(m.cfg.BinDir)+"/myapp); this list updates by itself.") + "\n")
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
		svc, tracked, onDisk := e.svc, e.tracked, e.onDisk
		mark, svcCell := "", sDim.Render("-")
		switch {
		case p.self != "" && e.resolved == p.self:
			svcCell = sDim.Render("ServiceMan itself (ServiceMan daemon self-restarts on reinstall)")
		case tracked:
			mark = " " + sGreen.Render("✓")
			st, known := m.status[svc.Key()]
			svcCell = svc.Unit + " " + stateCell(st, known)
			if st.QuickExit() == systemd.CleanQuickExit {
				svcCell += sYellow.Render(" · probably not a service")
			}
			if svc.Watch {
				svcCell += sDim.Render(" · watching")
			}
		case onDisk:
			svcCell = sYellow.Render(svc.Unit + " exists, not tracked")
		case len(e.hints) > 0:
			svcCell = sGreen.Render("likely a service") + sDim.Render(" (has "+strings.Join(e.hints, " and ")+")")
		}
		name := markedName(e.name, mark, nameW, i == sel)
		b.WriteString(cursorMark(i == sel) + name + colGap + sDim.Render(pad(humanBytes(uint64(e.size)), 7)) + colGap +
			sDim.Render(pad(e.mtime.Format("Jan 02 15:04"), 12)) + colGap + trunc(svcCell, svcW) + "\n")
	}

	custom := p.focus == focusCustom
	label := sBold.Render("Custom command or path")
	if custom {
		label = sHighlight.Render("Custom command or path")
	}
	if tw := inputTextWidth(w); p.custom.Width != tw {
		p.custom.Width = tw
	}
	b.WriteString("\n" + strings.Join(textField(cursorMark(custom), label, "", p.custom.View(), custom, w)[:2], "\n"))
	return b.String()
}
