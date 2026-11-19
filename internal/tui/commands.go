package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
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
		{"Follow journal", "enter", "Show the service's journal live", true, func(m *Model, s *config.Service) tea.Cmd { return m.startLogStream(*s) }},
		{"Status", "i", "Show the service's systemd status", true, func(m *Model, s *config.Service) tea.Cmd {
			return m.showStatic("Status", s.Unit, systemd.Systemctl(s.Scope, "status", "--no-pager", "-l", "-n", "30", "--", s.Unit))
		}},
		{"Start or stop", "s", "Start the service, or stop it if it's running", true, func(m *Model, s *config.Service) tea.Cmd {
			if systemd.IsRunning(m.status[s.Key()].ActiveState) {
				return act("stop")(m, s)
			}
			return act("start")(m, s)
		}},
		{"Start", "", "Start the service", true, act("start")},
		{"Stop", "", "Stop the service", true, act("stop")},
		{"Restart", "r", "Restart the service", true, act("restart")},
		{"Reload", "R", "Ask the service to reload its settings without restarting (ExecReload)", true, act("reload")},
		{"Toggle enable at boot", "", "Turn starting at boot on or off", true, func(m *Model, s *config.Service) tea.Cmd {
			verb := "enable"
			if st := m.status[s.Key()]; st.UnitFileState == "enabled" {
				verb = "disable"
			}
			return act(verb)(m, s)
		}},
		{"Edit service", "e", "Edit the service in a form (opens Emacs if the form can't show all its settings)", true, func(m *Model, s *config.Service) tea.Cmd { return m.openEditForm(*s) }},
		{"Edit unit file", "", "Edit the unit file in Emacs (for units installed by the system, edits an override file instead)", true, func(m *Model, s *config.Service) tea.Cmd { return m.edit(s, false, "") }},
		{"Edit override", "", "Edit the service's override file in Emacs", true, func(m *Model, s *config.Service) tea.Cmd { return m.edit(s, true, "") }},
		{"Reload unit files", "", "Make systemd read the unit files again after you change them (daemon-reload; e does this for you)", true, func(m *Model, s *config.Service) tea.Cmd {
			q, i := systemd.DaemonReloadCmds(s.Scope)
			return m.runSystemctl(q, i, "systemctl daemon-reload ("+string(s.Scope)+")")
		}},
		{"View log file", "L", "Open the service's log file in Emacs", true, func(m *Model, s *config.Service) tea.Cmd {
			p := m.cfg.LogPath(*s)
			if _, err := os.Stat(p); err != nil {
				m.setErr("No log file at %s yet (ServiceMan daemon: %s — D turns it on)", p, m.daemonState)
				return nil
			}
			return m.editorCmd("view "+p, func(e string) *exec.Cmd { return systemd.ViewCmd(e, p) })
		}},
		{"Open URL", "", "Open the health-check URL in your browser (xdg-open)", true, func(m *Model, s *config.Service) tea.Cmd {
			if s.URL == "" {
				m.setErr("No URL set for %s (M-x \"Set URL\" sets one)", s.Unit)
				return nil
			}
			u := m.healthURL(*s)
			if u == "" {
				m.setErr("%s's URL is auto, and its server has no port yet (it picks one on its first start)", s.Unit)
				return nil
			}
			if err := exec.Command("xdg-open", u).Start(); err != nil {
				m.setErr("xdg-open: %v — URL: %s", err, u)
			} else {
				m.setMsg("Opened %s", u)
			}
			return nil
		}},
		{"Set URL", "", "Set the URL that's checked to see if the service is healthy", true, func(m *Model, s *config.Service) tea.Cmd {
			svc := *s
			p := newPrompt("Health URL for "+svc.Unit+" (empty = none, auto = from serviceman-info): ", svc.URL)
			p.onSubmit = func(m *Model, v string) tea.Cmd {
				v = config.NormalizeURL(strings.TrimSpace(v))
				if m.updateService(svc, func(s *config.Service) { s.URL = v }) == nil {
					m.setMsg("URL for %s set to %q", svc.Unit, v)
				}
				return m.healthCmd()
			}
			m.prompt = p
			return nil
		}},
		{"Toggle logging", "l", "Turn saving this service's logs to a file on or off", true, func(m *Model, s *config.Service) tea.Cmd {
			svc := *s
			if m.updateService(svc, func(s *config.Service) { s.NoRecord = !s.NoRecord }) == nil {
				state := "on"
				if !svc.NoRecord {
					state = "off"
				}
				m.setMsg("Logging for %s: %s", svc.Unit, state)
			}
			return nil
		}},
		{"Track services", "t", "Pick existing systemd services to add to the list", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openPicker() }},
		{"Toggle watching", "w", "Turn on or off restarting the service when its binary is replaced", true, func(m *Model, s *config.Service) tea.Cmd { return m.toggleWatch(*s) }},
		{"Edit app config", "C", "Open the app's own config file in Emacs", true, func(m *Model, s *config.Service) tea.Cmd {
			if m.systemRefused(*s) {
				return nil
			}
			if p, _ := m.appConfig(*s); p != "" {
				return m.editFile(p)
			}
			return m.setConfigPath(*s, true)
		}},
		{"Set config path", "", "Choose the app's own config file", true, func(m *Model, s *config.Service) tea.Cmd {
			if m.systemRefused(*s) {
				return nil
			}
			return m.setConfigPath(*s, false)
		}},
		{"Set watching path", "", "Watch a different file for changes (for example, a script run by an interpreter)", true, func(m *Model, s *config.Service) tea.Cmd { return m.setWatchPath(*s) }},
		{"Edit env file", "", "Open the service's env file in Emacs", true, func(m *Model, s *config.Service) tea.Cmd {
			if m.systemRefused(*s) {
				return nil
			}
			st := m.status[s.Key()]
			if len(st.EnvFiles) == 0 {
				m.setErr("%s has no .env file (M-x \"Set env file\" adds one)", s.Unit)
				return nil
			}
			p := st.EnvFiles[0]
			return m.editorCmd("edit "+config.Tilde(p), func(e string) *exec.Cmd { return systemd.EditEnvFileCmd(e, p) })
		}},
		{"Set env file", "", "Choose the env file with the service's secrets (default ~/.secrets/<binary>.env)", true, func(m *Model, s *config.Service) tea.Cmd { return m.setEnvFile(*s) }},
		{"Remove service", "", "Stop the service, turn off starting at boot, and delete its unit file (keeps the binary)", true, func(m *Model, s *config.Service) tea.Cmd {
			m.confirmRemove(*s)
			return nil
		}},
		{"New service", "c", "Create a service from a binary or any command and add it to the list", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openBinPicker() }},
		{"Untrack", "ctrl+k d", "Remove the service from the list (the service itself is untouched)", true, func(m *Model, s *config.Service) tea.Cmd {
			svc := *s
			if m.updateConfig(func(c *config.Config) { c.Untrack(svc.Unit, svc.Scope) }) == nil {
				m.setMsg("Untracked %s (log files kept)", svc.Unit)
			}
			return nil
		}},
		{"Move up", "alt+p", "Move the service up in the list", true, func(m *Model, s *config.Service) tea.Cmd { return m.move(s, -1) }},
		{"Move down", "alt+n", "Move the service down in the list", true, func(m *Model, s *config.Service) tea.Cmd { return m.move(s, 1) }},
		{"Filter", "ctrl+s", "Show only the services that match what you type", false, func(m *Model, _ *config.Service) tea.Cmd { return m.startFilter() }},
		{"Refresh", "g", "Update the status of all services now", false, func(m *Model, _ *config.Service) tea.Cmd {
			m.setMsg("Refreshed")
			return tea.Batch(m.refreshCmd(), m.healthCmd())
		}},
		{"Toggle daemon", "D", "Turn the ServiceMan daemon (logging and watching) on or off; press twice to restart it", false, func(m *Model, _ *config.Service) tea.Cmd { return m.toggleDaemon() }},
		{"Run command", "alt+x", "Run a command by name", false, func(m *Model, _ *config.Service) tea.Cmd { return m.openPalette() }},
		{"Help", "? ctrl+h", "Show the keybindings", false, func(m *Model, _ *config.Service) tea.Cmd { return m.showHelp() }},
		{"Quit", "q", "Quit ServiceMan (also C-x C-c)", false, func(m *Model, _ *config.Service) tea.Cmd { return m.quit() }},
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
