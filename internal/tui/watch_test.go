package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
	"serviceman/internal/watcher"
)

func TestWatchCellAndDetail(t *testing.T) {
	svc := config.Service{Unit: "hello.service", Scope: config.User, Watch: true}
	k := svc.Key()
	m := &Model{
		cfg:         &config.Config{},
		status:      map[string]systemd.Status{k: {ExecPath: "/srv/bin/hello"}},
		watch:       map[string]watcher.State{},
		loggerState: "active",
	}
	plain := func(s string) string { return ansi.Strip(s) }
	cases := []struct {
		name   string
		state  *watcher.State
		logger string
		cell   string
		detail string
	}{
		{"daemon down", nil, "inactive", "●", "● Inactive · serviceman daemon is inactive (D turns it on)"},
		{"not picked up", nil, "active", "!", "! On · not picked up yet (within 5 s)"},
		{"idle", &watcher.State{}, "active", "●", "● On"},
		{"restarted", &watcher.State{Result: "ok", RestartedAt: time.Now().Add(-3 * time.Minute)}, "active", "●", "● On"},
		{"failed", &watcher.State{Result: "failed", Detail: "unit is failed after restart", RestartedAt: time.Now().Add(-3 * time.Minute)}, "active", "✖", "✖ On · restart 3m ago failed: unit is failed after restart"},
		{"skipped", &watcher.State{Result: "skipped", Detail: "service is inactive; not restarted", ChangedAt: time.Now()}, "active", "●", "● On"},
	}
	for _, c := range cases {
		m.loggerState = c.logger
		m.watch = map[string]watcher.State{}
		if c.state != nil {
			m.watch[k] = *c.state
		}
		if got := plain(m.watchCell(svc)); got != c.cell {
			t.Errorf("%s: cell %q, want %q", c.name, got, c.cell)
		}
		if got := plain(m.watchDetail(svc)); got != c.detail {
			t.Errorf("%s: detail %q, want %q", c.name, got, c.detail)
		}
	}

	m.loggerState, m.watch = "active", map[string]watcher.State{k: {}}
	other := svc
	other.WatchPath = "/srv/app.py"
	if got := plain(m.watchDetail(other)); got != "● On · watches /srv/app.py instead of the Run binary" {
		t.Errorf("watch path override: %q", got)
	}
	unknown := config.Service{Unit: "gone.service", Scope: config.User, Watch: true}
	m.watch[unknown.Key()] = watcher.State{}
	if got := plain(m.watchDetail(unknown)); got != "! On · ExecStart unknown (unit not loaded?)" {
		t.Errorf("unknown ExecStart: %q", got)
	}

	off := svc
	off.Watch = false
	if plain(m.watchCell(off)) != "○" {
		t.Error("unwatched cell")
	}
	if got := plain(m.watchDetail(off)); got != "○ Off" {
		t.Errorf("unwatched detail %q", got)
	}
}

func TestLoggingDetail(t *testing.T) {
	m := &Model{loggerState: "active"}
	on := config.Service{Unit: "hello.service", Scope: config.User}
	off := on
	off.NoRecord = true
	for _, c := range []struct {
		svc    config.Service
		logger string
		want   string
	}{
		{on, "active", "● On"},
		{on, "not installed", "● Inactive · serviceman daemon is not installed (D turns it on)"},
		{off, "active", "○ Off"},
		{off, "inactive", "○ Off"},
	} {
		m.loggerState = c.logger
		if got := ansi.Strip(m.loggingDetail(c.svc)); got != c.want {
			t.Errorf("NoRecord=%v daemon %q: %q, want %q", c.svc.NoRecord, c.logger, got, c.want)
		}
	}
}

func TestCirclesWhenDaemonOff(t *testing.T) {
	// Tests have no terminal, so force colors or both circles render alike.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)
	if sGreen.Render("● On") == sDim.Render("● On") {
		t.Fatal("green and gray render the same; the test can't tell them apart")
	}
	svc := config.Service{Unit: "hello.service", Scope: config.User, Watch: true}
	m := &Model{
		loggerState: "active",
		status:      map[string]systemd.Status{svc.Key(): {ExecPath: "/srv/bin/hello"}},
		watch:       map[string]watcher.State{svc.Key(): {}},
	}
	if got := m.watchDetail(svc); got != sGreen.Render("● On") {
		t.Errorf("watching, daemon active: %q, want green", got)
	}
	if got := m.loggingDetail(svc); got != sGreen.Render("● On") {
		t.Errorf("logging, daemon active: %q, want green", got)
	}
	m.loggerState = "inactive"
	for name, got := range map[string]string{"watching": m.watchDetail(svc), "logging": m.loggingDetail(svc)} {
		if want := sDim.Render("● Inactive") + m.daemonOffNote(); got != want {
			t.Errorf("%s, daemon off: %q, want gray Inactive + note", name, got)
		}
	}
}

func TestPagerHeader(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	p := newPager("Journal", "hello.service")
	p.live = true
	p.setLines([]string{"a", "b", "c"})
	p.view(100, 10)
	h := p.header(100)
	if !strings.Contains(h, sGreen.Render("● Live")) {
		t.Errorf("live journal header lacks a green ● Live: %q", ansi.Strip(h))
	}
	for _, want := range []string{"SERVICEMAN", "Journal", "hello.service", "Follow (f): on", "Wrap (w): on", "Lines 1–3 of 3", "All"} {
		if !strings.Contains(ansi.Strip(h), want) {
			t.Errorf("header %q lacks %q", ansi.Strip(h), want)
		}
	}
	if w := ansi.StringWidth(strings.Split(h, "\n")[0]); w != 100 {
		t.Errorf("header is %d wide, want 100", w)
	}

	p.live, p.ended = false, "stream ended"
	if h := ansi.Strip(p.header(80)); !strings.Contains(h, "○ Ended") || strings.Contains(h, "Live") {
		t.Errorf("ended journal header %q", h)
	}

	help := newPager("Keybindings", "")
	help.setLines([]string{"x"})
	help.view(100, 10)
	if h := ansi.Strip(help.header(80)); strings.Contains(h, "Follow") || strings.Contains(h, "Live") {
		t.Errorf("help header shows journal-only state: %q", h)
	}
}

func TestAppConfigDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	svc := config.Service{Unit: "myapp.service", Scope: config.User}
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{svc.Key(): {ExecPath: "/home/u/.local/bin/myapp"}}}
	if p, def := m.appConfig(svc); p != "" || def {
		t.Fatalf("no config yet: %q %v", p, def)
	}
	os.MkdirAll(config.AppDir("myapp"), 0o755)
	want := filepath.Join(config.AppDir("myapp"), "config.toml")
	os.WriteFile(want, nil, 0o644)
	if p, def := m.appConfig(svc); p != want || !def {
		t.Fatalf("default: %q %v, want %q", p, def, want)
	}
	svc.ConfigPath = "/etc/myapp.toml" // set with C: wins over the default
	if p, def := m.appConfig(svc); p != "/etc/myapp.toml" || def {
		t.Fatalf("explicit: %q %v", p, def)
	}
}

// The TUI uses the terminal's ANSI palette, so the terminal's theme decides
// the actual colors: styles emit basic SGR codes, never 24-bit hex colors.
func TestTerminalPalette(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)
	for name, c := range map[string]struct {
		got, sgr string
	}{
		"accent": {sKey.Render("x"), "36"},
		"green":  {sGreen.Render("x"), "32"},
		"red":    {sRed.Render("x"), "31"},
		"yellow": {sYellow.Render("x"), "33"},
		"dim":    {sDim.Render("x"), "97"},
	} {
		if strings.Contains(c.got, "38;2;") {
			t.Errorf("%s uses a 24-bit color: %q", name, c.got)
		}
		if !strings.Contains(c.got, c.sgr+"m") && !strings.Contains(c.got, ";"+c.sgr) && !strings.Contains(c.got, "["+c.sgr) {
			t.Errorf("%s = %q, want ANSI SGR %s", name, c.got, c.sgr)
		}
	}
	// Only descriptions are italic.
	italic := func(s string) bool {
		return strings.Contains(s, "\x1b[3;") || strings.Contains(s, "\x1b[3m") || strings.Contains(s, ";3;") || strings.Contains(s, ";3m")
	}
	for name, st := range map[string]lipgloss.Style{"desc": sDesc, "selected desc": sDescSel} {
		if got := st.Render("x"); !italic(got) {
			t.Errorf("%s = %q, want italics", name, got)
		}
	}
	for name, st := range map[string]lipgloss.Style{"dim": sDim, "header": sHeader, "accent": sKey, "title": sTitle} {
		if got := st.Render("x"); italic(got) {
			t.Errorf("%s = %q, want no italics", name, got)
		}
	}
	// Normal text keeps the terminal's default foreground.
	if got := sDescSel.Render("x"); got != "\x1b[3mx\x1b[0m" {
		t.Errorf("selected desc = %q, want no foreground color", got)
	}
}

// A selector's chosen option is green like its ✓; the top bar sits on a
// heavy rule.
func TestChosenOptionAndTopBarRule(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 100, h: 60}
	m.openForm(nil)
	f := m.form.get("watch")
	if f.choices == nil {
		t.Fatal("watch field is not a selector")
	}
	view := m.View()
	if want := sChosen.Render(choiceLabel(f.value())) + " " + sGreen.Render("✓"); !strings.Contains(view, want) {
		t.Errorf("view lacks the green chosen option %q", want)
	}
	if lines := strings.Split(ansi.Strip(view), "\n"); !strings.HasPrefix(lines[1], gutter+"━") {
		t.Errorf("line under the top bar = %q, want a heavy rule", lines[1])
	}
}
