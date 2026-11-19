// Package config holds ServiceMan's settings and tracked services
// (config.go, merge.go), where its files live (paths.go), and the
// conventions for the services it manages (conventions.go).
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

// Service is a systemd unit tracked by ServiceMan.
type Service struct {
	Unit  string `json:"unit"`
	Scope Scope  `json:"scope"`
	// URL is an optional HTTP health-check endpoint (handy for web services).
	URL string `json:"url,omitempty"`
	// NoRecord turns logging off for this unit (the daemon stops writing its log file).
	NoRecord bool `json:"no_record,omitempty"`
	// Watch turns on watching: the daemon restarts the unit when its binary changes.
	Watch bool `json:"watch,omitempty"`
	// WatchPath overrides the watched file (default: ExecStart's executable).
	WatchPath string `json:"watch_path,omitempty"`
	// ConfigPath is the application's own config file (ServiceMan metadata only).
	ConfigPath string `json:"config_path,omitempty"`
}

// CurrentVersion is the config format version this build expects. Bump it
// whenever a setting is added or removed (and in config.template.json).
const CurrentVersion = 1

type Config struct {
	// Version is the config format version the file was written for;
	// `serviceman config version` compares it with CurrentVersion.
	Version int `json:"version"`
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

// NormalizeURL adds "http://" to a URL without a scheme.
func NormalizeURL(u string) string {
	if u != "" && !strings.Contains(u, "://") {
		return "http://" + u
	}
	return u
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
	} else {
		c.Version = CurrentVersion // a new file starts at the current format
	}
	c.applyDefaults()
	return c, nil
}

func (c *Config) applyDefaults() {
	if c.LogDir == "" {
		c.LogDir = filepath.Join(DataDir(), "logs")
	}
	c.LogDir = ExpandTilde(c.LogDir)
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

// Key identifies a tracked service: "<scope>/<unit>".
func (s Service) Key() string { return string(s.Scope) + "/" + s.Unit }

// Find returns the tracked service, or nil when it isn't tracked.
func (c *Config) Find(unit string, scope Scope) *Service {
	if i := c.Index(unit, scope); i >= 0 {
		return &c.Services[i]
	}
	return nil
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

// LogPath is the log file the daemon writes for a unit.
func (c *Config) LogPath(s Service) string {
	name := s.Unit + ".log"
	if s.Scope == User {
		name = "user--" + name
	}
	return filepath.Join(c.LogDir, name)
}
