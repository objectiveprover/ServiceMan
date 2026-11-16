// Package tui is the serviceman terminal interface (Bubble Tea, Emacs keys).
package tui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	statusErr   string

	msg    string
	msgErr bool
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
		m.setMsg("No services tracked yet — press a to track an existing one, + to create one, ? for help")
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
	err    error
}
type healthMsg map[string]healthResult
type actionDoneMsg struct {
	desc  string
	err   error
	after func(m *Model) tea.Cmd
}
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
	return tea.Batch(m.refreshCmd(), m.healthCmd(), tick(), healthTick())
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
		lg := "not installed"
		if _, err := os.Stat(logger.UnitPath()); err == nil {
			lg = systemd.IsActive(config.User, logger.UnitName)
		}
		ws, _ := watcher.LoadStates(config.WatchStatePath())
		return statusMsg{res, ws, lg, firstErr}
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
}
func (m *Model) setErr(format string, a ...any) {
	m.msg, m.msgErr = fmt.Sprintf(format, a...), true
}

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
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.refreshCmd(), tick())

	case healthTickMsg:
		return m, tea.Batch(m.healthCmd(), healthTick())

	case statusMsg:
		m.status, m.watch, m.loggerState = msg.status, msg.watch, msg.logger
		if m.bins != nil {
			m.bins.load(m.cfg.BinDir) // pick up newly deployed binaries
		}
		m.statusErr = ""
		if msg.err != nil {
			m.statusErr = msg.err.Error()
		}
		return m, nil

	case healthMsg:
		m.health = msg
		return m, nil

	case needAuthMsg:
		return m, m.execInteractive(msg.cmd, msg.desc, msg.after)

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
		return m, tea.Batch(cmds...)

	case logLinesMsg, logEndMsg:
		if m.pager != nil {
			return m, m.pager.handleStream(msg)
		}
		return m, nil

	case pickerLoadedMsg:
		if m.picker != nil {
			m.picker.loaded(msg)
		}
		return m, nil

	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()

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
	switch m.mode {
	case modePager:
		body = m.pager.view(m.w, bodyH)
	case modePicker:
		body = m.picker.view(m, m.w, bodyH)
	case modeBinPicker:
		body = m.bins.view(m, m.w, bodyH)
	case modeForm:
		body = m.form.view(m.w, bodyH)
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
	if m.prompt != nil {
		if c := m.prompt.candidatesView(m.w, m.h/3); c != "" {
			b.WriteString(c + "\n")
		}
	}
	b.WriteString(m.modeLine() + "\n")
	if m.prompt != nil {
		b.WriteString(m.prompt.view(m.w))
	} else {
		st := sDim
		if m.msgErr {
			st = sRed
		}
		b.WriteString(st.Render(trunc(m.msg, m.w)))
	}
	return b.String()
}

func (m *Model) modeLine() string {
	var left string
	switch m.mode {
	case modePager:
		left = " serviceman: " + m.pager.modeLine()
	case modePicker:
		left = " serviceman: Track existing service"
	case modeBinPicker:
		left = " serviceman: Binaries in " + config.Tilde(m.cfg.BinDir)
	case modeForm:
		left = " serviceman: New service   C-c C-c install · C-c C-e edit in Emacs & install · C-g cancel"
	default:
		left = fmt.Sprintf(" serviceman: Services (%d/%d)", min(m.cursor+1, len(m.rows())), len(m.rows()))
		if m.filter != "" {
			left += "  filter: " + m.filter
		}
	}
	right := "daemon: " + m.loggerState + " "
	gap := m.w - len([]rune(left)) - len([]rune(right))
	if gap < 1 {
		return sModeLine.Render(pad(left, m.w))
	}
	return sModeLine.Render(left + strings.Repeat(" ", gap) + right)
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
	p := newPager("Logs " + s.Unit + " (journal)")
	m.pager, m.mode = p, modePager
	return p.stream(context.Background(), exec.Command("journalctl", args...))
}

func (m *Model) showStatic(title string, cmd *exec.Cmd) tea.Cmd {
	out, _ := cmd.CombinedOutput()
	p := newPager(title)
	p.follow = false
	p.setLines(strings.Split(strings.TrimRight(string(out), "\n"), "\n"))
	m.pager, m.mode = p, modePager
	return nil
}
