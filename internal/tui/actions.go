package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

// edit opens the unit file (or a drop-in) in Emacs; why, when set, says why
// the Edit Service form wasn't used.
func (m *Model) edit(s *config.Service, override bool, why string) tea.Cmd {
	st := m.status[s.Key()]
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
	st := m.status[svc.Key()]
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

func (m *Model) toggleWatch(svc config.Service) tea.Cmd {
	on := !svc.Watch
	if m.updateService(svc, func(s *config.Service) { s.Watch = on }) != nil {
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
	if m.daemonState != "active" {
		m.setErr("%s — but the serviceman daemon is %s (D turns it on)", msg, m.daemonState)
	} else {
		m.setMsg("%s", msg)
	}
	return nil
}

func (m *Model) setConfigPath(svc config.Service, thenOpen bool) tea.Cmd {
	cur, _ := m.appConfig(svc)
	if cur == "" { // suggest the convention's directory
		if bin := config.BinaryName(m.watchedPath(svc)); bin != "" {
			cur = config.AppDir(bin) + "/"
		}
	}
	p := newPrompt("App config file for "+svc.Unit+" (empty = ~/.config/<binary>/config.*): ", config.Tilde(cur))
	p.onSubmit = func(m *Model, v string) tea.Cmd {
		v, ok := m.absPathInput(v)
		if !ok || m.updateService(svc, func(s *config.Service) { s.ConfigPath = v }) != nil {
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
		v, ok := m.absPathInput(v)
		if ok && m.updateService(svc, func(s *config.Service) { s.WatchPath = v }) == nil {
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

// absPathInput reads a path typed at a prompt: empty, or absolute after "~"
// expansion. Otherwise it says so and returns false.
func (m *Model) absPathInput(v string) (string, bool) {
	v = config.ExpandTilde(strings.TrimSpace(v))
	if v != "" && !filepath.IsAbs(v) {
		m.setErr("Use an absolute path (or ~/...)")
		return "", false
	}
	return v, true
}

// setEnvFile asks for an env file and attaches it to the unit with a
// drop-in (systemd's EnvironmentFile=), suggesting ~/.secrets/<binary>.env.
func (m *Model) setEnvFile(svc config.Service) tea.Cmd {
	cur := ""
	if dropIn := systemd.EnvFileDropInPath(svc.Scope, svc.Unit); fileExists(dropIn) {
		if st := m.status[svc.Key()]; len(st.EnvFiles) > 0 {
			cur = st.EnvFiles[len(st.EnvFiles)-1] // drop-ins come after the unit's own
		}
	}
	if cur == "" {
		if bin := config.BinaryName(m.watchedPath(svc)); bin != "" {
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
