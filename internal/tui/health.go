package tui

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"serviceman/internal/config"
)

type healthTickMsg struct{}

type healthMsg map[string]healthResult

type healthResult struct {
	code int
	dur  time.Duration
	err  error
	// note replaces the check for an "auto" URL that has nothing to check
	// yet: "no port" (the server hasn't picked one) or "no info" (its binary
	// doesn't answer serviceman-info).
	note string
}

func healthTick() tea.Cmd {
	return tea.Tick(10*time.Second, func(time.Time) tea.Msg { return healthTickMsg{} })
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
	// m.status is replaced on every refresh, never changed in place, so the
	// goroutine can read it.
	status, cache := m.status, m.infoCache
	return func() tea.Msg {
		client := &http.Client{Timeout: 4 * time.Second}
		res := healthMsg{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		for _, s := range svcs {
			wg.Add(1)
			go func(s config.Service) {
				defer wg.Done()
				r := healthResult{}
				url := s.URL
				if url == config.AutoURL {
					bin := status[s.Key()].ExecPath
					if bin == "" {
						return // unit not loaded yet: the next check asks
					}
					if info, err := cache.Query(bin); err != nil {
						r.note = "no info"
					} else if url = info.HealthURL(); url == "" {
						r.note = "no port"
					}
					if r.note != "" {
						mu.Lock()
						res[s.Key()] = r
						mu.Unlock()
						return
					}
				}
				start := time.Now()
				resp, err := client.Get(url)
				r.dur = time.Since(start)
				if err != nil {
					r.err = err
				} else {
					r.code = resp.StatusCode
					resp.Body.Close()
				}
				mu.Lock()
				res[s.Key()] = r
				mu.Unlock()
			}(s)
		}
		wg.Wait()
		return res
	}
}

func (m *Model) healthCell(s config.Service) string {
	if s.URL == "" {
		return sDim.Render("-")
	}
	r, ok := m.health[s.Key()]
	if !ok {
		return sDim.Render("…")
	}
	if r.note != "" {
		return sDim.Render(r.note)
	}
	if r.err != nil {
		return sRed.Render("✖ down")
	}
	txt := fmt.Sprintf("%d %dms", r.code, r.dur.Milliseconds())
	switch {
	case r.code < 400:
		return sGreen.Render(txt)
	case r.code < 500:
		return sYellow.Render(txt)
	default:
		return sRed.Render(txt)
	}
}
