package daemon

import (
	"fmt"
	"strings"
	"time"

	"serviceman/internal/systemd"
	"serviceman/internal/watcher"
)

// VerifyDelay is how long after a restart the unit must still be active for
// the restart to count as ok.
var VerifyDelay = 5 * time.Second

// SystemdRestart is the Restarter used by the daemon. Running (or failed)
// units are restarted; units the user stopped are left alone.
func SystemdRestart(t watcher.Target, ctime time.Time) (result, detail string) {
	if t.Self {
		out, err := systemd.Systemctl(t.Scope, "--no-block", "restart", t.Unit).CombinedOutput()
		if err != nil {
			return "error", fmt.Sprintf("%v: %s", err, strings.TrimSpace(string(out)))
		}
		return "ok", "ServiceMan daemon restarting with the new binary"
	}

	m, err := systemd.Show(t.Scope, []string{t.Unit})
	if err != nil {
		return "error", err.Error()
	}
	st := m[t.Unit]
	switch st.ActiveState {
	case "active", "activating", "reloading", "deactivating", "failed":
	default:
		return "skipped", "service is " + orUnknown(st.ActiveState) + "; not restarted"
	}
	// Restarted by hand (or by a deploy script) after the binary landed.
	if st.ActiveState == "active" && !st.Since.IsZero() && st.Since.Unix() > ctime.Unix() {
		return "ok", "already running the new binary"
	}

	quick, _ := systemd.ActionCmds(t.Scope, "restart", t.Unit)
	if needAuth, err := systemd.RunQuick(quick); needAuth {
		return "error", "sudo needs a password; restart it from the TUI (r)"
	} else if err != nil {
		return "error", "restart: " + err.Error()
	}

	time.Sleep(VerifyDelay)
	if s := systemd.IsActive(t.Scope, t.Unit); s != "active" {
		return "failed", "unit is " + orUnknown(s) + " after restart"
	}
	return "ok", ""
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

var _ watcher.Restarter = SystemdRestart
