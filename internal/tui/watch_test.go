package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

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
		{"daemon down", nil, "inactive", "!", "serviceman daemon is inactive"},
		{"not picked up", nil, "active", "!", "not watched yet"},
		{"idle", &watcher.State{}, "active", "●", "watching, no changes yet"},
		{"restarted", &watcher.State{Result: "ok", RestartedAt: time.Now().Add(-3 * time.Minute)}, "active", "●", "auto-restarted 3m ago ✓"},
		{"failed", &watcher.State{Result: "failed", Detail: "unit is failed after restart", RestartedAt: time.Now()}, "active", "✖", "failed: unit is failed"},
		{"skipped", &watcher.State{Result: "skipped", Detail: "service is inactive; not restarted", ChangedAt: time.Now()}, "active", "●", "not restarted"},
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
		if got := plain(m.watchDetail(svc)); !strings.Contains(got, c.detail) || !strings.HasPrefix(got, "/srv/bin/hello") {
			t.Errorf("%s: detail %q, want it to contain %q", c.name, got, c.detail)
		}
	}
	off := svc
	off.Watch = false
	if plain(m.watchCell(off)) != "○" {
		t.Error("unwatched cell")
	}
}
