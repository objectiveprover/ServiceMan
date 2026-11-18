// Package tui is the serviceman terminal interface (Bubble Tea, Emacs keys).
package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
	"serviceman/internal/logger"
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
	loggerState string
	daemonBusy  bool // a D toggle is in flight
	daemonOld   bool // still installed as serviceman-logger.service
	statusErr   string

	msg    string
	msgErr bool
	msgSeq int // bumped by setMsg/setErr so each message gets its own 5 s timer
	prefix string

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

func svcKey(s config.Service) string { return string(s.Scope) + "/" + s.Unit }

// ------------------------------------------------------------------ messages

type tickMsg struct{}
type healthTickMsg struct{}
type statusMsg struct {
	status map[string]systemd.Status
	watch  map[string]watcher.State
	logger string
	legacy bool // the serviceman daemon still runs as serviceman-logger.service
	err    error
}
type healthMsg map[string]healthResult
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

type healthResult struct {
	code int
	dur  time.Duration
	err  error
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}
func healthTick() tea.Cmd {
	return tea.Tick(10*time.Second, func(time.Time) tea.Msg { return healthTickMsg{} })
}

func (m *Model) Init() tea.Cmd {
	s := m.msgSeq // the startup message expires too
	clear := tea.Tick(msgTimeout, func(time.Time) tea.Msg { return clearMsgMsg{s} })
	return tea.Batch(m.refreshCmd(), m.healthCmd(), tick(), healthTick(), clear)
}

func (m *Model) refreshCmd() tea.Cmd {
	svcs := append([]config.Service(nil), m.cfg.Services...)
	return func() tea.Msg {
		by := map[config.Scope][]string{}
		for _, s := range svcs {
			by[s.Scope] = append(by[s.Scope], s.Unit)
		}
		res := map[string]systemd.Status{}
		var firstErr error
		for sc, units := range by {
			st, err := systemd.Show(sc, units)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			for u, s := range st {
				res[string(sc)+"/"+u] = s
			}
		}
		lg, legacy := logger.State()
		ws, _ := watcher.LoadStates(config.WatchStatePath())
		return statusMsg{res, ws, lg, legacy, firstErr}
	}
}

func (m *Model) healthCmd() tea.Cmd {
	var svcs []config.Service
	for _, s := range m.cfg.Services {
		if s.URL != "" {
			svcs = append(svcs, s)
		}
	}
	if len(svcs) == 0 {
		return nil
	}
	return func() tea.Msg {
		client := &http.Client{Timeout: 4 * time.Second}
		res := healthMsg{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, s := range svcs {
			wg.Add(1)
			go func(s config.Service) {
				defer wg.Done()
				start := time.Now()
				r := healthResult{}
				resp, err := client.Get(s.URL)
				r.dur = time.Since(start)
				if err != nil {
					r.err = err
				} else {
					r.code = resp.StatusCode
					resp.Body.Close()
				}
				mu.Lock()
				res[svcKey(s)] = r
				mu.Unlock()
			}(s)
		}
		wg.Wait()
		return res
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
		st := m.status[svcKey(s)]
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
			m.loggerState = msg.logger
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

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()

	// As in Emacs, the echo area is cleared by the next key, which brings the
	// key reference back.
	if m.prefix == "" {
		m.msg, m.msgErr = "", false
	}

	// ESC is a Meta prefix, as in Emacs: ESC x == M-x. ESC ESC (or a fast
	// double tap, which arrives as M-ESC) falls through as a plain ESC, which
	// cancels like C-g.
	if k == "alt+esc" {
		msg.Alt, k = false, "esc"
		if m.prefix == "esc" {
			m.prefix, m.msg = "", ""
		}
	} else if m.prefix == "esc" {
		m.prefix, m.msg = "", ""
		if k == "ctrl+g" {
			m.setMsg("Quit")
			return nil
		}
		if k != "esc" {
			msg.Alt = true
			k = msg.String()
		}
	} else if k == "esc" && m.prefix == "" {
		m.prefix = k
		m.setMsg("ESC-")
		return nil
	}

	if m.prefix != "" {
		p := m.prefix
		m.prefix = ""
		combo := p + " " + k
		if combo == "ctrl+x ctrl+c" {
			return m.quit()
		}
		if k == "ctrl+g" {
			m.setMsg("Quit")
			return nil
		}
		if m.mode == modeForm && m.prompt == nil {
			if cmd, ok := m.form.prefixKey(m, combo); ok {
				return cmd
			}
		}
		m.setErr("%s is undefined", emacsKey(combo))
		return nil
	}
	if k == "ctrl+x" || (k == "ctrl+c" && m.prompt == nil) {
		m.prefix = k
		m.setMsg("%s-", emacsKey(k))
		return nil
	}

	if m.prompt != nil {
		return m.promptKey(msg)
	}

	switch m.mode {
	case modePager:
		return m.pager.key(m, msg)
	case modePicker:
		return m.picker.key(m, msg)
	case modeBinPicker:
		return m.bins.key(m, msg)
	case modeForm:
		return m.form.key(m, msg)
	default:
		return m.listKey(msg)
	}
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

func (m *Model) footer() string {
	var b strings.Builder
	b.WriteString(rule(m.w) + "\n")
	switch {
	case m.prompt != nil:
		b.WriteString(m.prompt.view(m.w))
	case m.msg != "":
		st := sDim
		if m.msgErr {
			st = sRed
		}
		b.WriteString(trunc(gutter+st.Render(m.msg), m.w))
	default:
		b.WriteString(m.keyHints())
	}
	return b.String()
}

// keyHints is the bottom line when the echo area is empty: the main keys of
// the current mode.
func (m *Model) keyHints() string {
	var keys []string
	switch m.mode {
	case modePager:
		// Follow and Wrap show their keys in the top bar.
		keys = []string{"Back", "RET/q", "Search", "C-s", "Top/Bottom", "M-↑/M-↓"}
	case modePicker:
		keys = []string{"Back", "C-g", "Type to filter", "", "Track/Untrack", "RET", "Move", "C-n/C-p"}
	case modeBinPicker:
		keys = []string{"Back", "C-g", "Create/Edit service", "RET", "Move", "C-n/C-p", "Type to filter", ""}
	case modeForm:
		keys = []string{"Cancel", "C-g", "Install", "C-c C-c", "Emacs & install", "C-c C-e", "Field", "TAB/S-TAB", "Cycle choice", "SPC"}
		if m.form.edit != nil {
			keys[2], keys[4] = "Save", "Emacs & save"
		}
	default:
		keys = []string{"Help", "?", "Quit", "q", "Commands", "M-x", "Start/Stop", "s", "Edit", "e", "Filter", "/", "Journal", "RET"}
	}
	return hintLine(m.w, "", keys)
}

// hintLine renders "Name (key)   Name (key) …" (name/key pairs, like the
// details box labels) after an optional lead, dropping pairs from the end
// when they don't fit.
func hintLine(w int, lead string, kv []string) string {
	render := func(d, k string) string {
		if k == "" {
			return sDim.Render(d)
		}
		return sDim.Render(d+" (") + sKey.Render(k) + sDim.Render(")")
	}
	sep := sDim.Render("   ")
	out := gutter
	if lead != "" {
		out += sBold.Render(lead) + "   "
	}
	for i := 0; i+1 < len(kv); i += 2 {
		next := render(kv[i], kv[i+1])
		if i > 0 {
			next = sep + next
		}
		if i > 0 && ansi.StringWidth(out+next) > w {
			break
		}
		out += next
	}
	return trunc(out, w)
}

// topBar is the first line of every view plus the separator under it:
// SERVICEMAN, the view's name and its context on the left, the view's main
// indicators on the right. The left side gives way when space runs out.
func topBar(w int, view string, left []string, right string) string {
	items := []string{gutter + sTitle.Render("SERVICEMAN")}
	if view != "" {
		items = append(items, sBold.Render(view))
	}
	items = append(items, left...)
	if right != "" {
		right += gutter
	}
	// Four spaces between items; two when narrow.
	var l string
	for _, gap := range []string{"    ", "  "} {
		l = strings.Join(items, gap)
		if ansi.StringWidth(l)+ansi.StringWidth(right)+2 <= w {
			break
		}
	}
	l = trunc(l, max(0, w-ansi.StringWidth(right)-2))
	bar := l + strings.Repeat(" ", max(0, w-ansi.StringWidth(l)-ansi.StringWidth(right))) + right
	return bar + "\n" + rule(w)
}

var daemonLabel = sDim.Render("Daemon (") + sKey.Render("D") + sDim.Render("): ")

// daemonCell is the daemon state for the top header.
func (m *Model) daemonCell() string {
	if m.daemonOld && m.loggerState == "active" {
		return daemonLabel + sGreen.Render("active") +
			sYellow.Render(" (old unit name serviceman-logger; press D twice to migrate)")
	}
	switch m.loggerState {
	case "":
		return daemonLabel + sDim.Render("…")
	case "active":
		return daemonLabel + sGreen.Render("active")
	case "failed":
		return daemonLabel + sRed.Render("failed")
	default:
		return daemonLabel + sYellow.Render(m.loggerState)
	}
}

// ------------------------------------------------------------------ log streams

// startLogStream opens a live journal follow for a service in the pager.
func (m *Model) startLogStream(s config.Service) tea.Cmd {
	args := []string{"-n", "1000", "-f", "-o", "short-iso", "--no-pager"}
	if s.Scope == config.User {
		args = append(args, "--user-unit", s.Unit)
	} else {
		args = append(args, "-u", s.Unit)
	}
	p := newPager("Journal", s.Unit)
	m.pager, m.mode = p, modePager
	return p.stream(context.Background(), exec.Command("journalctl", args...))
}

func (m *Model) showStatic(title, subject string, cmd *exec.Cmd) tea.Cmd {
	out, _ := cmd.CombinedOutput()
	p := newPager(title, subject)
	p.follow = false
	p.setLines(strings.Split(strings.TrimRight(string(out), "\n"), "\n"))
	m.pager, m.mode = p, modePager
	return nil
}
