package config

import "testing"

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

func TestBinaryName(t *testing.T) {
	for in, want := range map[string]string{
		"":                       "",
		"/":                      "",
		"myapp":                  "myapp",
		"/home/u/.local/bin/app": "app",
	} {
		if got := BinaryName(in); got != want {
			t.Errorf("BinaryName(%q) = %q, want %q", in, got, want)
		}
	}
	if DefaultAppConfig("") != "" || DefaultAppConfig("a/b") != "" {
		t.Error("DefaultAppConfig accepts only a bare binary name")
	}
}

func TestTildeEnvFile(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for in, want := range map[string]string{
		"/home/u/.secrets/a.env":  "~/.secrets/a.env",
		"-/home/u/.secrets/a.env": "-~/.secrets/a.env",
		"-/etc/a.env":             "-/etc/a.env",
	} {
		if got := TildeEnvFile(in); got != want {
			t.Errorf("TildeEnvFile(%q) = %q, want %q", in, got, want)
		}
		if got := ExpandEnvFile(TildeEnvFile(in)); got != in {
			t.Errorf("ExpandEnvFile(TildeEnvFile(%q)) = %q", in, got)
		}
	}
}
