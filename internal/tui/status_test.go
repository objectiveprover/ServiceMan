package tui

import "testing"

// Without the ServiceMan daemon's unit file, its state is "not installed"
// and no systemctl is run for it.
func TestFetchStatusDaemonNotInstalled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	st, daemonState, legacy, err := fetchStatus(nil)
	if daemonState != "not installed" || legacy || err != nil || len(st) != 0 {
		t.Fatalf("fetchStatus(nil) = %v, %q, %v, %v", st, daemonState, legacy, err)
	}
}
