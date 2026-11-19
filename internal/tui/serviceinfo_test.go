package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
	"serviceman/internal/serviceinfo"
	"serviceman/internal/systemd"
)

// fakeServer writes a binary with a serviceman-info command that reports the
// port in the file next to it ("null" until it's written).
func fakeServer(t *testing.T) (bin, portFile string) {
	t.Helper()
	dir := t.TempDir()
	bin, portFile = filepath.Join(dir, "shelf"), filepath.Join(dir, "port")
	os.WriteFile(portFile, []byte("null"), 0o644)
	os.WriteFile(bin, []byte(`#!/bin/sh
[ "$1" = serviceman-info ] || exit 2
printf '{"serviceman_info":1,"caddyman_info":1,"name":"shelf","description":"Shelf server","args":["serve"],"port":%s,"health_path":"/health","config_path":"","env_file":null,"proxy":{}}' "$(cat `+portFile+`)"
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
		t.Fatal("no serviceman-info query")
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

// The New Service picker asks binaries for their service info in the
// background: a server that answers is listed with the services and marked
// autoconfigurable, and picking it opens a form already filled in. A binary
// that only contains a look-alike name is never run.
func TestBinPickerMarksAutoconfigurable(t *testing.T) {
	m := binPickerModel(t, "alpha", "zeta")
	m.infoCache = &serviceinfo.Cache{}
	srv, portFile := fakeServer(t)
	os.WriteFile(portFile, []byte("20004"), 0o644)
	os.Symlink(srv, filepath.Join(m.cfg.BinDir, "shelf"))
	ran := filepath.Join(t.TempDir(), "ran")
	os.WriteFile(filepath.Join(m.cfg.BinDir, "node"), []byte("#!/bin/sh\n# vnd.3gpp-v2x-local-service-information\ntouch "+ran+"\n"), 0o755)

	p := m.bins
	p.reloading = false
	m.update(p.reloadCmd(m)())
	var order []string
	for _, i := range p.shown {
		order = append(order, p.all[i].name)
	}
	if got := strings.Join(order, " "); got != "shelf alpha node zeta" {
		t.Errorf("order = %q, want the autoconfigurable server first", got)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "service · autoconfigurable — Shelf server") {
		t.Errorf("view lacks the autoconfigurable mark:\n%s", view)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("a binary without serviceman-info was run")
	}

	// Reopened, the picker shows what it learnt at once.
	m.bins = nil
	m.openBinPicker()
	if e := m.bins.all[m.bins.shown[0]]; e.name != "shelf" || e.info == nil {
		t.Errorf("reopened: first %q with info %v", e.name, e.info)
	}

	// Picking it fills the form straight away.
	m.handleKey(keyCN)
	if cmd := m.handleKey(keyEnter); cmd != nil {
		t.Error("the form asked again for an answer the picker had")
	}
	f := m.form
	if f == nil || f.val("url") != "http://localhost:20004/health" || f.val("desc") != "Shelf server" {
		t.Fatalf("form not filled in: %+v", f)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "autoconfigured") {
		t.Errorf("form top bar lacks \"autoconfigured\":\n%s", view)
	}
}

// Tracking an existing unit whose binary describes itself fills in its
// health URL and app config; without a port yet, it waits for one.
func TestTrackFillsFromServiceInfo(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	appCfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(appCfg, []byte("{}"), 0o644)
	port := 20004
	info := &serviceinfo.Info{Version: 1, Port: &port, HealthPath: "/health", ConfigPath: appCfg}
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 120, h: 30}
	m.openPicker()
	m.picker.loaded(pickerLoadedMsg{entries: []systemd.UnitEntry{
		{Unit: "shelf.service", Scope: config.User},
		{Unit: "late.service", Scope: config.User},
		{Unit: "plain.service", Scope: config.User},
	}})
	m.update(unitInfoMsg{
		"user/shelf.service": {"/bin/shelf", info},
		"user/late.service":  {"/bin/late", &serviceinfo.Info{Version: 1, HealthPath: "/"}},
	})
	if view := ansi.Strip(m.View()); strings.Count(view, "autoconfigurable") != 2 {
		t.Errorf("want 2 units marked autoconfigurable:\n%s", view)
	}
	m.handleKey(keyCN)
	m.handleKey(keyEnter)
	s := m.cfg.Find("shelf.service", config.User)
	if s == nil || s.URL != "http://localhost:20004/health" || s.ConfigPath != config.Tilde(appCfg) {
		t.Fatalf("tracked = %+v", s)
	}
	if cmd := m.handleKey(keyEnter); cmd == nil {
		t.Error("tracking a server without a port doesn't wait for one")
	}
	if s := m.cfg.Find("late.service", config.User); s == nil || s.URL != "" {
		t.Errorf("late = %+v", s)
	}
	m.handleKey(keyEnter)
	if s := m.cfg.Find("plain.service", config.User); s == nil || s.URL != "" || s.ConfigPath != "" {
		t.Errorf("plain = %+v", s)
	}
}

func TestDistroPath(t *testing.T) {
	for p, want := range map[string]bool{"/usr/bin/pipewire": true, "/usr/local/bin/app": false, "/home/me/.local/bin/app": false, "/bin/sh": true, "/opt/app/app": false} {
		if distroPath(p) != want {
			t.Errorf("distroPath(%q) = %v", p, !want)
		}
	}
}
