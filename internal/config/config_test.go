package config

import (
	"encoding/json"
	"os"
	"testing"
)

func TestServiceKeyAndFind(t *testing.T) {
	c := &Config{}
	c.Track(Service{Unit: "a.service", Scope: User})
	c.Track(Service{Unit: "a.service", Scope: System})
	if k := c.Services[0].Key(); k != "user/a.service" {
		t.Errorf("Key = %q", k)
	}
	s := c.Find("a.service", System)
	if s == nil || s.Scope != System {
		t.Fatalf("Find = %+v", s)
	}
	s.URL = "http://x" // Find points into the config
	if c.Services[1].URL != "http://x" || c.Services[0].URL != "" {
		t.Errorf("Find didn't return the tracked entry: %+v", c.Services)
	}
	if c.Find("b.service", User) != nil {
		t.Error("Find of an untracked unit isn't nil")
	}
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "",
		"localhost:8080/health": "http://localhost:8080/health",
		"https://example.org":   "https://example.org",
	} {
		if got := NormalizeURL(in); got != want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTemplateMatchesDefaults(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_DATA_HOME", "")
	b, err := os.ReadFile("../../config.template.json")
	if err != nil {
		t.Fatal(err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("template is not a valid config: %v", err)
	}
	if c.Version != CurrentVersion {
		t.Errorf("template version %d, CurrentVersion %d: bump both together", c.Version, CurrentVersion)
	}
	c.applyDefaults()
	d := Config{}
	d.applyDefaults()
	if c.LogDir != d.LogDir || c.BinDir != d.BinDir || c.MaxLogSizeMB != d.MaxLogSizeMB || c.MaxLogFiles != d.MaxLogFiles {
		t.Errorf("template %+v differs from the built-in defaults %+v", c, d)
	}
}
