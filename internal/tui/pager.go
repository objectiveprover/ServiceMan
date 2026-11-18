package tui

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const maxPagerLines = 20000

var (
	reErr  = regexp.MustCompile(`(?i)\b(error|err|fail(ed|ure)?|fatal|panic|crit(ical)?|emerg|alert|exception|traceback)\b`)
	reWarn = regexp.MustCompile(`(?i)\b(warn(ing)?|deprecated)\b`)
)

type pager struct {
	title     string // the view's name in the top bar, e.g. Journal
	subject   string // e.g. the unit name
	lastShown int    // 1-based index of the last line on screen
	lines     []string
	offset    int
	follow    bool
	wrap      bool
	search    string
	w, h      int

	// live stream
	session int
	live    bool
	ended   string
	cmd     *exec.Cmd
	done    chan struct{}
	once    sync.Once
}

var pagerSession int

type logLinesMsg struct {
	session int
	lines   []string
	ch      chan string // nil when the stream has ended
}
type logEndMsg struct {
	session int
}

func newPager(title, subject string) *pager {
	return &pager{title: title, subject: subject, follow: true, wrap: true}
}

func (p *pager) setLines(l []string) {
	p.lines = l
	if p.follow {
		p.offset = p.bottomOffset()
	}
}

// stream starts cmd and feeds its output lines to the pager.
func (p *pager) stream(cmd *exec.Cmd) tea.Cmd {
	pagerSession++
	p.session = pagerSession
	p.live = true
	p.done = make(chan struct{})
	p.cmd = cmd
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	ch := make(chan string, 4096)
	if err := cmd.Start(); err != nil {
		p.live = false
		p.ended = err.Error()
		return nil
	}
	go func() { cmd.Wait(); pw.Close() }()
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			select {
			case ch <- sc.Text():
			case <-p.done:
				return
			}
		}
	}()
	return waitLines(p.session, ch)
}

func waitLines(session int, ch chan string) tea.Cmd {
	return func() tea.Msg {
		l, ok := <-ch
		if !ok {
			return logEndMsg{session}
		}
		lines := []string{l}
		for len(lines) < 5000 {
			select {
			case l, ok := <-ch:
				if !ok {
					return logLinesMsg{session, lines, nil}
				}
				lines = append(lines, l)
			default:
				return logLinesMsg{session, lines, ch}
			}
		}
		return logLinesMsg{session, lines, ch}
	}
}

func (p *pager) handleStream(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case logLinesMsg:
		if msg.session != p.session {
			return nil
		}
		p.append(msg.lines)
		if msg.ch == nil {
			p.live, p.ended = false, "stream ended"
			return nil
		}
		return waitLines(p.session, msg.ch)
	case logEndMsg:
		if msg.session == p.session {
			p.live, p.ended = false, "stream ended"
		}
	}
	return nil
}

func (p *pager) append(lines []string) {
	p.lines = append(p.lines, lines...)
	if over := len(p.lines) - maxPagerLines; over > 0 {
		p.lines = p.lines[over:]
		p.offset = max(0, p.offset-over)
	}
	if p.follow {
		p.offset = p.bottomOffset()
	}
}

func (p *pager) stop() {
	p.once.Do(func() {
		if p.done != nil {
			close(p.done)
		}
		if p.cmd != nil && p.cmd.Process != nil {
			p.cmd.Process.Kill()
		}
	})
}

// ------------------------------------------------------------------ geometry

// segments is how line i is displayed: wrapped or truncated to the width.
func (p *pager) segments(i int) []string {
	line := strings.ReplaceAll(p.lines[i], "\t", "    ")
	if p.w <= 0 {
		return []string{line}
	}
	if p.wrap && ansi.StringWidth(line) > p.w {
		return strings.Split(ansi.Hardwrap(line, p.w, true), "\n")
	}
	return []string{trunc(line, p.w)}
}

func (p *pager) lineHeight(i int) int { return len(p.segments(i)) }

func (p *pager) bottomOffset() int {
	if p.h <= 0 {
		return max(0, len(p.lines)-1)
	}
	used := 0
	for i := len(p.lines) - 1; i >= 0; i-- {
		used += p.lineHeight(i)
		if used > p.h {
			return i + 1
		}
	}
	return 0
}

func (p *pager) scroll(n int) {
	p.offset = max(0, min(p.offset+n, p.bottomOffset()))
	p.follow = p.offset >= p.bottomOffset()
}

// ------------------------------------------------------------------ keys

func (p *pager) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	page := max(1, p.h-2)
	switch msg.String() {
	case "q", "ctrl+g", "esc", "enter": // RET opened the journal, so RET also goes back
		p.stop()
		m.pager, m.mode = nil, modeList
		return m.refreshCmd()
	case "ctrl+n", "down", "n":
		p.scroll(1)
	case "ctrl+p", "up", "p":
		p.scroll(-1)
	case "ctrl+v", "pgdown", " ":
		p.scroll(page)
	case "alt+v", "pgup", "backspace":
		p.scroll(-page)
	case "alt+up", "alt+<", "<", "home":
		p.offset, p.follow = 0, false
	case "alt+down", "alt+>", ">", "end":
		p.offset, p.follow = p.bottomOffset(), true
	case "f":
		p.follow = !p.follow
		if p.follow {
			p.offset = p.bottomOffset()
		}
	case "w":
		p.wrap = !p.wrap
		p.offset = min(p.offset, p.bottomOffset())
	case "ctrl+s", "/":
		p.isearch(m, 1)
	case "ctrl+r":
		p.isearch(m, -1)
	case "ctrl+l": // Emacs's recenter: every key redraws the screen already
	default:
		m.setErr("%s is undefined in viewer (q to go back)", emacsKey(msg.String()))
	}
	return nil
}

// isearch implements Emacs-style incremental search in the minibuffer.
func (p *pager) isearch(m *Model, dir int) {
	origin, origFollow := p.offset, p.follow
	pos := p.offset
	label := func(d int, failing bool) string {
		s := "I-search: "
		if d < 0 {
			s = "I-search backward: "
		}
		if failing {
			s = "Failing " + s
		}
		return s
	}
	pr := newPrompt(label(dir, false), "")
	find := func(q string, from, d int) {
		if q == "" {
			return
		}
		ql := strings.ToLower(q)
		for i := from; i >= 0 && i < len(p.lines); i += d {
			if strings.Contains(strings.ToLower(p.lines[i]), ql) {
				pos = i
				p.offset = max(0, min(i-p.h/3, p.bottomOffset()))
				p.follow = false
				pr.label = label(d, false)
				return
			}
		}
		pr.label = label(d, true)
	}
	pr.onChange = func(m *Model, v string) {
		p.search = v
		find(v, pos, dir)
	}
	pr.onKey = func(m *Model, k string) (bool, tea.Cmd) {
		switch k {
		case "ctrl+s":
			if pr.ti.Value() == "" && p.search != "" {
				pr.ti.SetValue(p.search)
				pr.ti.CursorEnd()
			}
			dir = 1
			find(pr.ti.Value(), pos+1, 1)
			return true, nil
		case "ctrl+r":
			if pr.ti.Value() == "" && p.search != "" {
				pr.ti.SetValue(p.search)
				pr.ti.CursorEnd()
			}
			dir = -1
			find(pr.ti.Value(), pos-1, -1)
			return true, nil
		}
		return false, nil
	}
	pr.onSubmit = func(m *Model, v string) tea.Cmd {
		p.search = v
		return nil
	}
	pr.onCancel = func(m *Model) {
		p.offset, p.follow = origin, origFollow
		p.search = ""
	}
	m.prompt = pr
}

// ------------------------------------------------------------------ view

// header is the viewer's top bar: what is shown and its state on the left,
// the position on the right. Each toggle shows its key; the other keys are
// in the bottom bar.
func (p *pager) header(w int) string {
	var left []string
	if p.subject != "" {
		left = append(left, sDim.Render(p.subject))
	}
	if p.streamed() {
		switch {
		case p.live:
			left = append(left, sGreen.Render("● Live"))
		case p.ended == "stream ended":
			left = append(left, sYellow.Render("○ Ended"))
		default:
			left = append(left, sRed.Render("✖ Ended: "+p.ended))
		}
		left = append(left, onOff("Follow", "f", p.follow))
	}
	left = append(left, onOff("Wrap", "w", p.wrap))

	pos := "All"
	if len(p.lines) > 0 && p.bottomOffset() > 0 {
		switch {
		case p.offset == 0:
			pos = "Top"
		case p.offset >= p.bottomOffset():
			pos = "Bottom"
		default:
			pos = fmt.Sprintf("%d%%", p.offset*100/max(1, p.bottomOffset()))
		}
	}
	lines := "No lines"
	if len(p.lines) > 0 {
		lines = fmt.Sprintf("Lines %d–%d of %d", min(p.offset+1, len(p.lines)), max(p.offset+1, p.lastShown), len(p.lines))
	}
	return topBar(w, p.title, left, sDim.Render(lines)+"  "+sDim.Render(pos))
}

// streamed is true for a live command's output (the journal), as opposed to
// static text (help, status).
func (p *pager) streamed() bool { return p.live || p.ended != "" }

func (p *pager) view(w, h int) string {
	h = max(1, h-2)             // header + separator
	cw := max(1, w-len(gutter)) // text starts after the gutter, like the rest of the UI
	resize := p.w != cw || p.h != h
	p.w, p.h = cw, h
	if resize && p.follow {
		p.offset = p.bottomOffset()
	}
	var out []string
	p.lastShown = 0
	for i := p.offset; i < len(p.lines) && len(out) < h; i++ {
		for _, s := range p.segments(i) {
			if len(out) >= h {
				break
			}
			out = append(out, gutter+p.style(s))
		}
		p.lastShown = i + 1
	}
	// The header comes last so it reports what was actually laid out.
	return p.header(w) + "\n" + strings.Join(out, "\n")
}

func (p *pager) style(s string) string {
	if strings.Contains(s, "\x1b[") { // already styled (help text)
		return s
	}
	base := lipglossNone
	switch {
	case reErr.MatchString(s):
		base = sRed.Render
	case reWarn.MatchString(s):
		base = sYellow.Render
	}
	if p.search == "" {
		return base(s)
	}
	// highlight matches, case-insensitively
	lower, q := strings.ToLower(s), strings.ToLower(p.search)
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(lower[i:], q)
		if j < 0 || len(lower) != len(s) {
			b.WriteString(base(s[i:]))
			break
		}
		b.WriteString(base(s[i : i+j]))
		b.WriteString(sMatch.Render(s[i+j : i+j+len(q)]))
		i += j + len(q)
	}
	return b.String()
}

func lipglossNone(s ...string) string { return strings.Join(s, " ") }
