package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"serviceman/internal/config"
	"serviceman/internal/serviceinfo"
	"serviceman/internal/systemd"
)

// fakeServer writes a binary with a service-info command that reports the
// port in the file next to it ("null" until it's written).
func fakeServer(t *testing.T) (bin, portFile string) {
	t.Helper()
	dir := t.TempDir()
	bin, portFile = filepath.Join(dir, "shelf"), filepath.Join(dir, "port")
	os.WriteFile(portFile, []byte("null"), 0o644)
	os.WriteFile(bin, []byte(`#!/bin/sh
[ "$1" = service-info ] || exit 2
printf '{"service_info":1,"name":"shelf","description":"Shelf server","args":["serve"],"port":%s,"health_path":"/health","config_path":"","env_file":null,"proxy":{}}' "$(cat `+portFile+`)"
`), 0o755)
	return bin, portFile
}

func TestNewFormFilledFromServiceInfo(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bin, portFile := fakeServer(t)
	os.WriteFile(portFile, []byte("20004"), 0o644)
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 100, h: 30}
	cmd := m.openForm(&formPrefill{name: "shelf", exec: bin, watch: true})
	if cmd == nil {
		t.Fatal("no service-info query")
	}
	f := m.form
	f.get("env").ti.SetValue("A=1") // typed while the binary answers
	m.update(cmd())
	for id, want := range map[string]string{"desc": "Shelf server", "exec": bin + " serve", "url": "http://localhost:20004/health", "env": "A=1"} {
		if got := f.val(id); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
}

func TestServiceInfoKeepsWhatWasTyped(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bin, portFile := fakeServer(t)
	os.WriteFile(portFile, []byte("20004"), 0o644)
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 100, h: 30}
	cmd := m.openForm(&formPrefill{name: "shelf", exec: bin})
	f := m.form
	f.get("desc").ti.SetValue("Mine")
	f.get("url").ti.SetValue("http://localhost:1/x")
	m.update(cmd())
	if f.val("desc") != "Mine" || f.val("url") != "http://localhost:1/x" {
		t.Errorf("typed values replaced: desc %q, url %q", f.val("desc"), f.val("url"))
	}
	// A form closed before the answer arrives is left alone.
	m.form = nil
	m.update(cmd())
}

func TestHealthURLFilledOnceThePortIsPicked(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	bin, portFile := fakeServer(t)
	defer func(w, p time.Duration) { portWait, portPoll = w, p }(portWait, portPoll)
	portWait, portPoll = 2*time.Second, 20*time.Millisecond

	svc := config.Service{Unit: "shelf.service", Scope: config.User}
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 100, h: 30}
	m.updateConfig(func(c *config.Config) { c.Track(svc) })
	info, _ := serviceinfo.Query(bin)
	if info.HealthURL() != "" {
		t.Fatal("port before the first start")
	}
	go func() { time.Sleep(100 * time.Millisecond); os.WriteFile(portFile, []byte("20009"), 0o644) }()
	m.update(waitForPort(svc, bin)())
	if got := m.cfg.Find(svc.Unit, svc.Scope).URL; got != "http://localhost:20009/health" {
		t.Errorf("URL = %q", got)
	}
}
