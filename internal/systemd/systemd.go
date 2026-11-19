// Package systemd wraps systemctl/journalctl for ServiceMan.
package systemd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"serviceman/internal/config"
)

// IsRoot reports whether we already have root privileges (no sudo needed).
func IsRoot() bool { return os.Geteuid() == 0 }

func scopeArgs(scope config.Scope) []string {
	if scope == config.User {
		return []string{"--user"}
	}
	return nil
}

// Systemctl builds an unprivileged systemctl command for the scope.
func Systemctl(scope config.Scope, args ...string) *exec.Cmd {
	return exec.Command("systemctl", append(scopeArgs(scope), args...)...)
}

func run(cmd *exec.Cmd) (string, error) {
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		if msg != "" {
			err = fmt.Errorf("%s", msg)
		}
	}
	return out.String(), err
}

// ShellScript returns a `sh -c` command. On failure it pauses so the user can
// read the output before the TUI comes back.
func ShellScript(script string, env ...string) *exec.Cmd {
	// The script runs in its own `sh -e` (errexit is ignored inside `a || b`).
	wrapper := `sh -ec "$SERVICEMAN_SCRIPT"; rc=$?
if [ $rc -ne 0 ] && [ $rc -ne 3 ]; then echo; printf 'serviceman: command failed (exit %s) — press Enter to return ' $rc; read _; fi
exit $rc`
	cmd := exec.Command("sh", "-c", wrapper)
	cmd.Env = append(append(os.Environ(), env...), "SERVICEMAN_SCRIPT="+script)
	return cmd
}

func sudo() string {
	if IsRoot() {
		return ""
	}
	return "sudo "
}

func ctl(scope config.Scope) string {
	if scope == config.User {
		return "systemctl --user"
	}
	return sudo() + "systemctl"
}

// UserUnitDir is where user unit files live: the only ones ServiceMan
// creates or edits (system units can only be tracked).
func UserUnitDir() string { return filepath.Join(config.ConfigHome(), "systemd", "user") }

// IsVendorPath reports whether a unit file belongs to the distro/packages,
// in which case it should be customised with a drop-in instead.
func IsVendorPath(p string) bool {
	return strings.HasPrefix(p, "/usr/lib/") || strings.HasPrefix(p, "/lib/") ||
		strings.HasPrefix(p, "/usr/share/") || strings.HasPrefix(p, "/run/")
}
