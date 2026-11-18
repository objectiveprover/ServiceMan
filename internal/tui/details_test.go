package tui

import (
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
		daemonState: "active",
	}
	plain := func(s string) string { return ansi.Strip(s) }
	cases := []struct {
		name   string
		state  *watcher.State
		daemon string
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
		m.daemonState = c.daemon
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

	m.daemonState, m.watch = "active", map[string]watcher.State{k: {}}
	other := svc
	other.WatchPath = "/srv/app.py"
	if got := plain(m.watchDetail(other)); got != "● On · watches /srv/app.py instead of the Run binary" {
		t.Errorf("watch path override: %q", got)
	}
	unknown := config.Service{Unit: "gone.service", Scope: config.User, Watch: true}
	m.watch[unknown.Key()] = watcher.State{}
	if got := plain(m.watchDetail(unknown)); got != "! On · Run binary unknown (unit not loaded?)" {
		t.Errorf("unknown Run binary: %q", got)
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
	m := &Model{daemonState: "active"}
	on := config.Service{Unit: "hello.service", Scope: config.User}
	off := on
	off.NoRecord = true
	for _, c := range []struct {
		svc    config.Service
		daemon string
		want   string
	}{
		{on, "active", "● On"},
		{on, "not installed", "● Inactive · serviceman daemon is not installed (D turns it on)"},
		{off, "active", "○ Off"},
		{off, "inactive", "○ Off"},
	} {
		m.daemonState = c.daemon
		if got := ansi.Strip(m.loggingDetail(c.svc)); got != c.want {
			t.Errorf("NoRecord=%v daemon %q: %q, want %q", c.svc.NoRecord, c.daemon, got, c.want)
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
		daemonState: "active",
		status:      map[string]systemd.Status{svc.Key(): {ExecPath: "/srv/bin/hello"}},
		watch:       map[string]watcher.State{svc.Key(): {}},
	}
	if got := m.watchDetail(svc); got != sGreen.Render("● On") {
		t.Errorf("watching, daemon active: %q, want green", got)
	}
	if got := m.loggingDetail(svc); got != sGreen.Render("● On") {
		t.Errorf("logging, daemon active: %q, want green", got)
	}
	m.daemonState = "inactive"
	for name, got := range map[string]string{"watching": m.watchDetail(svc), "logging": m.loggingDetail(svc)} {
		if want := sDim.Render("● Inactive") + m.daemonOffNote(); got != want {
			t.Errorf("%s, daemon off: %q, want gray Inactive + note", name, got)
		}
	}
}
