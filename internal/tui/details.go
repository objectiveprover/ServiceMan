package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

func (m *Model) detailView(w int) string {
	s := m.selected()
	sep := rule(w) + "\n"
	if s == nil {
		return sep
	}
	st := m.status[s.Key()]
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
		title += sDim.Render(" — ") + sDesc.Render(st.Description)
	}
	if st.NRestarts > 0 {
		title += sYellow.Render(fmt.Sprintf("  restarts: %d", st.NRestarts))
	}

	run := label("Run", "") + orDash(config.Tilde(st.ExecPath))
	if note := quickExitNote(st); note != "" {
		run += "  " + note
	}

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

// loggingDetail is the Logging row: Off, On, or Inactive (on, but the
// serviceman daemon that writes the log file is off).
func (m *Model) loggingDetail(s config.Service) string {
	switch {
	case s.NoRecord:
		return sDim.Render("○ Off")
	case m.daemonState != "active":
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
	ws, ok := m.watch[s.Key()]
	switch {
	case m.daemonState != "active": // on, but nothing watches while the daemon is off
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
	case m.daemonState != "active":
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
	ws, ok := m.watch[s.Key()]
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

// quickExitNote explains, for the details box, a program that exited right
// after starting ("" when it didn't).
func quickExitNote(st systemd.Status) string {
	after := runDur(st.RunTime) + " after starting"
	switch st.QuickExit() {
	case systemd.CleanQuickExit:
		return sYellow.Render("Probably not a service: it exited on its own " + after + " (status 0)")
	case systemd.CrashQuickExit:
		how := fmt.Sprintf("exited with status %d", st.ExitStatus)
		if st.ExitCode != 1 {
			how = fmt.Sprintf("was killed by signal %d (%s)", st.ExitStatus, syscall.Signal(st.ExitStatus))
		}
		return sRed.Render("Crashed on start: it " + how + " " + after + " (journal: RET)")
	}
	return ""
}
