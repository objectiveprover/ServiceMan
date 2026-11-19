package tui

import (
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/serviceinfo"
)

// A server with a `service-info` subcommand describes itself; the New and
// Edit Service forms fill their empty fields from it. Before its first start
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
func queryServiceInfo(f *form, bin string) tea.Cmd {
	if bin == "" {
		return nil
	}
	return func() tea.Msg {
		info, err := serviceinfo.Query(bin)
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
	m.setMsg("Filled in from %s service-info", config.BinaryName(msg.bin))
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
