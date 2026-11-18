package tui

import (
	"path/filepath"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

// watchedPath is the file watching follows for s ("" = unknown yet).
func (m *Model) watchedPath(s config.Service) string { return watchedPathIn(m.status, s) }

// watchedPathIn is watchedPath with the statuses given, for code that runs
// off the UI goroutine.
func watchedPathIn(status map[string]systemd.Status, s config.Service) string {
	if s.WatchPath != "" {
		return config.ExpandTilde(s.WatchPath)
	}
	return status[s.Key()].ExecPath
}

// appConfig is the service's own config file: the one set with C, or else
// the convention's ~/.config/<binary>/config.* when it exists (isDefault).
func (m *Model) appConfig(s config.Service) (path string, isDefault bool) {
	if s.ConfigPath != "" {
		return s.ConfigPath, false
	}
	if bin := m.watchedPath(s); bin != "" {
		if p := config.DefaultAppConfig(filepath.Base(bin)); p != "" {
			return p, true
		}
	}
	return "", false
}
