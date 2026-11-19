package systemd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

// OverridePath is the drop-in override file for a user unit.
func OverridePath(unit string) string {
	return filepath.Join(UserUnitDir(), unit+".d", "override.conf")
}

// EditUnitCmd opens a user unit file (or drop-in) in the editor and
// daemon-reloads the user manager afterwards.
func EditUnitCmd(editor, path string) *exec.Cmd {
	return ShellScript(`mkdir -p "$(dirname "$SERVICEMAN_FILE")"
eval "$SERVICEMAN_EDITOR \"\$SERVICEMAN_FILE\""
`+ctl(config.User)+` daemon-reload`, "SERVICEMAN_EDITOR="+editor, "SERVICEMAN_FILE="+path)
}

// EditFileCmd opens a user service's file (e.g. its app config) in the
// editor, as you: ServiceMan never edits files as root. No reload.
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
		umask = "umask 077\n"
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
