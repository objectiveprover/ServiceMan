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
