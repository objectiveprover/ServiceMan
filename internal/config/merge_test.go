package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestUpdateFromTemplate(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(cfg, []byte(`{"editor":"vim","services":[{"unit":"a.service"}],"custom":1,"nested":{"x":1}}`), 0o600)
	tmpl := []byte(`{"version":2,"editor":"","bin_dir":"~/.local/bin","services":[],"nested":{"x":2,"y":3}}`)

	res, err := UpdateFromTemplate(cfg, tmpl)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bin_dir", "nested.y", "version"}; !reflect.DeepEqual(res.Added, want) {
		t.Errorf("added %v, want %v", res.Added, want)
	}
	if res.OldVersion != 0 || res.NewVersion != 2 {
		t.Errorf("version %d → %d, want 0 → 2", res.OldVersion, res.NewVersion)
	}
	var got map[string]any
	b, _ := os.ReadFile(cfg)
	json.Unmarshal(b, &got)
	want := map[string]any{
		"version":  float64(2),
		"editor":   "vim",                                      // existing value kept
		"services": []any{map[string]any{"unit": "a.service"}}, // array kept
		"custom":   float64(1),                                 // field not in the template kept
		"nested":   map[string]any{"x": float64(1), "y": float64(3)},
		"bin_dir":  "~/.local/bin",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged config %v, want %v", got, want)
	}
	if fi, _ := os.Stat(cfg); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want the original 0600", fi.Mode().Perm())
	}

	// Nothing missing: the file isn't rewritten.
	before, _ := os.Stat(cfg)
	if res, err := UpdateFromTemplate(cfg, tmpl); err != nil || len(res.Added) != 0 || res.NewVersion != 2 {
		t.Fatalf("second update: %+v %v", res, err)
	}
	if after, _ := os.Stat(cfg); !after.ModTime().Equal(before.ModTime()) {
		t.Error("file rewritten although nothing changed")
	}

	// An older version is raised even when no field is missing (a setting
	// was removed); existing fields still stay.
	os.WriteFile(cfg, []byte(`{"version":1,"editor":"vim","old_setting":true}`), 0o644)
	res, _ = UpdateFromTemplate(cfg, []byte(`{"version":2,"editor":""}`))
	if len(res.Added) != 0 || res.OldVersion != 1 || res.NewVersion != 2 {
		t.Errorf("version-only update: %+v", res)
	}
	if v, _ := FileVersion(cfg); v != 2 {
		t.Errorf("FileVersion = %d, want 2", v)
	}
	// A newer config is never downgraded.
	os.WriteFile(cfg, []byte(`{"version":5}`), 0o644)
	if res, _ := UpdateFromTemplate(cfg, []byte(`{"version":2}`)); res.NewVersion != 5 {
		t.Errorf("downgraded to %d", res.NewVersion)
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

func TestDefaultAppConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if p := DefaultAppConfig("myapp"); p != "" {
		t.Fatalf("no dir: %q", p)
	}
	dir := AppDir("myapp")
	os.MkdirAll(dir, 0o755)
	for _, f := range []string{"config.toml.20261117-103000.bak", "config.example.toml", ".env"} {
		os.WriteFile(filepath.Join(dir, f), nil, 0o644)
	}
	if p := DefaultAppConfig("myapp"); p != "" {
		t.Fatalf("picked a backup/example: %q", p)
	}
	os.WriteFile(filepath.Join(dir, "config.toml"), nil, 0o644)
	if p := DefaultAppConfig("myapp"); p != filepath.Join(dir, "config.toml") {
		t.Fatalf("got %q", p)
	}
}
