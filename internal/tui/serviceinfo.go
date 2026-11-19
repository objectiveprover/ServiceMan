package tui

import (
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/serviceinfo"
	"serviceman/internal/systemd"
)

// A server with a `serviceman-info` subcommand describes itself; the New and
// Edit Service forms fill their empty fields from it, and a unit tracked as
// it is gets its health URL and app config from it. Before its first start
// such a server has no port yet, so the health URL is filled in once it has
// started and picked one.

// serviceInfoMsg carries a binary's answer to the form it was asked for.
type serviceInfoMsg struct {
	f    *form
	bin  string
	info *serviceinfo.Info
}

// portWaitMsg carries the health URL of a service whose server has just
// picked its port ("" when it didn't within the wait).
type portWaitMsg struct {
	svc config.Service
	url string
}

// portWait is how long to wait after installing a service for its server to
// pick a port, and how often to ask.
var portWait, portPoll = 20 * time.Second, 500 * time.Millisecond

// execBinary is the program an ExecStart= line runs ("" when it isn't a file
// path): systemd's prefixes (-, @, +, !, :) are dropped and ~ expanded.
func execBinary(exec string) string {
	f := strings.Fields(exec)
	if len(f) == 0 {
		return ""
	}
	bin := config.ExpandTilde(strings.TrimLeft(f[0], "-@+!:"))
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
	if u := info.HealthURL(); u != "" && f.val("url") == "" {
		f.get("url").ti.SetValue(u)
	}
	f.info, f.infoBin = info, msg.bin
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
// said: the health URL (once the server has a port) and the app config (when
// the file exists). Values already set are kept. It tells whether anything
// was filled in.
func fillTracked(s *config.Service, info *serviceinfo.Info) bool {
	if info == nil {
		return false
	}
	filled := false
	if u := info.HealthURL(); u != "" && s.URL == "" {
		s.URL, filled = u, true
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
func trackedNote(svc config.Service, info *serviceinfo.Info, bin string, filled bool) string {
	switch {
	case info == nil:
		return ""
	case filled && svc.URL == "":
		return " — filled in from " + config.BinaryName(bin) + " " + serviceinfo.Command + "; the health URL follows once it picks its port"
	case filled:
		return " — filled in from " + config.BinaryName(bin) + " " + serviceinfo.Command
	case svc.URL == "":
		return " — its health URL follows once it picks its port"
	}
	return ""
}

// followPort waits for a just-tracked server without a port yet to pick one
// (nil when there's nothing to wait for).
func followPort(svc config.Service, info *serviceinfo.Info, bin string) tea.Cmd {
	if info == nil || bin == "" || svc.URL != "" || info.HealthURL() != "" {
		return nil
	}
	return waitForPort(svc, bin)
}

// waitForPort follows a new service whose server had no port yet: once its
// first start has picked one, the health URL is set (if still empty).
func waitForPort(svc config.Service, bin string) tea.Cmd {
	return func() tea.Msg {
		deadline := time.Now().Add(portWait)
		for time.Now().Before(deadline) {
			time.Sleep(portPoll)
			if info, err := serviceinfo.Query(bin); err == nil {
				if u := info.HealthURL(); u != "" {
					return portWaitMsg{svc, u}
				}
			}
		}
		return portWaitMsg{svc, ""}
	}
}

func (m *Model) applyPortWait(msg portWaitMsg) tea.Cmd {
	if msg.url == "" {
		return nil
	}
	set := false
	if m.updateService(msg.svc, func(s *config.Service) {
		if s.URL == "" {
			s.URL, set = msg.url, true
		}
	}) != nil || !set {
		return nil
	}
	m.setMsg("%s picked its port: health URL %s", msg.svc.Unit, msg.url)
	return m.healthCmd()
}
