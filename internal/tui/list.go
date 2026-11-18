package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

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
		{"Follow journal", "enter", "Follow the service's journal live", true, func(m *Model, s *config.Service) tea.Cmd { return m.startLogStream(*s) }},
		{"Status", "i", "Show `systemctl status`", true, func(m *Model, s *config.Service) tea.Cmd {
			return m.showStatic("Status", s.Unit, systemd.Systemctl(s.Scope, "status", "--no-pager", "-l", "-n", "30", "--", s.Unit))
		}},
		{"Start or stop", "s", "Start the service, or stop it if it's running", true, func(m *Model, s *config.Service) tea.Cmd {
			switch m.status[svcKey(*s)].ActiveState {
			case "active", "activating", "reloading":
				return act("stop")(m, s)
			}
			return act("start")(m, s)
		}},
		{"Start", "", "Start the service", true, act("start")},
		{"Stop", "", "Stop the service", true, act("stop")},
		{"Restart", "r", "Restart the service", true, act("restart")},
		{"Reload", "R", "Reload the service's configuration (ExecReload)", true, act("reload")},
		{"Toggle enable at boot", "t", "Enable/disable the service at boot", true, func(m *Model, s *config.Service) tea.Cmd {
			verb := "enable"
			if st := m.status[svcKey(*s)]; st.UnitFileState == "enabled" {
				verb = "disable"
			}
			return act(verb)(m, s)
		}},
		{"Edit service", "e", "Edit the service in the Edit Service form (units it can't represent open in Emacs)", true, func(m *Model, s *config.Service) tea.Cmd { return m.openEditForm(*s) }},
		{"Edit unit file", "", "Edit the unit file in Emacs (vendor units: drop-in override)", true, func(m *Model, s *config.Service) tea.Cmd { return m.edit(s, false, "") }},
		{"Edit override", "E", "Edit a drop-in override.conf in Emacs", true, func(m *Model, s *config.Service) tea.Cmd { return m.edit(s, true, "") }},
		{"Reload unit files", "", "Make systemd re-read unit files from disk (systemctl daemon-reload) for the service's scope (e does it after editing)", true, func(m *Model, s *config.Service) tea.Cmd {
			q, i := systemd.DaemonReloadCmds(s.Scope)
			return m.runSystemctl(q, i, "systemctl daemon-reload ("+string(s.Scope)+")")
		}},
		{"View log file", "L", "Open the service's log file (the Logs row) in Emacs", true, func(m *Model, s *config.Service) tea.Cmd {
			p := m.cfg.LogPath(*s)
			if _, err := os.Stat(p); err != nil {
				m.setErr("No log file at %s yet (serviceman daemon: %s — D turns it on)", p, m.loggerState)
				return nil
			}
			return m.execInteractive(systemd.ViewCmd(m.editor(), p), "view "+p, nil)
		}},
		{"Open URL", "o", "Open the health URL in a browser (xdg-open)", true, func(m *Model, s *config.Service) tea.Cmd {
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
		{"Set URL", "u", "Set the HTTP health-check URL", true, func(m *Model, s *config.Service) tea.Cmd {
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
		{"Toggle logging", "l", "Turn logging on/off for this service", true, func(m *Model, s *config.Service) tea.Cmd {
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
				m.setMsg("Logging for %s: %s", svc.Unit, state)
			}
			return nil
		}},
		{"Track existing services", "a", "Add services: pick existing systemd services to track", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openPicker() }},
		{"Toggle watching", "w", "Watching: restart the service when its binary is replaced", true, func(m *Model, s *config.Service) tea.Cmd { return m.toggleWatch(*s) }},
		{"Edit app config", "C", "Open the app's own config file in Emacs", true, func(m *Model, s *config.Service) tea.Cmd {
			if p, _ := m.appConfig(*s); p != "" {
				return m.editFile(p)
			}
			return m.setConfigPath(*s, true)
		}},
		{"Set config path", "", "Set the app's own config file (shown in details)", true, func(m *Model, s *config.Service) tea.Cmd { return m.setConfigPath(*s, false) }},
		{"Set watching path", "", "Watch another file than ExecStart's binary (e.g. for interpreters)", true, func(m *Model, s *config.Service) tea.Cmd { return m.setWatchPath(*s) }},
		{"Edit env file", "", "Open the unit's EnvironmentFile in Emacs", true, func(m *Model, s *config.Service) tea.Cmd {
			st := m.status[svcKey(*s)]
			if len(st.EnvFiles) == 0 {
				m.setErr("%s has no EnvironmentFile= (M-x \"Set env file\" adds one)", s.Unit)
				return nil
			}
			return m.editFile(st.EnvFiles[0])
		}},
		{"Set env file", "", "Attach an env file (secrets) through a drop-in; default ~/.secrets/<binary>.env", true, func(m *Model, s *config.Service) tea.Cmd { return m.setEnvFile(*s) }},
		{"Remove service", "", "Stop, disable and delete the unit file (binary untouched)", true, func(m *Model, s *config.Service) tea.Cmd {
			svc := *s
			m.yesOrNo(fmt.Sprintf("Remove %s (stop, disable, delete unit + drop-ins)?", svc.Unit),
				func(m *Model) tea.Cmd { return m.removeService(svc) })
			return nil
		}},
		{"New service", "c", "Create a service from a binary in the bin dir or any command (auto-tracked)", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openBinPicker() }},
		{"Untrack", "ctrl+k d", "Stop tracking (the service itself is untouched)", true, func(m *Model, s *config.Service) tea.Cmd {
			svc := *s
			if m.updateConfig(func(c *config.Config) { c.Untrack(svc.Unit, svc.Scope) }) == nil {
				m.setMsg("Untracked %s (log files kept)", svc.Unit)
			}
			return nil
		}},
		{"Move up", "alt+p", "Move service up in the list", true, func(m *Model, s *config.Service) tea.Cmd { return m.move(s, -1) }},
		{"Move down", "alt+n", "Move service down in the list", true, func(m *Model, s *config.Service) tea.Cmd { return m.move(s, 1) }},
		{"Filter", "ctrl+s /", "Filter tracked services (incremental)", false, func(m *Model, _ *config.Service) tea.Cmd { return m.startFilter() }},
		{"Refresh", "g", "Refresh status now", false, func(m *Model, _ *config.Service) tea.Cmd {
			m.setMsg("Refreshed")
			return tea.Batch(m.refreshCmd(), m.healthCmd())
		}},
		{"Toggle daemon", "D", "Turn the serviceman daemon (logging + watching) on/off; twice restarts it", false, func(m *Model, _ *config.Service) tea.Cmd { return m.toggleDaemon() }},
		{"Run command", "alt+x", "Run a command by name", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openPalette() }},
		{"Help", "? ctrl+h", "Show key bindings", false, func(m *Model, _ *config.Service) tea.Cmd { return m.showHelp() }},
		{"Quit", "q", "Quit (also C-x C-c)", false, func(m *Model, _ *config.Service) tea.Cmd { return m.quit() }},
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
	case "alt+up", "alt+<", "home":
		m.cursor = 0
	case "alt+down", "alt+>", "end":
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

// edit opens the unit file (or a drop-in) in Emacs; why, when set, says why
// the Edit Service form wasn't used.
func (m *Model) edit(s *config.Service, override bool, why string) tea.Cmd {
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
	if why != "" {
		note += " (" + why + ")"
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
			m.setMsg("Removed %s (binary and log files kept)", svc.Unit)
		}
		return m.refreshCmd()
	})
}

// toggleDaemon stops the serviceman daemon when it's running, and otherwise
// (re)installs and starts it, so pressing D twice restarts it.
func (m *Model) toggleDaemon() tea.Cmd {
	if m.daemonBusy {
		m.setMsg("The serviceman daemon is still turning %s…", map[bool]string{true: "on", false: "off"}[m.loggerState == "activating"])
		return nil
	}
	m.daemonBusy = true
	switch m.loggerState {
	case "active", "activating", "reloading":
		m.setMsg("Turning the serviceman daemon off…")
		m.loggerState = "deactivating"
		return func() tea.Msg {
			return daemonDoneMsg{actionDoneMsg{desc: "serviceman daemon off (logging and watching stopped; D turns it on)", err: logger.Disable()}}
		}
	default:
		m.setMsg("Turning the serviceman daemon on…")
		m.loggerState = "activating"
		return func() tea.Msg {
			notes, err := logger.Install()
			return daemonDoneMsg{actionDoneMsg{desc: "serviceman daemon on: " + strings.Join(notes, "; "), err: err}}
		}
	}
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
		m.setMsg("Watching off for %s", svc.Unit)
		return nil
	}
	path := m.watchedPath(svc)
	msg := fmt.Sprintf("Watching on for %s: %s", svc.Unit, config.Tilde(path))
	home, _ := os.UserHomeDir()
	switch {
	case path == "":
		msg = fmt.Sprintf("Watching on for %s (ExecStart unknown — is the unit loaded?)", svc.Unit)
	case !strings.HasPrefix(path, home+"/"):
		msg += " — if that's an interpreter, M-x \"Set watching path\""
	}
	if m.loggerState != "active" {
		m.setErr("%s — but the serviceman daemon is %s (D turns it on)", msg, m.loggerState)
	} else {
		m.setMsg("%s", msg)
	}
	return nil
}

func (m *Model) setConfigPath(svc config.Service, thenOpen bool) tea.Cmd {
	cur, _ := m.appConfig(svc)
	if cur == "" { // suggest the convention's directory
		if bin := filepath.Base(m.watchedPath(svc)); bin != "." && bin != "/" {
			cur = config.AppDir(bin) + "/"
		}
	}
	p := newPrompt("App config file for "+svc.Unit+" (empty = ~/.config/<binary>/config.*): ", config.Tilde(cur))
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
	p := newPrompt("Watched file for "+svc.Unit+" (empty = ExecStart's binary): ", config.Tilde(svc.WatchPath))
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
				m.setMsg("%s: watching %s (w toggles watching)", svc.Unit, config.Tilde(v))
			}
		}
		return nil
	}
	m.prompt = p
	return nil
}

// setEnvFile asks for an env file and attaches it to the unit with a
// drop-in (systemd's EnvironmentFile=), suggesting ~/.secrets/<binary>.env.
func (m *Model) setEnvFile(svc config.Service) tea.Cmd {
	cur := ""
	if dropIn := systemd.EnvFileDropInPath(svc.Scope, svc.Unit); fileExists(dropIn) {
		if st := m.status[svcKey(svc)]; len(st.EnvFiles) > 0 {
			cur = st.EnvFiles[len(st.EnvFiles)-1] // drop-ins come after the unit's own
		}
	}
	if cur == "" {
		if bin := filepath.Base(m.watchedPath(svc)); bin != "." && bin != "/" {
			cur = config.DefaultEnvFile(bin)
		}
	}
	p := newPrompt("Env file for "+svc.Unit+" (empty = remove serviceman's): ", config.Tilde(cur))
	p.onSubmit = func(m *Model, v string) tea.Cmd {
		v = config.ExpandEnvFile(v)
		if v != "" && !filepath.IsAbs(strings.TrimPrefix(v, "-")) {
			m.setErr("Use an absolute path (or ~/...)")
			return nil
		}
		q, i := systemd.SetEnvFileCmds(svc.Scope, svc.Unit, v)
		desc := "env file of " + svc.Unit + ": " + config.Tilde(v)
		if v == "" {
			desc = "removed serviceman's env file drop-in of " + svc.Unit
		}
		return m.runSystemctlThen(q, i, desc, func(m *Model) tea.Cmd {
			switch problem := config.EnvFileProblem(v); {
			case v == "":
				m.setMsg("%s — restart it (r) to apply", desc)
			case problem != "":
				m.setErr("%s set, but it's %s — restart (r) once it's fixed", config.Tilde(v), problem)
			default:
				m.setMsg("%s set — restart %s (r) to load it", config.Tilde(v), svc.Unit)
			}
			return m.refreshCmd()
		})
	}
	m.prompt = p
	return nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

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
	add := func(k, d string) { lines = append(lines, gutter+sKey.Render(pad(k, 20))+colGap+d) }
	lines = append(lines, sBold.Render("Navigation"))
	add("C-n / n / ↓", "next service")
	add("C-p / p / ↑", "previous service")
	add("C-v / M-v", "page down / up")
	add("M-↑ / M-↓", "first / last")
	add("C-g / ESC ESC", "cancel / clear filter")
	add("ESC key", "same as M-key (ESC x = M-x)")
	add("C-x C-c", "quit")
	lines = append(lines, "", sBold.Render("Commands (also via M-x)"))
	nameW := 0
	for _, c := range commands {
		nameW = max(nameW, ansi.StringWidth(c.name))
	}
	for _, c := range commands {
		var ks []string
		for _, k := range strings.Fields(c.keys) {
			ks = append(ks, emacsKey(k))
		}
		if len(ks) == 0 {
			ks = []string{"M-x"}
		}
		add(strings.Join(ks, " / "), sDim.Render(pad(c.name, nameW)+colGap)+c.desc)
	}
	lines = append(lines, "", sBold.Render("Log / status viewer"))
	add("C-n C-p C-v M-v", "scroll (also SPC / DEL)")
	add("M-↑ / M-↓", "top / bottom")
	add("C-s / C-r", "isearch forward / backward")
	add("f / w", "toggle follow / line wrap")
	add("RET / q / C-g", "back to the list")
	lines = append(lines, "", sBold.Render("Add Services (a)"))
	add("type", "filter (space-separated terms)")
	add("C-n / RET", "from the filter into the list")
	add("RET", "toggle tracking of the unit")
	add("C-g / ESC ESC", "back to the services")
	lines = append(lines, "", sBold.Render("New Service (c): pick a binary"))
	add("type", "filter the binaries in "+config.Tilde(m.cfg.BinDir))
	add("C-n / C-p", "move from the filter to the binaries and Custom command or path")
	add("RET on a binary", "no service: open the prefilled form; has one: Edit Service")
	add("RET on Custom…", "open the form for that command (empty: a blank form)")
	add("C-g", "back")
	lines = append(lines, "", sBold.Render("New Service / Edit Service (e) form"))
	add("TAB C-n / S-TAB C-p", "next / previous field")
	add("SPC / C-f / C-b", "cycle choice fields")
	add("C-c C-c", "write unit, systemctl daemon-reload, enable/start (edit: restart), track")
	add("C-c C-e", "same, but open the unit in Emacs first")
	lines = append(lines, "", sDim.Render("Config: "+config.Path()), sDim.Render("Log files: "+m.cfg.LogDir),
		sDim.Render("Watching state: "+config.WatchStatePath()))
	p := newPager("Keybindings", "")
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

	keyed := func(name, key string) string { return sDim.Render(name+" (") + sKey.Render(key) + sDim.Render(")") }
	left := []string{keyed("Create service", "c"), keyed("Add services", "a")}
	if m.filter != "" {
		left = append(left, sDim.Render("Filter: ")+sKey.Render(m.filter))
	}
	if m.statusErr != "" {
		left = append(left, sRed.Render(trunc(m.statusErr, w/3)))
	}
	// The Daemon indicator stays on the right; the left side gives way.
	b.WriteString(topBar(w, "", left, m.daemonCell()) + "\n")

	cols := []column{
		{"SCOPE", 5, 3}, {"STATE", 20, 9}, {"BOOT", 9, 5}, {"PID", 7, 1},
		{"RAM", 7, 2}, {"UPTIME", 8, 4}, {"HEALTH", 12, 6},
	}
	unitW := func() int {
		t := 3 // marker (2) + spare (1)
		for _, c := range cols {
			t += len(colGap) + c.width
		}
		return w - t
	}
	count := fmt.Sprintf("UNIT [%d]", len(m.cfg.Services))
	if m.filter != "" {
		count = fmt.Sprintf("UNIT [%d/%d]", len(rows), len(m.cfg.Services))
	}
	// The UNIT column is as wide as the longest name (all services, so it
	// doesn't jump while filtering), capped at maxUnitW; longer names are
	// truncated here and shown in full in the details box.
	const maxUnitW = 32
	need := ansi.StringWidth(count)
	for _, s := range m.cfg.Services {
		n := ansi.StringWidth(s.Unit)
		if m.status[svcKey(s)].NeedReload {
			n += 2 // " ⟳"
		}
		need = max(need, n)
	}
	need = min(need, maxUnitW)
	for unitW() < min(need, 18) && len(cols) > 1 {
		lo := 0
		for i, c := range cols {
			if c.prio < cols[lo].prio {
				lo = i
			}
		}
		cols = append(cols[:lo], cols[lo+1:]...)
	}
	uw := max(1, min(need, unitW()))
	hdr := sHeader.Render(gutter + pad(count, uw))
	for _, c := range cols {
		hdr += sHeader.Render(colGap + pad(c.title, c.width))
	}
	b.WriteString(hdr + "\n")

	detailH := 10
	listH := h - 3 - detailH // title, separator, column header
	if listH < 1 {
		listH, detailH = h-3, 0
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+listH {
		m.offset = m.cursor - listH + 1
	}
	if len(rows) == 0 {
		msg := "  No tracked services. Press a to add existing units, c to create a new one."
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
		cells["PID"], cells["RAM"], cells["UPTIME"] = "-", "-", "-"
		if st.MainPID > 0 {
			cells["PID"] = fmt.Sprint(st.MainPID)
		}
		if st.Memory > 0 {
			cells["RAM"] = humanBytes(st.Memory)
		}
		if st.ActiveState == "active" && !st.Since.IsZero() {
			cells["UPTIME"] = humanDur(time.Since(st.Since))
		}
		cells["HEALTH"] = m.healthCell(s)

		unit := s.Unit
		if st.NeedReload {
			unit += " ⟳"
		}
		line := pad(unit, uw)
		if i == m.cursor {
			line = sHighlight.Render(line)
		}
		for _, c := range cols {
			line += colGap + pad(cells[c.title], c.width)
		}
		b.WriteString(cursorMark(i == m.cursor) + line + "\n")
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
	// label is a row name plus the key that acts on it, e.g. "Logs (L):".
	label := func(name, key string) string {
		const w = 16
		if key == "" {
			return gutter + sDim.Render(pad(name+":", w))
		}
		txt := name + " (" + key + "):"
		return gutter + sDim.Render(name+" (") + sKey.Render(key) + sDim.Render("):"+strings.Repeat(" ", max(1, w-len([]rune(txt)))))
	}

	title := gutter + sBold.Render(s.Unit)
	if st.Description != "" {
		title += sDim.Render(" — " + st.Description)
	}
	if st.NRestarts > 0 {
		title += sYellow.Render(fmt.Sprintf("  restarts: %d", st.NRestarts))
	}

	run := label("Run", "") + orDash(config.Tilde(st.ExecPath))

	unit := label("Unit", "") + orDash(config.Tilde(st.FragmentPath))
	if len(st.DropIns) > 0 {
		var names []string
		for _, d := range st.DropIns {
			names = append(names, filepath.Base(d))
		}
		unit += sDim.Render("  Drop-ins: ") + strings.Join(names, ", ")
	}
	if st.NeedReload {
		unit += sYellow.Render("  ⟳ changed on disk, systemd hasn't re-read it — M-x \"Reload unit files\"")
	}

	var envs []string
	for _, e := range st.EnvFiles {
		txt := config.Tilde(e)
		if p := config.EnvFileProblem(e); p != "" {
			txt += sYellow.Render(" (" + p + ")")
		}
		envs = append(envs, txt)
	}
	env := label("Env", "") + orDash(strings.Join(envs, ", "))
	cfgRow := label("Config", "")
	if p, isDefault := m.appConfig(*s); isDefault {
		cfgRow += config.Tilde(p) + sDim.Render(" (default location)")
	} else {
		cfgRow += orDash(config.Tilde(p))
	}
	url := label("Health URL", "") + orDash(s.URL)
	logs := label("Logs", "L") + config.Tilde(m.cfg.LogPath(*s))

	var out []string
	// Only the rows toggled or opened from the list show their key.
	for _, l := range []string{title, run, unit, env, cfgRow, url,
		label("Watching", "w") + m.watchDetail(*s), label("Logging", "l") + m.loggingDetail(*s), logs} {
		out = append(out, trunc(l, w))
	}
	return sep + strings.Join(out, "\n")
}

// daemonOffNote explains an Inactive watching/logging row.
func (m *Model) daemonOffNote() string {
	return sYellow.Render(" · serviceman daemon is " + orDash(m.loggerState) + " (D turns it on)")
}

// appConfig is the service's own config file: the one set with C, or else
// the convention's ~/.config/<binary>/config.* when it exists (isDefault).
func (m *Model) appConfig(s config.Service) (path string, isDefault bool) {
	if s.ConfigPath != "" {
		return s.ConfigPath, false
	}
	if bin := m.watchedPath(s); bin != "" {
		if p := config.DefaultAppConfig(filepath.Base(bin)); p != "" {
			return p, true
		}
	}
	return "", false
}

// loggingDetail is the Logging row: Off, On, or Inactive (on, but the
// serviceman daemon that writes the log file is off).
func (m *Model) loggingDetail(s config.Service) string {
	switch {
	case s.NoRecord:
		return sDim.Render("○ Off")
	case m.loggerState != "active":
		return sDim.Render("● Inactive") + m.daemonOffNote()
	default:
		return sGreen.Render("● On")
	}
}

// watchCell is the watching indicator: on/off and its last outcome.
func (m *Model) watchCell(s config.Service) string {
	if !s.Watch {
		return sDim.Render("○")
	}
	ws, ok := m.watch[svcKey(s)]
	switch {
	case m.loggerState != "active": // on, but nothing watches while the daemon is off
		return sDim.Render("●")
	case !ok || m.watchedPath(s) == "":
		return sYellow.Render("!")
	case ws.Result == "failed" || ws.Result == "error":
		return sRed.Render("✖")
	default:
		return sGreen.Render("●")
	}
}

// watchDetail is the Watching row: Off, On, or Inactive (on, but the
// serviceman daemon that watches the binary is off). While On, a note
// explains a yellow ! or red ✖.
func (m *Model) watchDetail(s config.Service) string {
	switch {
	case !s.Watch:
		return sDim.Render("○ Off")
	case m.loggerState != "active":
		return sDim.Render("● Inactive") + m.daemonOffNote()
	}
	path := m.watchedPath(s)
	var out string
	switch c := ansi.Strip(m.watchCell(s)); c {
	case "!":
		out = sYellow.Render("! On")
	case "✖":
		out = sRed.Render("✖ On")
	default:
		out = sGreen.Render("● On")
	}
	if s.WatchPath != "" {
		out += sDim.Render(" · watches " + config.Tilde(path) + " instead of the Run binary")
	}
	ws, ok := m.watch[svcKey(s)]
	ago := func(t time.Time) string { return humanDur(time.Since(t)) + " ago" }
	switch {
	case path == "":
		return out + sYellow.Render(" · ExecStart unknown (unit not loaded?)")
	case !ok:
		return out + sYellow.Render(" · not picked up yet (within 5 s)")
	case ws.Result == "failed":
		return out + sRed.Render(" · restart "+ago(ws.RestartedAt)+" failed: "+ws.Detail)
	case ws.Result == "error":
		return out + sRed.Render(" · "+ws.Detail+" ("+ago(ws.ChangedAt)+")")
	}
	return out
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

// humanDur is a duration as its two largest units, spaced and without
// leading zeros: "45s", "12m 30s", "5h 7m", "3d 4h". A zero second unit is
// left out ("2h", "3d").
func humanDur(d time.Duration) string {
	s := int(max(d, 0).Seconds())
	units := []struct {
		n    int
		name string
	}{{s / 86400, "d"}, {s / 3600 % 24, "h"}, {s / 60 % 60, "m"}, {s % 60, "s"}}
	for i, u := range units {
		if u.n == 0 && i < len(units)-1 {
			continue
		}
		out := fmt.Sprintf("%d%s", u.n, u.name)
		if i+1 < len(units) && units[i+1].n > 0 {
			out += fmt.Sprintf(" %d%s", units[i+1].n, units[i+1].name)
		}
		return out
	}
	return "0s"
}
