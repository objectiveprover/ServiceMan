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

// privCmds builds the commands for a script body that runs systemctl (ctl)
// and maybe file operations that need root (priv, a command prefix). User
// units, and root, get only quick, which runs it directly. Otherwise quick
// runs it with sudo -n (never asks), and interactive, the fallback when sudo
// wants a password, says what it's doing (label) and may ask for it. The
// script reads its values from env, so they are never parsed by the shell.
func privCmds(scope config.Scope, label string, env []string, body func(priv, ctl string) string) (quick, interactive *exec.Cmd) {
	script := func(s string) *exec.Cmd {
		c := exec.Command("sh", "-ec", s)
		c.Env = append(os.Environ(), env...)
		return c
	}
	if scope == config.User || IsRoot() {
		return script(body("", ctl(scope))), nil
	}
	return script(body("sudo -n ", "sudo -n systemctl")),
		ShellScript(`printf 'serviceman: %s (sudo)\n' "$SERVICEMAN_LABEL"`+"\n"+body("sudo ", "sudo systemctl"),
			append(env, "SERVICEMAN_LABEL="+label)...)
}

// ActionCmds runs systemctl verb on a unit: a quick, non-interactive command
// and (for system units when not root) an interactive fallback that may ask
// for a sudo password.
func ActionCmds(scope config.Scope, verb, unit string) (quick, interactive *exec.Cmd) {
	return privCmds(scope, "systemctl "+verb+" "+unit, []string{"SERVICEMAN_UNIT=" + unit},
		func(_, ctl string) string { return ctl + " " + verb + ` -- "$SERVICEMAN_UNIT"` })
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
		func(_, ctl string) string { return ctl + " daemon-reload" })
}

// RemoveUnitCmds stops and disables a unit, deletes its unit file and drop-in
// directory when they live in the admin directory (never vendor files), and
// reloads the manager.
func RemoveUnitCmds(scope config.Scope, unit, fragmentPath string) (quick, interactive *exec.Cmd) {
	dir := UnitDir(scope)
	file := filepath.Join(dir, unit)
	if fragmentPath != "" && filepath.Dir(fragmentPath) == dir {
		file = fragmentPath
	}
	env := []string{"SERVICEMAN_UNIT=" + unit, "SERVICEMAN_FILE=" + file, "SERVICEMAN_DROPINS=" + filepath.Join(dir, unit+".d")}
	return privCmds(scope, "removing "+unit, env, func(priv, ctl string) string {
		return ctl + ` disable --now -- "$SERVICEMAN_UNIT" || true
` + priv + `rm -f -- "$SERVICEMAN_FILE"
` + priv + `rm -rf -- "$SERVICEMAN_DROPINS"
` + ctl + ` daemon-reload
` + ctl + ` reset-failed -- "$SERVICEMAN_UNIT" 2>/dev/null || true`
	})
}

// EnvFileDropInPath is the drop-in serviceman writes for M-x "Set env file".
func EnvFileDropInPath(scope config.Scope, unit string) string {
	return filepath.Join(UnitDir(scope), unit+".d", "env-file.conf")
}

// SetEnvFileCmds writes (or, for an empty path, removes) a drop-in that adds
// EnvironmentFile=path to the unit, then runs systemctl daemon-reload. The
// unit file itself is left alone; the service picks the variables up on its
// next (re)start.
func SetEnvFileCmds(scope config.Scope, unit, path string) (quick, interactive *exec.Cmd) {
	file := EnvFileDropInPath(scope, unit)
	content := "# Written by serviceman (M-x \"Set env file\")\n[Service]\nEnvironmentFile=" + path + "\n"
	env := []string{"SERVICEMAN_FILE=" + file, "SERVICEMAN_DIR=" + filepath.Dir(file), "SERVICEMAN_CONTENT=" + content}
	return privCmds(scope, "setting the env file of "+unit, env, func(priv, ctl string) string {
		if path == "" {
			return priv + `rm -f -- "$SERVICEMAN_FILE"
` + ctl + ` daemon-reload`
		}
		return priv + `mkdir -p -- "$SERVICEMAN_DIR"
printf '%s' "$SERVICEMAN_CONTENT" | ` + priv + `tee -- "$SERVICEMAN_FILE" >/dev/null
` + ctl + ` daemon-reload`
	})
}
