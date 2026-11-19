package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Where ServiceMan keeps its files (XDG directories), and "~" helpers.

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

// ConfigHome is $XDG_CONFIG_HOME, or ~/.config.
func ConfigHome() string { return xdg("XDG_CONFIG_HOME", ".config") }

func Dir() string { return filepath.Join(ConfigHome(), "serviceman") }

func Path() string { return filepath.Join(Dir(), "config.json") }

func StateDir() string { return filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "serviceman") }

func DataDir() string { return filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "serviceman") }

// WatchStatePath is where the daemon publishes watching results.
func WatchStatePath() string { return filepath.Join(StateDir(), "watch.json") }

// Tilde abbreviates a path under the home directory with "~".
func Tilde(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || p == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// ExpandTilde turns a leading "~/" into the home directory.
func ExpandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}
