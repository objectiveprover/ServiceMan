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

// userScript runs a script for the user manager. It reads its values from
// env, so they are never parsed by the shell.
func userScript(env []string, s string) *exec.Cmd {
	c := exec.Command("sh", "-ec", s)
	c.Env = append(os.Environ(), env...)
	return c
}

// privCmds builds the commands for a script body that runs systemctl (ctl).
// User units, and root, get only quick, which runs it directly. Otherwise
// quick runs it with sudo -n (never asks), and interactive, the fallback when
// sudo wants a password, says what it's doing (label) and may ask for it.
func privCmds(scope config.Scope, label string, env []string, body func(ctl string) string) (quick, interactive *exec.Cmd) {
	if scope == config.User || IsRoot() {
		return userScript(env, body(ctl(scope))), nil
	}
	return userScript(env, body("sudo -n systemctl")),
		ShellScript(`printf 'serviceman: %s (sudo)\n' "$SERVICEMAN_LABEL"`+"\n"+body("sudo systemctl"),
			append(env, "SERVICEMAN_LABEL="+label)...)
}

// ActionCmds runs systemctl verb on a unit: a quick, non-interactive command
// and (for system units when not root) an interactive fallback that may ask
// for a sudo password.
func ActionCmds(scope config.Scope, verb, unit string) (quick, interactive *exec.Cmd) {
	return privCmds(scope, "systemctl "+verb+" "+unit, []string{"SERVICEMAN_UNIT=" + unit},
		func(ctl string) string { return ctl + " " + verb + ` -- "$SERVICEMAN_UNIT"` })
}

// RunQuick runs a quick command. needsAuth is true when sudo wanted a password.
func RunQuick(cmd *exec.Cmd) (needsAuth bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	c.Env = cmd.Env
	_, err = run(c)
	if err != nil {
		m := strings.ToLower(err.Error())
		if strings.Contains(m, "password is required") || strings.Contains(m, "terminal is required") ||
			strings.Contains(m, "interactive authentication") {
			return true, err
		}
	}
	return false, err
}

// DaemonReloadCmds reloads the systemd manager of the scope.
func DaemonReloadCmds(scope config.Scope) (quick, interactive *exec.Cmd) {
	return privCmds(scope, "systemctl daemon-reload", nil,
		func(ctl string) string { return ctl + " daemon-reload" })
}

// RemoveUnitCmd stops and disables a user unit, deletes its unit file and
// drop-in directory when they live in the user unit directory (never vendor
// files), and reloads the user manager.
func RemoveUnitCmd(unit, fragmentPath string) *exec.Cmd {
	dir := UserUnitDir()
	file := filepath.Join(dir, unit)
	if fragmentPath != "" && filepath.Dir(fragmentPath) == dir {
		file = fragmentPath
	}
	env := []string{"SERVICEMAN_UNIT=" + unit, "SERVICEMAN_FILE=" + file, "SERVICEMAN_DROPINS=" + filepath.Join(dir, unit+".d")}
	return userScript(env, `systemctl --user disable --now -- "$SERVICEMAN_UNIT" || true
rm -f -- "$SERVICEMAN_FILE"
rm -rf -- "$SERVICEMAN_DROPINS"
systemctl --user daemon-reload
systemctl --user reset-failed -- "$SERVICEMAN_UNIT" 2>/dev/null || true`)
}

// EnvFileDropInPath is the drop-in ServiceMan writes for M-x "Set env file".
func EnvFileDropInPath(unit string) string {
	return filepath.Join(UserUnitDir(), unit+".d", "env-file.conf")
}

// SetEnvFileCmd writes (or, for an empty path, removes) a drop-in that adds
// EnvironmentFile=path to a user unit, then runs systemctl daemon-reload.
// The unit file itself is left alone; the service picks the variables up on
// its next (re)start.
func SetEnvFileCmd(unit, path string) *exec.Cmd {
	file := EnvFileDropInPath(unit)
	content := "# Written by ServiceMan (M-x \"Set env file\")\n[Service]\nEnvironmentFile=" + path + "\n"
	env := []string{"SERVICEMAN_FILE=" + file, "SERVICEMAN_DIR=" + filepath.Dir(file), "SERVICEMAN_CONTENT=" + content}
	if path == "" {
		return userScript(env, `rm -f -- "$SERVICEMAN_FILE"
systemctl --user daemon-reload`)
	}
	return userScript(env, `mkdir -p -- "$SERVICEMAN_DIR"
printf '%s' "$SERVICEMAN_CONTENT" > "$SERVICEMAN_FILE"
systemctl --user daemon-reload`)
}
