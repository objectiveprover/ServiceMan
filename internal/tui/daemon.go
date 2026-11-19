package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/daemon"
	"serviceman/internal/systemd"
)

// toggleDaemon stops the ServiceMan daemon when it's running, and otherwise
// (re)installs and starts it, so pressing D twice restarts it.
func (m *Model) toggleDaemon() tea.Cmd {
	if m.daemonBusy {
		m.setMsg("The ServiceMan daemon is still turning %s…", map[bool]string{true: "on", false: "off"}[m.daemonState == "activating"])
		return nil
	}
	m.daemonBusy = true
	switch {
	case systemd.IsRunning(m.daemonState):
		m.setMsg("Turning the ServiceMan daemon off…")
		m.daemonState = "deactivating"
		return func() tea.Msg {
			return daemonDoneMsg{actionDoneMsg{desc: "ServiceMan daemon off (logging and watching stopped; D turns it on)", err: daemon.Disable()}}
		}
	default:
		m.setMsg("Turning the ServiceMan daemon on…")
		m.daemonState = "activating"
		return func() tea.Msg {
			notes, err := daemon.Install()
			return daemonDoneMsg{actionDoneMsg{desc: "ServiceMan daemon on: " + strings.Join(notes, "; "), err: err}}
		}
	}
}

// daemonOffNote explains an Inactive watching/logging row.
func (m *Model) daemonOffNote() string {
	return sYellow.Render(" · ServiceMan daemon is " + orDash(m.daemonState) + " (D turns it on)")
}

var daemonLabel = sDim.Render("Daemon (") + sKey.Render("D") + sDim.Render("): ")

// daemonCell is the daemon state for the top header.
func (m *Model) daemonCell() string {
	if m.daemonOld && m.daemonState == "active" {
		return daemonLabel + sGreen.Render("active") +
			sYellow.Render(" (old unit name serviceman-logger; press D twice to migrate)")
	}
	switch m.daemonState {
	case "":
		return daemonLabel + sDim.Render("…")
	case "active":
		return daemonLabel + sGreen.Render("active")
	case "failed":
		return daemonLabel + sRed.Render("failed")
	default:
		return daemonLabel + sYellow.Render(m.daemonState)
	}
}
