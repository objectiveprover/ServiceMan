// Package config holds the list of tracked services and serviceman settings.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Scope string

const (
	System Scope = "system"
	User   Scope = "user"
)

// Service is a systemd unit tracked by serviceman.
type Service struct {
	Unit  string `json:"unit"`
	Scope Scope  `json:"scope"`
	// URL is an optional HTTP health-check endpoint (handy for web services).
	URL string `json:"url,omitempty"`
	// NoRecord disables log recording by the logger daemon for this unit.
	NoRecord bool `json:"no_record,omitempty"`
	// Watch makes the daemon restart the unit when its binary changes.
	Watch bool `json:"watch,omitempty"`
	// WatchPath overrides the watched file (default: ExecStart's executable).
	WatchPath string `json:"watch_path,omitempty"`
	// ConfigPath is the application's own config file (serviceman metadata only).
	ConfigPath string `json:"config_path,omitempty"`
}

type Config struct {
	// Editor is the command used to edit unit files. Empty = auto
	// (emacsclient -t if an Emacs server is running, else emacs -nw).
	Editor string `json:"editor,omitempty"`
	LogDir string `json:"log_dir,omitempty"`
	// BinDir is where deployable binaries live (the TUI's bin picker).
	BinDir       string    `json:"bin_dir,omitempty"`
	MaxLogSizeMB int       `json:"max_log_size_mb,omitempty"`
	MaxLogFiles  int       `json:"max_log_files,omitempty"`
	Services     []Service `json:"services"`
}

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

func Dir() string      { return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "serviceman") }
func Path() string     { return filepath.Join(Dir(), "config.json") }
func StateDir() string { return filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "serviceman") }
func DataDir() string  { return filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "serviceman") }

// WatchStatePath is where the daemon publishes binary-watch results.
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

// Load reads the config, returning defaults if it does not exist yet.
func Load() (*Config, error) {
	c := &Config{}
	b, err := os.ReadFile(Path())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, fmt.Errorf("parse %s: %w", Path(), err)
		}
	}
	c.applyDefaults()
	return c, nil
}

func (c *Config) applyDefaults() {
	if c.LogDir == "" {
		c.LogDir = filepath.Join(DataDir(), "logs")
	}
	if c.BinDir == "" {
		home, _ := os.UserHomeDir()
		c.BinDir = filepath.Join(home, ".local", "bin")
	}
	c.BinDir = ExpandTilde(c.BinDir)
	if c.MaxLogSizeMB <= 0 {
		c.MaxLogSizeMB = 20
	}
	if c.MaxLogFiles <= 0 {
		c.MaxLogFiles = 5
	}
	for i := range c.Services {
		if c.Services[i].Scope == "" {
			c.Services[i].Scope = System
		}
	}
}

// Save writes the config atomically.
func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

// Update performs load-modify-save so concurrent CLI/TUI edits don't clobber
// each other.
func Update(fn func(*Config)) (*Config, error) {
	c, err := Load()
	if err != nil {
		return nil, err
	}
	fn(c)
	return c, c.Save()
}

func (c *Config) Index(unit string, scope Scope) int {
	for i, s := range c.Services {
		if s.Unit == unit && s.Scope == scope {
			return i
		}
	}
	return -1
}

func (c *Config) Track(s Service) bool {
	if c.Index(s.Unit, s.Scope) >= 0 {
		return false
	}
	c.Services = append(c.Services, s)
	return true
}

func (c *Config) Untrack(unit string, scope Scope) bool {
	i := c.Index(unit, scope)
	if i < 0 {
		return false
	}
	c.Services = append(c.Services[:i], c.Services[i+1:]...)
	return true
}

// LogPath is where the logger daemon records a unit's logs.
func (c *Config) LogPath(s Service) string {
	name := s.Unit + ".log"
	if s.Scope == User {
		name = "user--" + name
	}
	return filepath.Join(c.LogDir, name)
}

// NormalizeUnit appends ".service" when no unit suffix is given.
func NormalizeUnit(name string) string {
	name = strings.TrimSpace(name)
	for _, suf := range []string{".service", ".socket", ".timer", ".target", ".path", ".mount"} {
		if strings.HasSuffix(name, suf) {
			return name
		}
	}
	return name + ".service"
}

// ValidUnitName rejects names that systemd would not accept.
func ValidUnitName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(":-_.\\@", r)) {
			return false
		}
	}
	return true
}
