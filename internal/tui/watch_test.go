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
	k := svcKey(svc)
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
	m.watch[svcKey(unknown)] = watcher.State{}
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
		status:      map[string]systemd.Status{svcKey(svc): {ExecPath: "/srv/bin/hello"}},
		watch:       map[string]watcher.State{svcKey(svc): {}},
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

	p := newPager("JOURNAL", "hello.service")
	p.live = true
	p.setLines([]string{"a", "b", "c"})
	p.view(80, 10)
	h := p.header(80)
	if !strings.Contains(h, sGreen.Render("● Live")) {
		t.Errorf("live journal header lacks a green ● Live: %q", ansi.Strip(h))
	}
	for _, want := range []string{"JOURNAL", "hello.service", "Follow: on", "Wrap: on", "Lines 1–3 of 3", "All"} {
		if !strings.Contains(ansi.Strip(h), want) {
			t.Errorf("header %q lacks %q", ansi.Strip(h), want)
		}
	}
	if w := ansi.StringWidth(h); w != 80 {
		t.Errorf("header is %d wide, want 80", w)
	}

	p.live, p.ended = false, "stream ended"
	if h := ansi.Strip(p.header(80)); !strings.Contains(h, "○ Ended") || strings.Contains(h, "Live") {
		t.Errorf("ended journal header %q", h)
	}

	help := newPager("HELP", "")
	help.setLines([]string{"x"})
	help.view(80, 10)
	if h := ansi.Strip(help.header(80)); strings.Contains(h, "Follow") || strings.Contains(h, "Live") {
		t.Errorf("help header shows journal-only state: %q", h)
	}
}

func TestAppConfigDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	svc := config.Service{Unit: "myapp.service", Scope: config.User}
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{svcKey(svc): {ExecPath: "/home/u/.local/bin/myapp"}}}
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
