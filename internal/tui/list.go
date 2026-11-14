package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"svcman/internal/config"
	"svcman/internal/logger"
	"svcman/internal/systemd"
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
		{"new-service", "+ c", "Create a new service unit (auto-tracked)", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openForm() }},
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
	lines = append(lines, sTitle.Render("svcman — key bindings"), "", sBold.Render("Navigation"))
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
	lines = append(lines, "", sBold.Render("New service form"))
	add("TAB C-n / S-TAB C-p", "next / previous field")
	add("SPC / C-f / C-b", "cycle choice fields")
	add("C-c C-c", "write unit, daemon-reload, enable/start, track")
	add("C-c C-e", "same, but open the unit in Emacs first")
	lines = append(lines, "", sDim.Render("Config: "+config.Path()), sDim.Render("Recorded logs: "+m.cfg.LogDir))
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

	header := sTitle.Render(" svcman") + sDim.Render(fmt.Sprintf("  %d tracked", len(m.cfg.Services)))
	if m.statusErr != "" {
		header += "  " + sRed.Render(trunc(m.statusErr, w/2))
	}
	b.WriteString(header + "\n")

	cols := []column{
		{"SCOPE", 6, 3}, {"STATE", 20, 9}, {"BOOT", 9, 5}, {"PID", 8, 1},
		{"MEM", 8, 2}, {"UPTIME", 8, 4}, {"HEALTH", 12, 6}, {"REC", 3, 0},
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

	detailH := 4
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
	var l1, l2, l3 string
	l1 = " " + sBold.Render(s.Unit)
	if st.Description != "" {
		l1 += sDim.Render(" — " + st.Description)
	}
	if st.NRestarts > 0 {
		l1 += sYellow.Render(fmt.Sprintf("  restarts: %d", st.NRestarts))
	}
	l2 = sDim.Render(" unit: ") + orDash(st.FragmentPath)
	if st.NeedReload {
		l2 += sYellow.Render("  ⟳ changed on disk — D to daemon-reload")
	}
	if s.URL != "" {
		l3 = sDim.Render(" url: ") + s.URL + "  "
	}
	if s.NoRecord {
		l3 += sDim.Render(" log: recording off (x)")
	} else {
		l3 += sDim.Render(" log: ") + m.cfg.LogPath(*s)
	}
	return sep + trunc(l1, w) + "\n" + trunc(l2, w) + "\n" + trunc(l3, w)
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
