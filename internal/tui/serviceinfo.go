package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/serviceinfo"
	"serviceman/internal/systemd"
)

// A server with a `serviceman-info` subcommand describes itself; the New and
// Edit Service forms fill their empty fields from it, and a unit tracked as
// it is gets its app config from it. Its health URL is set to "auto"
// (config.AutoURL): looked up from serviceman-info whenever it's checked, so
// it's there from the start, follows the port the server picks on its first
// start and any later change to it.

// serviceInfoMsg carries a binary's answer to the form it was asked for.
type serviceInfoMsg struct {
	f    *form
	bin  string
	info *serviceinfo.Info
}

// execBinary is the program an ExecStart= line runs ("" when it isn't a file
// path): systemd's prefixes (-, @, +, !, :) are dropped, and ~ and a leading
// %h (systemd's specifier for the home directory; the forms only handle user
// units, where that's the user's) are expanded.
func execBinary(exec string) string {
	f := strings.Fields(exec)
	if len(f) == 0 {
		return ""
	}
	bin := strings.TrimLeft(f[0], "-@+!:")
	if rest, ok := strings.CutPrefix(bin, "%h"); ok && (rest == "" || strings.HasPrefix(rest, "/")) {
		bin = "~" + rest
	}
	bin = config.ExpandTilde(bin)
	if !strings.HasPrefix(bin, "/") {
		return ""
	}
	return bin
}

// queryServiceInfo asks the form's binary for its service info, off the UI
// goroutine (a binary gets up to serviceinfo.Timeout to answer).
func queryServiceInfo(f *form, bin string, cache *serviceinfo.Cache) tea.Cmd {
	if bin == "" {
		return nil
	}
	return func() tea.Msg {
		info, err := cache.Query(bin)
		if err != nil {
			return nil
		}
		return serviceInfoMsg{f, bin, info}
	}
}

// applyServiceInfo fills the form from msg when it's still open: only fields
// that are empty or still hold what the form was opened with, so nothing
// typed meanwhile is lost.
func (m *Model) applyServiceInfo(msg serviceInfoMsg) {
	f, info := msg.f, msg.info
	if m.form != f || m.mode != modeForm {
		return
	}
	untouched := func(id string) bool { return f.val(id) == f.initial[id] }
	if f.edit == nil {
		if info.Description != "" && untouched("desc") {
			f.get("desc").ti.SetValue(info.Description)
		}
		if len(info.Args) > 0 && untouched("exec") && f.val("exec") == msg.bin {
			f.get("exec").ti.SetValue(info.ExecStart(msg.bin))
		}
	}
	if info.EnvFile != nil && f.val("envfile") == "" {
		if p := config.ExpandTilde(*info.EnvFile); config.EnvFileProblem(p) != "missing" {
			f.get("envfile").ti.SetValue(config.Tilde(p))
		}
	}
	if info.ConfigPath != "" && f.val("config") == "" {
		if _, err := os.Stat(config.ExpandTilde(info.ConfigPath)); err == nil {
			f.get("config").ti.SetValue(config.Tilde(config.ExpandTilde(info.ConfigPath)))
		}
	}
	if f.val("url") == "" {
		f.get("url").ti.SetValue(config.AutoURL)
	}
	f.info = info
	m.setMsg("Filled in from %s %s", config.BinaryName(msg.bin), serviceinfo.Command)
}

// unitInfo is a unit's binary and what it said with serviceman-info.
type unitInfo struct {
	bin  string
	info *serviceinfo.Info
}

// unitInfoMsg brings the unitInfo of the user units that have one.
type unitInfoMsg map[string]unitInfo

// unitInfoCmd asks the binaries of the user units in entries for their
// service info, off the UI goroutine. System units, and units running the
// distribution's programs, aren't asked: those aren't servers set up by the
// service conventions.
func (m *Model) unitInfoCmd(entries []systemd.UnitEntry) tea.Cmd {
	var units []string
	for _, e := range entries {
		if e.Scope == config.User && !strings.Contains(e.Unit, "@.") { // not templates
			units = append(units, e.Unit)
		}
	}
	if len(units) == 0 {
		return nil
	}
	cache := m.infoCache
	return func() tea.Msg {
		st, _ := systemd.Show(config.User, units)
		out := unitInfoMsg{}
		for _, s := range st {
			if s.ExecPath == "" || distroPath(s.ExecPath) {
				continue
			}
			if info, err := cache.Query(s.ExecPath); err == nil {
				out[config.Service{Unit: s.Unit, Scope: config.User}.Key()] = unitInfo{s.ExecPath, info}
			}
		}
		return out
	}
}

// distroPath tells whether a program comes with the distribution.
func distroPath(p string) bool {
	if strings.HasPrefix(p, "/usr/local/") {
		return false
	}
	for _, d := range []string{"/usr/", "/bin/", "/sbin/", "/lib/", "/lib64/", "/snap/"} {
		if strings.HasPrefix(p, d) {
			return true
		}
	}
	return false
}

// fillTracked fills in a service about to be tracked from what its binary
// said: the health URL ("auto") and the app config (when the file exists).
// Values already set are kept. It tells whether anything was filled in.
func fillTracked(s *config.Service, info *serviceinfo.Info) bool {
	if info == nil {
		return false
	}
	filled := false
	if s.URL == "" {
		s.URL, filled = config.AutoURL, true
	}
	if info.ConfigPath != "" && s.ConfigPath == "" {
		p := config.ExpandTilde(info.ConfigPath)
		if _, err := os.Stat(p); err == nil {
			s.ConfigPath, filled = config.Tilde(p), true
		}
	}
	return filled
}

// trackedNote is what tracking svc says about its service info.
func trackedNote(info *serviceinfo.Info, bin string, filled bool) string {
	if info == nil || !filled {
		return ""
	}
	return " — filled in from " + config.BinaryName(bin) + " " + serviceinfo.Command
}

// healthURL is the URL checked for s: its URL, or for "auto" what its
// server reports now ("" when there's none: no port yet, or its binary
// hasn't answered). An "auto" binary is never run here (this runs on the UI
// goroutine): its answer comes from the cache the health checks fill.
func (m *Model) healthURL(s config.Service) string {
	if s.URL != config.AutoURL {
		return s.URL
	}
	if info, ok := m.infoCache.Peek(m.status[s.Key()].ExecPath); ok && info != nil {
		return info.HealthURL()
	}
	return ""
}
