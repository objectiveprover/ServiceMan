package systemd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"serviceman/internal/config"
)

// ResolveEditor picks the editor command: config value, else emacsclient if
// an Emacs server is running, else plain terminal Emacs.
func ResolveEditor(configured string) string {
	if configured != "" {
		return configured
	}
	if _, err := exec.LookPath("emacsclient"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		if exec.CommandContext(ctx, "emacsclient", "-e", "t").Run() == nil {
			return "emacsclient -t"
		}
	}
	if _, err := exec.LookPath("emacs"); err == nil {
		return "emacs -nw"
	}
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "vi"
}

// OverridePath is the drop-in override file for a unit.
func OverridePath(scope config.Scope, unit string) string {
	return filepath.Join(UnitDir(scope), unit+".d", "override.conf")
}

// needsPrivilege reports whether writing path requires sudo.
func needsPrivilege(scope config.Scope, path string) bool {
	if IsRoot() || scope == config.User {
		return false
	}
	home, _ := os.UserHomeDir()
	return !strings.HasPrefix(path, home+"/")
}

// EditCmd opens path in the editor (via sudoedit when it is root-owned, so
// Emacs runs as you with your own config) and daemon-reloads afterwards.
func EditCmd(scope config.Scope, editor, path string) *exec.Cmd {
	env := []string{"SERVICEMAN_EDITOR=" + editor, "SERVICEMAN_FILE=" + path}
	var s string
	if needsPrivilege(scope, path) {
		s = `sudo mkdir -p "$(dirname "$SERVICEMAN_FILE")"
SUDO_EDITOR="$SERVICEMAN_EDITOR" sudoedit "$SERVICEMAN_FILE"
sudo systemctl daemon-reload`
	} else {
		s = `mkdir -p "$(dirname "$SERVICEMAN_FILE")"
eval "$SERVICEMAN_EDITOR \"\$SERVICEMAN_FILE\""
` + ctl(scope) + ` daemon-reload`
	}
	return ShellScript(s, env...)
}

// EditFileCmd opens an arbitrary file (e.g. an app config) in the editor,
// through sudoedit when it lives outside the home directory. No reload.
func EditFileCmd(editor, path string) *exec.Cmd { return editFileCmd(editor, path, false) }

// EditEnvFileCmd is EditFileCmd for an env file, which holds secrets:
// whatever it creates is private (umask 077). A missing file is created
// empty (0600, its directories 0700) before the editor opens it, so it is
// private even when the editor doesn't run with our umask (an Emacs server),
// and removed again when nothing was saved in it.
func EditEnvFileCmd(editor, path string) *exec.Cmd { return editFileCmd(editor, path, true) }

func editFileCmd(editor, path string, secret bool) *exec.Cmd {
	env := []string{"SERVICEMAN_EDITOR=" + editor, "SERVICEMAN_FILE=" + path}
	umask := ""
	if secret {
		umask = "umask 077\n" // sudo keeps it too (it uses the union of both umasks)
	}
	if needsPrivilege(config.System, path) {
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			f.Close() // writable anyway (e.g. group-owned): no sudo
		} else {
			return ShellScript(umask+`SUDO_EDITOR="$SERVICEMAN_EDITOR" sudoedit "$SERVICEMAN_FILE"`, env...)
		}
	}
	if secret {
		return ShellScript(umask+`new=
trap '[ -z "$new" ] || [ -s "$SERVICEMAN_FILE" ] || rm -f -- "$SERVICEMAN_FILE"' EXIT
if [ ! -e "$SERVICEMAN_FILE" ]; then
  mkdir -p -- "$(dirname -- "$SERVICEMAN_FILE")"
  : > "$SERVICEMAN_FILE"
  new=1
fi
eval "$SERVICEMAN_EDITOR \"\$SERVICEMAN_FILE\""`, env...)
	}
	return ShellScript(`mkdir -p "$(dirname "$SERVICEMAN_FILE")"
eval "$SERVICEMAN_EDITOR \"\$SERVICEMAN_FILE\""`, env...)
}

// ViewCmd opens a (user-readable) file in the editor without reloading.
func ViewCmd(editor, path string) *exec.Cmd {
	return ShellScript(`eval "$SERVICEMAN_EDITOR \"\$SERVICEMAN_FILE\""`, "SERVICEMAN_EDITOR="+editor, "SERVICEMAN_FILE="+path)
}
