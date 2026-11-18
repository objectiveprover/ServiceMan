// Package tui is the serviceman terminal interface (Bubble Tea, Emacs keys).
package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
	"serviceman/internal/daemon"
	"serviceman/internal/systemd"
	"serviceman/internal/watcher"
)

type mode int

const (
	modeList mode = iota
	modePager
	modePicker
	modeForm
	modeBinPicker
)

type Model struct {
	cfg  *config.Config
	w, h int
	mode mode

	cursor, offset int
	filter         string

	status      map[string]systemd.Status
	health      map[string]healthResult
	watch       map[string]watcher.State
	daemonState string
	daemonBusy  bool // a D toggle is in flight
	daemonOld   bool // still installed as serviceman-logger.service
	statusErr   string

	msg    string
	msgErr bool
	msgSeq int // bumped by setMsg/setErr so each message gets its own 5 s timer
	prefix string

	// showPreview shows the unit preview in the New/Edit Service form. It's
	// hidden until C-c C-p turns it on, and stays that way for the next forms
	// of the session.
	showPreview bool

	prompt *prompt
	pager  *pager
	picker *picker
	bins   *binPicker
	form   *form
}

func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	m := &Model{cfg: cfg, status: map[string]systemd.Status{}, health: map[string]healthResult{}}
	if len(cfg.Services) == 0 {
		m.setMsg("No services tracked yet — press a to add existing ones, c to create one, ? for help")
	}
	if v, err := config.FileVersion(config.Path()); err == nil && v < config.CurrentVersion {
		m.setErr("Your config is version %d, current is %d — run `serviceman config update` to add the new settings", v, config.CurrentVersion)
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// ------------------------------------------------------------------ messages

type tickMsg struct{}
type statusMsg struct {
	status map[string]systemd.Status
	watch  map[string]watcher.State
	daemon string
	legacy bool // the serviceman daemon still runs as serviceman-logger.service
	err    error
}
type actionDoneMsg struct {
	desc  string
	err   error
	after func(m *Model) tea.Cmd
}
type daemonDoneMsg struct{ actionDoneMsg }
type needAuthMsg struct {
	cmd   *exec.Cmd
	desc  string
	after func(m *Model) tea.Cmd
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *Model) Init() tea.Cmd {
	s := m.msgSeq // the startup message expires too
	clear := tea.Tick(msgTimeout, func(time.Time) tea.Msg { return clearMsgMsg{s} })
	return tea.Batch(m.refreshCmd(), m.healthCmd(), tick(), healthTick(), clear)
}

func (m *Model) refreshCmd() tea.Cmd {
	svcs := append([]config.Service(nil), m.cfg.Services...)
	return func() tea.Msg {
		res, err := systemd.ShowServices(svcs)
		lg, legacy := daemon.State()
		ws, _ := watcher.LoadStates(config.WatchStatePath())
		return statusMsg{res, ws, lg, legacy, err}
	}
}

// ------------------------------------------------------------------ helpers

func (m *Model) setMsg(format string, a ...any) {
	m.msg, m.msgErr = fmt.Sprintf(format, a...), false
	m.msgSeq++
}
func (m *Model) setErr(format string, a ...any) {
	m.msg, m.msgErr = fmt.Sprintf(format, a...), true
	m.msgSeq++
}

// msgTimeout is how long an echo-area message stays before the key
// reference comes back. Errors don't expire; the next key clears them.
const msgTimeout = 5 * time.Second

type clearMsgMsg struct{ seq int }

// rows returns the tracked services matching the current filter.
func (m *Model) rows() []config.Service {
	if m.filter == "" {
		return m.cfg.Services
	}
	var out []config.Service
	for _, s := range m.cfg.Services {
		st := m.status[s.Key()]
		if matchAll(m.filter, s.Unit+" "+st.Description+" "+string(s.Scope)) {
			out = append(out, s)
		}
	}
	return out
}

func matchAll(query, hay string) bool {
	hay = strings.ToLower(hay)
	for _, t := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func (m *Model) selected() *config.Service {
	rows := m.rows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return nil
	}
	s := rows[m.cursor]
	return &s
}

func (m *Model) clampCursor() {
	n := len(m.rows())
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) updateConfig(fn func(*config.Config)) error {
	c, err := config.Update(fn)
	if err != nil {
		m.setErr("config: %v", err)
		return err
	}
	m.cfg = c
	m.clampCursor()
	return nil
}

// updateService changes the tracked service svc in the config (nothing when
// it's no longer tracked).
func (m *Model) updateService(svc config.Service, fn func(*config.Service)) error {
	return m.updateConfig(func(c *config.Config) {
		if s := c.Find(svc.Unit, svc.Scope); s != nil {
			fn(s)
		}
	})
}

func (m *Model) editor() string { return systemd.ResolveEditor(m.cfg.Editor) }

// runSystemctl runs quick; if sudo needs a password, hands the terminal to
// the interactive variant.
func (m *Model) runSystemctl(quick, inter *exec.Cmd, desc string) tea.Cmd {
	return m.runSystemctlThen(quick, inter, desc, nil)
}

// runSystemctlThen is runSystemctl with a follow-up run on success.
func (m *Model) runSystemctlThen(quick, inter *exec.Cmd, desc string, after func(m *Model) tea.Cmd) tea.Cmd {
	m.setMsg("%s…", desc)
	return func() tea.Msg {
		needAuth, err := systemd.RunQuick(quick)
		if needAuth && inter != nil {
			return needAuthMsg{inter, desc, after}
		}
		return actionDoneMsg{desc: desc, err: err, after: after}
	}
}

func (m *Model) execInteractive(cmd *exec.Cmd, desc string, after func(m *Model) tea.Cmd) tea.Cmd {
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return actionDoneMsg{desc: desc, err: err, after: after} })
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// ------------------------------------------------------------------ update

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if c, ok := msg.(clearMsgMsg); ok {
		// Only the latest message expires; errors and prefix prompts like
		// "C-x-" stay until the next key.
		if c.seq == m.msgSeq && m.prefix == "" && !m.msgErr {
			m.msg, m.msgErr = "", false
		}
		return m, nil
	}
	seq := m.msgSeq
	cmd := m.update(msg)
	if m.msgSeq != seq && m.msg != "" && !m.msgErr {
		s := m.msgSeq
		cmd = tea.Batch(cmd, tea.Tick(msgTimeout, func(time.Time) tea.Msg { return clearMsgMsg{s} }))
	}
	return m, cmd
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return nil

	case tickMsg:
		return tea.Batch(m.refreshCmd(), tick())

	case healthTickMsg:
		return tea.Batch(m.healthCmd(), healthTick())

	case statusMsg:
		m.status, m.watch, m.daemonOld = msg.status, msg.watch, msg.legacy
		if !m.daemonBusy { // keep showing "activating"/"deactivating" until D finishes
			m.daemonState = msg.daemon
		}
		if m.bins != nil {
			m.bins.load(m) // pick up newly deployed binaries and service changes
		}
		m.statusErr = ""
		if msg.err != nil {
			m.statusErr = msg.err.Error()
		}
		return nil

	case healthMsg:
		m.health = msg
		return nil

	case daemonDoneMsg:
		m.daemonBusy = false
		return m.update(msg.actionDoneMsg)

	case needAuthMsg:
		return m.execInteractive(msg.cmd, msg.desc, msg.after)

	case actionDoneMsg:
		switch {
		case msg.err == nil:
			m.setMsg("%s: done", msg.desc)
		case exitCode(msg.err) == 3:
			m.setMsg("%s: cancelled", msg.desc)
		default:
			m.setErr("%s: %v", msg.desc, msg.err)
		}
		cmds := []tea.Cmd{m.refreshCmd()}
		if msg.after != nil && msg.err == nil {
			cmds = append(cmds, msg.after(m))
		}
		return tea.Batch(cmds...)

	case logLinesMsg, logEndMsg:
		if m.pager != nil {
			return m.pager.handleStream(msg)
		}
		return nil

	case pickerLoadedMsg:
		if m.picker != nil {
			m.picker.loaded(msg)
		}
		return nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return nil
}

func (m *Model) quit() tea.Cmd {
	if m.pager != nil {
		m.pager.stop()
	}
	return tea.Quit
}

// ------------------------------------------------------------------ view

func (m *Model) View() string {
	if m.w == 0 {
		return ""
	}
	footer := m.footer()
	bodyH := m.h - strings.Count(footer, "\n") - 1
	var body string
	switch {
	case m.paletteOpen():
		body = m.paletteView(m.w, bodyH)
	case m.mode == modePager:
		body = m.pager.view(m.w, bodyH)
	case m.mode == modePicker:
		body = m.picker.view(m, m.w, bodyH)
	case m.mode == modeBinPicker:
		body = m.bins.view(m, m.w, bodyH)
	case m.mode == modeForm:
		body = m.form.view(m, m.w, bodyH)
	default:
		body = m.listView(m.w, bodyH)
	}
	// Force the body to exactly bodyH lines so the footer stays put.
	lines := strings.Split(body, "\n")
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n" + footer
}
