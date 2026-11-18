package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

// A tracked service is found for its binary by path, and through a symlink
// in the bin dir; the scan stores it so drawing needs no file lookups.
func TestBinScanLinksServices(t *testing.T) {
	m := binPickerModel(t, "direct", "real", "other")
	dir := m.cfg.BinDir
	os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "linked"))
	direct := config.Service{Unit: "direct.service", Scope: config.User}
	viaLink := config.Service{Unit: "real.service", Scope: config.User}
	m.cfg.Services = []config.Service{direct, viaLink}
	m.status = map[string]systemd.Status{
		direct.Key():  {ExecPath: filepath.Join(dir, "direct")},
		viaLink.Key(): {ExecPath: filepath.Join(dir, "real")},
	}
	m.bins.load(m)
	got := map[string]string{}
	for _, e := range m.bins.all {
		if e.tracked {
			got[e.name] = e.svc.Unit
		}
	}
	want := map[string]string{"direct": "direct.service", "real": "real.service", "linked": "real.service"}
	if len(got) != len(want) {
		t.Fatalf("tracked = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: service %q, want %q", k, got[k], v)
		}
	}
}

// The periodic rescan runs in the background, one at a time, and a result
// for a picker that was closed meanwhile is dropped.
func TestBinReloadInBackground(t *testing.T) {
	m := binPickerModel(t, "alpha")
	p := m.bins
	cmd := m.update(statusMsg{seq: 1})
	if cmd == nil || !p.reloading {
		t.Fatal("a status refresh didn't start a background rescan")
	}
	if m.update(statusMsg{seq: 2}) != nil {
		t.Error("a second rescan started while one was running")
	}
	os.WriteFile(filepath.Join(m.cfg.BinDir, "beta"), []byte("#!/bin/sh\n"), 0o755)
	m.update(cmd())
	if p.reloading || len(p.all) != 2 {
		t.Fatalf("after the rescan: reloading=%v, %d binaries, want 2", p.reloading, len(p.all))
	}

	cmd = p.reloadCmd(m)
	m.bins = nil // closed
	m.update(cmd())
	if len(p.all) != 2 || !p.reloading {
		t.Error("a closed picker took a rescan's result")
	}
}

// An older status refresh that finishes after a newer one is dropped, and
// no refresh is started while one is still running.
func TestStatusRefreshOrder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}}
	m.refreshCmd()
	m.refreshCmd()
	newer := map[string]systemd.Status{"user/a.service": {ActiveState: "active"}}
	m.update(statusMsg{seq: 2, status: newer, daemon: "active"})
	m.update(statusMsg{seq: 1, status: map[string]systemd.Status{}, daemon: "inactive"})
	if m.status["user/a.service"].ActiveState != "active" || m.daemonState != "active" {
		t.Fatalf("an older refresh replaced a newer one: %v, daemon %q", m.status, m.daemonState)
	}

	m.refreshCmd() // still running
	m.update(tickMsg{})
	if m.refreshSeq != 3 {
		t.Errorf("a tick started a refresh while one was running (refreshSeq %d)", m.refreshSeq)
	}
	m.update(statusMsg{seq: 3})
	m.update(tickMsg{})
	if m.refreshSeq != 4 {
		t.Errorf("a tick after the refresh finished didn't start one (refreshSeq %d)", m.refreshSeq)
	}
}

// Status opens the viewer at once and fills it when the command is done.
func TestShowStaticLoadsInBackground(t *testing.T) {
	m := &Model{cfg: &config.Config{}, w: 80, h: 20}
	cmd := m.showStatic("Status", "x", exec.Command("printf", "one\ntwo\n"))
	if m.mode != modePager || !m.pager.loading {
		t.Fatal("the viewer didn't open before the command ran")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Loading…") {
		t.Errorf("no Loading… while the command runs:\n%s", v)
	}
	m.update(cmd())
	if m.pager.loading || strings.Join(m.pager.lines, ",") != "one,two" {
		t.Fatalf("loading=%v lines=%q", m.pager.loading, m.pager.lines)
	}
}

// Track Services opens with the units listed before, lists them again in the
// background, and keeps the selected unit selected when the list changes.
func TestPickerUsesKeptUnits(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}}
	m.units = &pickerLoadedMsg{entries: []systemd.UnitEntry{
		{Unit: "b.service", Scope: config.User}, {Unit: "c.service", Scope: config.User},
	}}
	m.unitsLoading = true // a listing is already running: don't start another
	if cmd := m.openPicker(); cmd != nil {
		t.Error("opening started a second listing")
	}
	p := m.picker
	if p.loading || len(p.shown) != 2 {
		t.Fatalf("the kept units weren't shown at once: loading=%v shown=%d", p.loading, len(p.shown))
	}
	p.focusList()
	p.cursor = 1 // c.service
	m.update(pickerLoadedMsg{entries: []systemd.UnitEntry{
		{Unit: "a.service", Scope: config.User}, {Unit: "b.service", Scope: config.User}, {Unit: "c.service", Scope: config.User},
	}})
	if got := p.all[p.shown[p.cursor]].Unit; got != "c.service" {
		t.Errorf("selected %s after the new list, want c.service", got)
	}
	if m.unitsLoading || len(m.units.entries) != 3 {
		t.Error("the new list wasn't kept")
	}
}
