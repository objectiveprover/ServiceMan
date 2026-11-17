package logger

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledUnit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if u := InstalledUnit(); u != "" {
		t.Fatalf("nothing installed: got %q", u)
	}
	if st, legacy := State(); st != "not installed" || legacy {
		t.Fatalf("State() = %q, %v", st, legacy)
	}
	os.MkdirAll(filepath.Dir(LegacyUnitPath()), 0o755)
	os.WriteFile(LegacyUnitPath(), nil, 0o644)
	if u := InstalledUnit(); u != LegacyUnitName {
		t.Fatalf("only the old unit: got %q", u)
	}
	os.WriteFile(UnitPath(), nil, 0o644)
	if u := InstalledUnit(); u != UnitName {
		t.Fatalf("both units: got %q, want the new one", u)
	}
}
