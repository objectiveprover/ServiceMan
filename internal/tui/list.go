package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/logger"
	"serviceman/internal/systemd"
)

// command is a named action, reachable by key and by M-x.
type command struct {
	name    string
	keys    string // space-separated bubbletea key names
	desc    string
	needSvc bool
	run     func(m *Model, s *config.Service) tea.Cmd
}

var commands []command

func init() {
	act := func(verb string) func(m *Model, s *config.Service) tea.Cmd {
		return func(m *Model, s *config.Service) tea.Cmd {
			q, i := systemd.ActionCmds(s.Scope, verb, s.Unit)
			return m.runSystemctl(q, i, verb+" "+s.Unit)
		}
	}
	commands = []command{
		{"logs", "enter l", "Follow the service's journal live", true, func(m *Model, s *config.Service) tea.Cmd { return m.startLogStream(*s) }},
		{"status", "i", "Show `systemctl status`", true, func(m *Model, s *config.Service) tea.Cmd {
			return m.showStatic("Status "+s.Unit, systemd.Systemctl(s.Scope, "status", "--no-pager", "-l", "-n", "30", "--", s.Unit))
		}},
		{"start", "s", "Start the service", true, act("start")},
		{"stop", "S", "Stop the service", true, act("stop")},
		{"restart", "r", "Restart the service", true, act("restart")},
		{"reload", "R", "Reload the service's configuration (ExecReload)", true, act("reload")},
		{"toggle-enable", "t", "Enable/disable the service at boot", true, func(m *Model, s *config.Service) tea.Cmd {
			verb := "enable"
			if st := m.status[svcKey(*s)]; st.UnitFileState == "enabled" {
				verb = "disable"
			}
			return act(verb)(m, s)
		}},
		{"edit-unit", "e", "Edit the unit file in Emacs (vendor units: drop-in override)", true, func(m *Model, s *config.Service) tea.Cmd { return m.edit(s, false) }},
		{"edit-override", "E", "Edit a drop-in override.conf in Emacs", true, func(m *Model, s *config.Service) tea.Cmd { return m.edit(s, true) }},
		{"daemon-reload", "D", "Reload systemd unit files for the service's scope", true, func(m *Model, s *config.Service) tea.Cmd {
			q, i := systemd.DaemonReloadCmds(s.Scope)
			return m.runSystemctl(q, i, "daemon-reload ("+string(s.Scope)+")")
		}},
		{"view-recorded-log", "v", "Open the recorded log file in Emacs", true, func(m *Model, s *config.Service) tea.Cmd {
			p := m.cfg.LogPath(*s)
			if _, err := os.Stat(p); err != nil {
				m.setErr("No recorded log at %s yet (logger: %s — L installs it)", p, m.loggerState)
				return nil
			}
			return m.execInteractive(systemd.ViewCmd(m.editor(), p), "view "+p, nil)
		}},
		{"open-url", "o", "Open the health URL in a browser (xdg-open)", true, func(m *Model, s *config.Service) tea.Cmd {
			if s.URL == "" {
				m.setErr("No URL set for %s (u to set one)", s.Unit)
				return nil
			}
			if err := exec.Command("xdg-open", s.URL).Start(); err != nil {
				m.setErr("xdg-open: %v — URL: %s", err, s.URL)
			} else {
				m.setMsg("Opened %s", s.URL)
			}
			return nil
		}},
		{"set-url", "u", "Set the HTTP health-check URL", true, func(m *Model, s *config.Service) tea.Cmd {
			svc := *s
			p := newPrompt("Health URL for "+svc.Unit+" (empty = none): ", svc.URL)
			p.onSubmit = func(m *Model, v string) tea.Cmd {
				v = strings.TrimSpace(v)
				if v != "" && !strings.Contains(v, "://") {
					v = "http://" + v
				}
				if m.updateConfig(func(c *config.Config) {
					if i := c.Index(svc.Unit, svc.Scope); i >= 0 {
						c.Services[i].URL = v
					}
				}) == nil {
					m.setMsg("URL for %s set to %q", svc.Unit, v)
				}
				return m.healthCmd()
			}
			m.prompt = p
			return nil
		}},
		{"toggle-recording", "x", "Turn log recording on/off for this service", true, func(m *Model, s *config.Service) tea.Cmd {
			svc := *s
			if m.updateConfig(func(c *config.Config) {
				if i := c.Index(svc.Unit, svc.Scope); i >= 0 {
					c.Services[i].NoRecord = !c.Services[i].NoRecord
				}
			}) == nil {
				state := "on"
				if !svc.NoRecord {
					state = "off"
				}
				m.setMsg("Log recording for %s: %s", svc.Unit, state)
			}
			return nil
		}},
		{"track-existing", "a", "Pick existing systemd services to track", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openPicker() }},
		{"bin-services", "b", "Turn binaries in the bin dir into auto-restarting services", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openBinPicker() }},
		{"toggle-watch", "w", "Auto-restart the service when its binary is replaced", true, func(m *Model, s *config.Service) tea.Cmd { return m.toggleWatch(*s) }},
		{"edit-app-config", "C", "Open the app's own config file in Emacs", true, func(m *Model, s *config.Service) tea.Cmd {
			if s.ConfigPath == "" {
				return m.setConfigPath(*s, true)
			}
			return m.editFile(s.ConfigPath)
		}},
		{"set-config-path", "", "Set the app's own config file (shown in details)", true, func(m *Model, s *config.Service) tea.Cmd { return m.setConfigPath(*s, false) }},
		{"set-watch-path", "", "Watch another file than ExecStart's binary (e.g. for interpreters)", true, func(m *Model, s *config.Service) tea.Cmd { return m.setWatchPath(*s) }},
		{"edit-env-file", "", "Open the unit's EnvironmentFile in Emacs", true, func(m *Model, s *config.Service) tea.Cmd {
			st := m.status[svcKey(*s)]
			if len(st.EnvFiles) == 0 {
				m.setErr("%s has no EnvironmentFile= (e to add one to the unit)", s.Unit)
				return nil
			}
			return m.editFile(st.EnvFiles[0])
		}},
		{"remove-service", "", "Stop, disable and delete the unit file (binary untouched)", true, func(m *Model, s *config.Service) tea.Cmd {
			svc := *s
			m.yesOrNo(fmt.Sprintf("Remove %s (stop, disable, delete unit + drop-ins)?", svc.Unit),
				func(m *Model) tea.Cmd { return m.removeService(svc) })
			return nil
		}},
		{"new-service", "+ c", "Create a new service unit (auto-tracked)", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openForm(nil) }},
		{"untrack", "ctrl+k d", "Stop tracking (the service itself is untouched)", true, func(m *Model, s *config.Service) tea.Cmd {
			svc := *s
			if m.updateConfig(func(c *config.Config) { c.Untrack(svc.Unit, svc.Scope) }) == nil {
				m.setMsg("Untracked %s (recorded logs kept)", svc.Unit)
			}
			return nil
		}},
		{"move-up", "alt+p alt+up", "Move service up in the list", true, func(m *Model, s *config.Service) tea.Cmd { return m.move(s, -1) }},
		{"move-down", "alt+n alt+down", "Move service down in the list", true, func(m *Model, s *config.Service) tea.Cmd { return m.move(s, 1) }},
		{"filter", "ctrl+s /", "Filter tracked services (incremental)", false, func(m *Model, _ *config.Service) tea.Cmd { return m.startFilter() }},
		{"refresh", "g", "Refresh status now", false, func(m *Model, _ *config.Service) tea.Cmd {
			m.setMsg("Refreshed")
			return tea.Batch(m.refreshCmd(), m.healthCmd())
		}},
		{"logger-install", "L", "Install/restart the log-recorder user service", false, func(m *Model, _ *config.Service) tea.Cmd {
			m.setMsg("Installing logger…")
			return func() tea.Msg {
				notes, err := logger.Install()
				return actionDoneMsg{desc: "logger install: " + strings.Join(notes, "; "), err: err}
			}
		}},
		{"execute-command", "alt+x", "Run a command by name", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openPalette() }},
		{"help", "? ctrl+h", "Show key bindings", false, func(m *Model, _ *config.Service) tea.Cmd { return m.showHelp() }},
		{"quit", "q", "Quit (also C-x C-c)", false, func(m *Model, _ *config.Service) tea.Cmd { return m.quit() }},
	}
}

func runCommand(m *Model, c command) tea.Cmd {
	s := m.selected()
	if c.needSvc && s == nil {
		m.setErr("No service selected")
		return nil
	}
	return c.run(m, s)
}

func (m *Model) listKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	n := len(m.rows())
	page := max(1, m.h-8)
	switch k {
	case "ctrl+n", "n", "down":
		m.cursor = min(m.cursor+1, n-1)
	case "ctrl+p", "p", "up":
		m.cursor = max(m.cursor-1, 0)
	case "ctrl+v", "pgdown":
		m.cursor = min(m.cursor+page, n-1)
	case "alt+v", "pgup":
		m.cursor = max(m.cursor-page, 0)
	case "alt+<", "home":
		m.cursor = 0
	case "alt+>", "end":
		m.cursor = n - 1
	case "ctrl+g", "esc":
		if m.filter != "" {
			m.filter = ""
			m.setMsg("Filter cleared")
		} else {
			m.setMsg("Quit")
		}
	default:
		for _, c := range commands {
			for _, ck := range strings.Fields(c.keys) {
				if ck == k {
					return runCommand(m, c)
				}
			}
		}
		m.setErr("%s is undefined (? for help, M-x for commands)", emacsKey(k))
	}
	m.clampCursor()
	return nil
}

func (m *Model) edit(s *config.Service, override bool) tea.Cmd {
	st := m.status[svcKey(*s)]
	path := st.FragmentPath
	note := ""
	switch {
	case st.LoadState == "not-found" || path == "":
		if override {
			path = systemd.OverridePath(s.Scope, s.Unit)
		} else {
			path = systemd.UnitDir(s.Scope) + "/" + s.Unit
			note = " (new file)"
		}
	case override || systemd.IsVendorPath(path):
		if !override {
			note = " (vendor unit → drop-in override)"
		}
		path = systemd.OverridePath(s.Scope, s.Unit)
	}
	desc := "edit " + path + note
	m.setMsg("%s…", desc)
	return m.execInteractive(systemd.EditCmd(s.Scope, m.editor(), path), desc, nil)
}

// removeService stops/disables a unit, deletes its unit file and drop-ins
// and untracks it. Vendor units are refused.
func (m *Model) removeService(svc config.Service) tea.Cmd {
	st := m.status[svcKey(svc)]
	if systemd.IsVendorPath(st.FragmentPath) {
		m.setErr("%s is a vendor unit (%s); stop/disable it or untrack it (C-k) instead", svc.Unit, st.FragmentPath)
		return nil
	}
	q, i := systemd.RemoveUnitCmds(svc.Scope, svc.Unit, st.FragmentPath)
	return m.runSystemctlThen(q, i, "remove "+svc.Unit, func(m *Model) tea.Cmd {
		if m.updateConfig(func(c *config.Config) { c.Untrack(svc.Unit, svc.Scope) }) == nil {
			m.setMsg("Removed %s (binary and recorded logs kept)", svc.Unit)
		}
		return m.refreshCmd()
	})
}

func (m *Model) toggleWatch(svc config.Service) tea.Cmd {
	on := !svc.Watch
	if m.updateConfig(func(c *config.Config) {
		if i := c.Index(svc.Unit, svc.Scope); i >= 0 {
			c.Services[i].Watch = on
		}
	}) != nil {
		return nil
	}
	if !on {
		m.setMsg("Auto-restart off for %s", svc.Unit)
		return nil
	}
	path := m.watchedPath(svc)
	msg := fmt.Sprintf("Auto-restart on for %s: watching %s", svc.Unit, config.Tilde(path))
	home, _ := os.UserHomeDir()
	switch {
	case path == "":
		msg = fmt.Sprintf("Auto-restart on for %s (ExecStart unknown — is the unit loaded?)", svc.Unit)
	case !strings.HasPrefix(path, home+"/"):
		msg += " — if that's an interpreter, M-x set-watch-path"
	}
	if m.loggerState != "active" {
		m.setErr("%s — but the serviceman daemon is %s (L installs it)", msg, m.loggerState)
	} else {
		m.setMsg("%s", msg)
	}
	return nil
}

func (m *Model) setConfigPath(svc config.Service, thenOpen bool) tea.Cmd {
	p := newPrompt("App config file for "+svc.Unit+" (empty = none): ", config.Tilde(svc.ConfigPath))
	p.onSubmit = func(m *Model, v string) tea.Cmd {
		v = config.ExpandTilde(strings.TrimSpace(v))
		if v != "" && !filepath.IsAbs(v) {
			m.setErr("Use an absolute path (or ~/...)")
			return nil
		}
		if m.updateConfig(func(c *config.Config) {
			if i := c.Index(svc.Unit, svc.Scope); i >= 0 {
				c.Services[i].ConfigPath = v
			}
		}) != nil {
			return nil
		}
		if v == "" {
			m.setMsg("Config path for %s cleared", svc.Unit)
			return nil
		}
		m.setMsg("Config path for %s: %s", svc.Unit, config.Tilde(v))
		if thenOpen {
			return m.editFile(v)
		}
		return nil
	}
	m.prompt = p
	return nil
}

func (m *Model) setWatchPath(svc config.Service) tea.Cmd {
	p := newPrompt("Watch file for "+svc.Unit+" (empty = ExecStart's binary): ", config.Tilde(svc.WatchPath))
	p.onSubmit = func(m *Model, v string) tea.Cmd {
		v = config.ExpandTilde(strings.TrimSpace(v))
		if v != "" && !filepath.IsAbs(v) {
			m.setErr("Use an absolute path (or ~/...)")
			return nil
		}
		if m.updateConfig(func(c *config.Config) {
			if i := c.Index(svc.Unit, svc.Scope); i >= 0 {
				c.Services[i].WatchPath = v
			}
		}) == nil {
			if v == "" {
				m.setMsg("%s: watching ExecStart's binary", svc.Unit)
			} else {
				m.setMsg("%s: watching %s (w toggles auto-restart)", svc.Unit, config.Tilde(v))
			}
		}
		return nil
	}
	m.prompt = p
	return nil
}

// editFile opens a non-unit file (no daemon-reload afterwards).
func (m *Model) editFile(path string) tea.Cmd {
	return m.execInteractive(systemd.EditFileCmd(m.editor(), path), "edit "+config.Tilde(path), nil)
}

func (m *Model) move(s *config.Service, d int) tea.Cmd {
	if m.filter != "" {
		m.setErr("Clear the filter (C-g) before reordering")
		return nil
	}
	i := m.cfg.Index(s.Unit, s.Scope)
	j := i + d
	if i < 0 || j < 0 || j >= len(m.cfg.Services) {
		return nil
	}
	svc := *s
	if m.updateConfig(func(c *config.Config) {
		a := c.Index(svc.Unit, svc.Scope)
		b := a + d
		if a >= 0 && b >= 0 && b < len(c.Services) {
			c.Services[a], c.Services[b] = c.Services[b], c.Services[a]
		}
	}) == nil {
		m.cursor = j
	}
	return nil
}

func (m *Model) startFilter() tea.Cmd {
	old := m.filter
	p := newPrompt("Filter: ", m.filter)
	p.onChange = func(m *Model, v string) { m.filter = v; m.cursor = 0 }
	p.onSubmit = func(m *Model, v string) tea.Cmd { m.filter = strings.TrimSpace(v); return nil }
	p.onCancel = func(m *Model) { m.filter = old }
	m.prompt = p
	return nil
}

func (m *Model) showHelp() tea.Cmd {
	var lines []string
	add := func(k, d string) { lines = append(lines, "  "+sKey.Render(pad(k, 22))+d) }
	lines = append(lines, sTitle.Render("serviceman — key bindings"), "", sBold.Render("Navigation"))
	add("C-n / n / ↓", "next service")
	add("C-p / p / ↑", "previous service")
	add("C-v / M-v", "page down / up")
	add("M-< / M->", "first / last")
	add("C-g", "cancel / clear filter")
	add("C-x C-c", "quit")
	lines = append(lines, "", sBold.Render("Commands (also via M-x)"))
	for _, c := range commands {
		var ks []string
		for _, k := range strings.Fields(c.keys) {
			ks = append(ks, emacsKey(k))
		}
		if len(ks) == 0 {
			ks = []string{"M-x"}
		}
		add(strings.Join(ks, " / "), sDim.Render(pad(c.name, 20))+c.desc)
	}
	lines = append(lines, "", sBold.Render("Log / status viewer"))
	add("C-n C-p C-v M-v", "scroll (also SPC / DEL)")
	add("M-< M->", "top / bottom")
	add("C-s / C-r", "isearch forward / backward")
	add("f / w", "toggle follow / line wrap")
	add("q / C-g", "back to the list")
	lines = append(lines, "", sBold.Render("Track-existing picker"))
	add("type", "filter (space-separated terms)")
	add("RET / C-SPC", "toggle tracking of the unit")
	add("C-g", "back")
	lines = append(lines, "", sBold.Render("Bin picker (b)"))
	add("type", "filter binaries in "+config.Tilde(m.cfg.BinDir))
	add("RET", "no service: open the prefilled New service form")
	add("RET", "has a service: remove it (asks yes/no; binary kept)")
	add("C-g", "back")
	lines = append(lines, "", sBold.Render("New service form"))
	add("TAB C-n / S-TAB C-p", "next / previous field")
	add("SPC / C-f / C-b", "cycle choice fields")
	add("C-c C-c", "write unit, daemon-reload, enable/start, track")
	add("C-c C-e", "same, but open the unit in Emacs first")
	lines = append(lines, "", sDim.Render("Config: "+config.Path()), sDim.Render("Recorded logs: "+m.cfg.LogDir),
		sDim.Render("Auto-restart state: "+config.WatchStatePath()))
	p := newPager("Help")
	p.follow = false
	p.setLines(lines)
	m.pager, m.mode = p, modePager
	return nil
}

// ------------------------------------------------------------------ view

type column struct {
	title string
	width int
	prio  int // lower = dropped first when narrow
}

func (m *Model) listView(w, h int) string {
	rows := m.rows()
	var b strings.Builder

	header := sTitle.Render(" serviceman") + sDim.Render(fmt.Sprintf("  %d tracked", len(m.cfg.Services)))
	if m.statusErr != "" {
		header += "  " + sRed.Render(trunc(m.statusErr, w/2))
	}
	b.WriteString(header + "\n")

	cols := []column{
		{"SCOPE", 6, 3}, {"STATE", 20, 9}, {"BOOT", 9, 5}, {"PID", 8, 1},
		{"MEM", 8, 2}, {"UPTIME", 8, 4}, {"HEALTH", 12, 6}, {"W", 1, 7}, {"REC", 3, 0},
	}
	unitW := func() int {
		t := 4
		for _, c := range cols {
			t += c.width + 1
		}
		return w - t
	}
	for unitW() < 18 && len(cols) > 1 {
		lo := 0
		for i, c := range cols {
			if c.prio < cols[lo].prio {
				lo = i
			}
		}
		cols = append(cols[:lo], cols[lo+1:]...)
	}
	uw := min(unitW(), 40)

	hdr := "  " + pad("UNIT", uw) + " "
	for _, c := range cols {
		hdr += pad(c.title, c.width) + " "
	}
	b.WriteString(sHeader.Render(hdr) + "\n")

	detailH := 6
	listH := h - 2 - detailH
	if listH < 1 {
		listH, detailH = h-2, 0
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+listH {
		m.offset = m.cursor - listH + 1
	}
	if len(rows) == 0 {
		msg := "  No tracked services. Press a to track existing units, + to create a new one."
		if m.filter != "" {
			msg = "  No services match the filter (C-g clears it)."
		}
		b.WriteString(sDim.Render(msg) + "\n")
	}
	for i := m.offset; i < len(rows) && i < m.offset+listH; i++ {
		s := rows[i]
		st, known := m.status[svcKey(s)]
		cells := map[string]string{}
		cells["SCOPE"] = map[config.Scope]string{config.System: "sys", config.User: "user"}[s.Scope]
		cells["STATE"] = stateCell(st, known)
		cells["BOOT"] = bootCell(st.UnitFileState)
		cells["PID"], cells["MEM"], cells["UPTIME"] = "-", "-", "-"
		if st.MainPID > 0 {
			cells["PID"] = fmt.Sprint(st.MainPID)
		}
		if st.Memory > 0 {
			cells["MEM"] = humanBytes(st.Memory)
		}
		if st.ActiveState == "active" && !st.Since.IsZero() {
			cells["UPTIME"] = humanDur(time.Since(st.Since))
		}
		cells["HEALTH"] = m.healthCell(s)
		cells["W"] = m.watchCell(s)
		cells["REC"] = sGreen.Render("●")
		if s.NoRecord {
			cells["REC"] = sDim.Render("○")
		}

		unit := s.Unit
		if st.NeedReload {
			unit += " ⟳"
		}
		marker := "  "
		if i == m.cursor {
			marker = sKey.Render("▸ ")
		}
		line := pad(unit, uw)
		if i == m.cursor {
			line = sBold.Render(line)
		}
		line += " "
		for _, c := range cols {
			line += pad(cells[c.title], c.width) + " "
		}
		b.WriteString(marker + line + "\n")
	}
	used := min(len(rows)-m.offset, listH)
	if len(rows) == 0 {
		used = 1
	}
	for i := used; i < listH; i++ {
		b.WriteString("\n")
	}
	if detailH > 0 {
		b.WriteString(m.detailView(w))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) detailView(w int) string {
	s := m.selected()
	sep := sDim.Render(strings.Repeat("─", w)) + "\n"
	if s == nil {
		return sep
	}
	st := m.status[svcKey(*s)]
	label := func(l string) string { return sDim.Render(" " + pad(l+":", 8)) }

	l1 := " " + sBold.Render(s.Unit)
	if st.Description != "" {
		l1 += sDim.Render(" — " + st.Description)
	}
	if st.NRestarts > 0 {
		l1 += sYellow.Render(fmt.Sprintf("  restarts: %d", st.NRestarts))
	}

	l2 := label("unit") + orDash(config.Tilde(st.FragmentPath))
	if len(st.DropIns) > 0 {
		var names []string
		for _, d := range st.DropIns {
			names = append(names, filepath.Base(d))
		}
		l2 += sDim.Render("  drop-ins: ") + strings.Join(names, ", ")
	}
	if st.NeedReload {
		l2 += sYellow.Render("  ⟳ changed on disk — D to daemon-reload")
	}

	var envs []string
	for _, e := range st.EnvFiles {
		envs = append(envs, config.Tilde(e))
	}
	l3 := label("env") + orDash(strings.Join(envs, ", ")) + sDim.Render("   config: ")
	if s.ConfigPath != "" {
		l3 += config.Tilde(s.ConfigPath)
	} else {
		l3 += sDim.Render("- (C to set)")
	}

	l4 := label("watch") + m.watchDetail(*s)

	var l5 string
	if s.URL != "" {
		l5 = label("url") + s.URL + "  "
	}
	if s.NoRecord {
		l5 += sDim.Render(" log: recording off (x)")
	} else {
		l5 += sDim.Render(" log: ") + config.Tilde(m.cfg.LogPath(*s))
	}
	return sep + trunc(l1, w) + "\n" + trunc(l2, w) + "\n" + trunc(l3, w) + "\n" + trunc(l4, w) + "\n" + trunc(l5, w)
}

// watchCell is the W column: auto-restart on/off and its last outcome.
func (m *Model) watchCell(s config.Service) string {
	if !s.Watch {
		return sDim.Render("○")
	}
	ws, ok := m.watch[svcKey(s)]
	switch {
	case !ok || m.loggerState != "active":
		return sYellow.Render("!")
	case ws.Result == "failed" || ws.Result == "error":
		return sRed.Render("✖")
	default:
		return sGreen.Render("●")
	}
}

func (m *Model) watchDetail(s config.Service) string {
	if !s.Watch {
		return sDim.Render("off (w turns on auto-restart)")
	}
	path := m.watchedPath(s)
	out := orDash(config.Tilde(path))
	ws, ok := m.watch[svcKey(s)]
	ago := func(t time.Time) string { return humanDur(time.Since(t)) + " ago" }
	switch {
	case m.loggerState != "active":
		return out + sYellow.Render(" · serviceman daemon is "+m.loggerState+" (L installs it)")
	case path == "":
		return out + sYellow.Render(" · ExecStart unknown (unit not loaded?)")
	case !ok:
		return out + sYellow.Render(" · not watched yet (daemon picks it up within 5 s)")
	}
	switch ws.Result {
	case "":
		return out + sDim.Render(" · watching, no changes yet")
	case "ok":
		if ws.Detail != "" {
			return out + sGreen.Render(" · binary updated "+ago(ws.ChangedAt)+" ✓ ") + sDim.Render(ws.Detail)
		}
		return out + sGreen.Render(" · auto-restarted "+ago(ws.RestartedAt)+" ✓")
	case "skipped":
		return out + sYellow.Render(" · binary updated "+ago(ws.ChangedAt)+", not restarted: "+ws.Detail)
	case "failed":
		return out + sRed.Render(" · ✖ auto-restart "+ago(ws.RestartedAt)+" failed: "+ws.Detail)
	default:
		return out + sRed.Render(" · ✖ "+ws.Detail+" ("+ago(ws.ChangedAt)+")")
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func stateCell(st systemd.Status, known bool) string {
	if !known {
		return sDim.Render("…")
	}
	if st.LoadState == "not-found" {
		return sRed.Render("✖ not-found")
	}
	txt := st.ActiveState + "/" + st.SubState
	switch st.ActiveState {
	case "active":
		return sGreen.Render("● " + txt)
	case "failed":
		return sRed.Render("✖ " + txt)
	case "activating", "deactivating", "reloading":
		return sYellow.Render("◐ " + txt)
	default:
		return sDim.Render("○ " + txt)
	}
}

func bootCell(s string) string {
	switch s {
	case "enabled", "enabled-runtime":
		return sGreen.Render(s)
	case "":
		return sDim.Render("-")
	case "masked":
		return sRed.Render(s)
	default:
		return sDim.Render(s)
	}
}

func (m *Model) healthCell(s config.Service) string {
	if s.URL == "" {
		return sDim.Render("-")
	}
	r, ok := m.health[svcKey(s)]
	if !ok {
		return sDim.Render("…")
	}
	if r.err != nil {
		return sRed.Render("✖ down")
	}
	txt := fmt.Sprintf("%d %dms", r.code, r.dur.Milliseconds())
	switch {
	case r.code < 400:
		return sGreen.Render(txt)
	case r.code < 500:
		return sYellow.Render(txt)
	default:
		return sRed.Render(txt)
	}
}

func humanBytes(n uint64) string {
	f := float64(n)
	for _, u := range []string{"B", "K", "M", "G"} {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%.0f%s", f, u)
			}
			return fmt.Sprintf("%.1f%s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1fT", f)
}

func humanDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
