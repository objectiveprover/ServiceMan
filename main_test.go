package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A config that doesn't parse is an error, not a nil-pointer panic.
func TestPathsBadConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	os.MkdirAll(filepath.Join(dir, "serviceman"), 0o755)
	os.WriteFile(filepath.Join(dir, "serviceman", "config.json"), []byte("{not json"), 0o644)
	if err := paths(); err == nil {
		t.Fatal("paths() with a broken config: no error")
	}
}
