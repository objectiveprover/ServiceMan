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

	"svcman/internal/config"
	"svcman/internal/systemd"
)

// binPicker lists the executables in the bin dir and turns them into
// (auto-restarting) user services.
type binPicker struct {
	ti             textinput.Model
	all            []binEntry
	shown          []int
	cursor, offset int
	err            string
	h              int
	self           string // svcman's own binary
}

type binEntry struct {
	name, path, resolved string
	size                 int64
	mtime                time.Time
}

func (m *Model) openBinPicker() tea.Cmd {
	ti := newTextInput()
	ti.Placeholder = "type to filter"
	ti.Focus()
	p := &binPicker{ti: ti}
	if exe, err := os.Executable(); err == nil {
		p.self, _ = filepath.EvalSymlinks(exe)
	}
	p.load(m.cfg.BinDir)
	m.bins, m.mode = p, modeBinPicker
	m.setMsg("RET: create a service (or remove an existing one) · C-n/C-p move · C-g back")
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
	p.cursor = min(p.cursor, max(0, len(p.shown)-1))
}

// watchedPath is the file auto-restart follows for s ("" = unknown yet).
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
	page := max(1, p.h-3)
	switch msg.String() {
	case "ctrl+g", "esc":
		m.bins, m.mode = nil, modeList
		m.setMsg("")
		return m.refreshCmd()
	case "ctrl+n", "down":
		p.cursor = min(p.cursor+1, len(p.shown)-1)
	case "ctrl+p", "up":
		p.cursor = max(p.cursor-1, 0)
	case "ctrl+v", "pgdown":
		p.cursor = min(p.cursor+page, len(p.shown)-1)
	case "alt+v", "pgup":
		p.cursor = max(p.cursor-page, 0)
	case "alt+<":
		p.cursor = 0
	case "alt+>":
		p.cursor = len(p.shown) - 1
	case "enter", "ctrl+@", "ctrl+ ":
		if p.cursor < 0 || p.cursor >= len(p.shown) {
			return nil
		}
		return p.choose(m, p.all[p.shown[p.cursor]])
	default:
		before := p.ti.Value()
		var cmd tea.Cmd
		p.ti, cmd = p.ti.Update(msg)
		if p.ti.Value() != before {
			p.cursor = 0
			p.refilter()
		}
		return cmd
	}
	p.cursor = max(p.cursor, 0)
	return nil
}

func (p *binPicker) choose(m *Model, e binEntry) tea.Cmd {
	if p.self != "" && e.resolved == p.self {
		m.setErr("That's svcman itself — its daemon restarts on reinstall by itself (L installs it)")
		return nil
	}
	svc, tracked, onDisk := m.binLink(e)
	switch {
	case tracked:
		m.yesOrNo(fmt.Sprintf("Remove %s (stop, disable, delete unit + drop-ins; binary kept)?", svc.Unit),
			func(m *Model) tea.Cmd { return m.removeService(svc) })
		return nil
	case onDisk:
		m.yesOrNo(fmt.Sprintf("%s already exists in %s. Track it with auto-restart?", svc.Unit, config.Tilde(systemd.UnitDir(config.User))),
			func(m *Model) tea.Cmd {
				svc.Watch = true
				if m.updateConfig(func(c *config.Config) {
					if i := c.Index(svc.Unit, svc.Scope); i >= 0 {
						c.Services[i].Watch = true
					} else {
						c.Track(svc)
					}
				}) == nil {
					m.setMsg("Tracking %s with auto-restart", svc.Unit)
				}
				return m.refreshCmd()
			})
		return nil
	}
	home, _ := os.UserHomeDir()
	return m.openForm(&formPrefill{name: e.name, exec: e.path, wd: home, scope: config.User, watch: true})
}

func (p *binPicker) view(m *Model, w, h int) string {
	p.h = h
	var b strings.Builder
	b.WriteString(sTitle.Render(" Binaries in "+config.Tilde(m.cfg.BinDir)) + sDim.Render(fmt.Sprintf("  %d/%d executables", len(p.shown), len(p.all))) + "\n")
	b.WriteString(sKey.Render(" Filter: ") + p.ti.View() + "\n")
	if p.err != "" {
		b.WriteString(sRed.Render(trunc("  "+p.err, w)) + "\n")
		h--
	}
	nameW := 24
	svcW := max(0, w-4-nameW-1-7-1-12-1)
	b.WriteString(sHeader.Render("    "+pad("BINARY", nameW)+" "+pad("SIZE", 7)+" "+pad("MODIFIED", 12)+" SERVICE") + "\n")
	if len(p.all) == 0 && p.err == "" {
		b.WriteString(sDim.Render("  No executables here. Deploy one (e.g. go build -o " + config.Tilde(m.cfg.BinDir) + "/myapp); this list updates by itself."))
		return b.String()
	}
	listH := max(1, h-3)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+listH {
		p.offset = p.cursor - listH + 1
	}
	for i := p.offset; i < len(p.shown) && i < p.offset+listH; i++ {
		e := p.all[p.shown[i]]
		svc, tracked, onDisk := m.binLink(e)
		mark, svcCell := "  ", sDim.Render("-")
		switch {
		case p.self != "" && e.resolved == p.self:
			svcCell = sDim.Render("svcman itself (daemon self-restarts on reinstall)")
		case tracked:
			mark = sGreen.Render("✓ ")
			st, known := m.status[svcKey(svc)]
			svcCell = svc.Unit + " " + stateCell(st, known)
			if svc.Watch {
				svcCell += sDim.Render(" · auto-restart")
			}
		case onDisk:
			svcCell = sYellow.Render(svc.Unit + " exists, not tracked")
		}
		name := pad(e.name, nameW)
		if i == p.cursor {
			name = sSelected.Render(name)
		}
		cur := "  "
		if i == p.cursor {
			cur = sKey.Render("▸ ")
		}
		b.WriteString(cur + mark + name + " " + sDim.Render(pad(humanBytes(uint64(e.size)), 7)) + " " +
			sDim.Render(pad(e.mtime.Format("Jan 02 15:04"), 12)) + " " + trunc(svcCell, svcW) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
