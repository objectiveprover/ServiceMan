// Package systemd wraps systemctl/journalctl for svcman.
package systemd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"svcman/internal/config"
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

// ---------------------------------------------------------------- status

type Status struct {
	Unit          string
	Description   string
	LoadState     string
	ActiveState   string
	SubState      string
	UnitFileState string
	FragmentPath  string
	MainPID       int
	Memory        uint64 // bytes, 0 = unknown
	Since         time.Time
	NRestarts     int
	NeedReload    bool
	ExecPath      string   // executable of the first ExecStart= command
	DropIns       []string // drop-in files (*.conf)
	EnvFiles      []string // EnvironmentFile= entries
}

var showProps = "Id,Description,LoadState,ActiveState,SubState,UnitFileState,FragmentPath,MainPID,MemoryCurrent,ActiveEnterTimestamp,NRestarts,NeedDaemonReload,ExecStart,DropInPaths,EnvironmentFiles"

// parseExecPath extracts the executable from an ExecStart property value,
// e.g. "{ path=/usr/bin/foo ; argv[]=/usr/bin/foo -x ; ... }".
func parseExecPath(v string) string {
	i := strings.Index(v, "path=")
	if i < 0 {
		return ""
	}
	rest := v[i+len("path="):]
	if j := strings.Index(rest, " ;"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// parseEnvFile strips the " (ignore_errors=...)" suffix of an
// EnvironmentFiles property value.
func parseEnvFile(v string) string {
	if i := strings.LastIndex(v, " (ignore_errors="); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// Show fetches the status of several units of one scope in a single call.
func Show(scope config.Scope, units []string) (map[string]Status, error) {
	res := map[string]Status{}
	if len(units) == 0 {
		return res, nil
	}
	args := append([]string{"show", "--timestamp=unix", "-p", showProps, "--"}, units...)
	out, err := run(Systemctl(scope, args...))
	if err != nil && out == "" {
		return res, err
	}
	blocks := strings.Split(strings.TrimRight(out, "\n"), "\n\n")
	for i, blk := range blocks {
		if i >= len(units) {
			break
		}
		st := Status{Unit: units[i]}
		for _, line := range strings.Split(blk, "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			switch k {
			case "Description":
				st.Description = v
			case "LoadState":
				st.LoadState = v
			case "ActiveState":
				st.ActiveState = v
			case "SubState":
				st.SubState = v
			case "UnitFileState":
				st.UnitFileState = v
			case "FragmentPath":
				st.FragmentPath = v
			case "MainPID":
				st.MainPID, _ = strconv.Atoi(v)
			case "MemoryCurrent":
				if n, err := strconv.ParseUint(v, 10, 64); err == nil && n != ^uint64(0) {
					st.Memory = n
				}
			case "ActiveEnterTimestamp":
				if n, err := strconv.ParseInt(strings.TrimPrefix(v, "@"), 10, 64); err == nil && n > 0 {
					st.Since = time.Unix(n, 0)
				}
			case "NRestarts":
				st.NRestarts, _ = strconv.Atoi(v)
			case "NeedDaemonReload":
				st.NeedReload = v == "yes"
			case "ExecStart":
				if st.ExecPath == "" {
					st.ExecPath = parseExecPath(v)
				}
			case "DropInPaths":
				st.DropIns = strings.Fields(v)
			case "EnvironmentFiles":
				if f := parseEnvFile(v); f != "" {
					st.EnvFiles = append(st.EnvFiles, f)
				}
			}
		}
		res[units[i]] = st
	}
	return res, nil
}

// DetectScope guesses whether a unit lives in the system or user manager.
func DetectScope(unit string) (config.Scope, bool) {
	for _, sc := range []config.Scope{config.System, config.User} {
		m, err := Show(sc, []string{unit})
		if err == nil && m[unit].LoadState == "loaded" {
			return sc, true
		}
	}
	return config.System, false
}

// IsActive returns the is-active state string for a unit.
func IsActive(scope config.Scope, unit string) string {
	out, _ := run(Systemctl(scope, "is-active", unit))
	return strings.TrimSpace(out)
}

// ---------------------------------------------------------------- listing

type UnitEntry struct {
	Unit        string
	Scope       config.Scope
	FileState   string
	ActiveState string
	SubState    string
	Description string
}

// ListAll lists every service unit (files + loaded instances) for a scope.
func ListAll(scope config.Scope) ([]UnitEntry, error) {
	byName := map[string]*UnitEntry{}
	get := func(name string) *UnitEntry {
		e, ok := byName[name]
		if !ok {
			e = &UnitEntry{Unit: name, Scope: scope}
			byName[name] = e
		}
		return e
	}
	out, err := run(Systemctl(scope, "list-unit-files", "--type=service", "--no-legend", "--plain", "--no-pager"))
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasSuffix(f[0], "@.service") {
			continue
		}
		get(f[0]).FileState = f[1]
	}
	out, _ = run(Systemctl(scope, "list-units", "--type=service", "--all", "--no-legend", "--plain", "--no-pager"))
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[1] == "not-found" {
			continue
		}
		e := get(f[0])
		e.ActiveState, e.SubState = f[2], f[3]
		if len(f) > 4 {
			e.Description = strings.Join(f[4:], " ")
		}
	}
	res := make([]UnitEntry, 0, len(byName))
	for _, e := range byName {
		res = append(res, *e)
	}
	sort.Slice(res, func(i, j int) bool { return strings.ToLower(res[i].Unit) < strings.ToLower(res[j].Unit) })
	return res, nil
}

// ---------------------------------------------------------------- actions

// ActionCmds returns a quick, non-interactive command and (for system units
// when not root) an interactive fallback that may prompt for a sudo password.
func ActionCmds(scope config.Scope, verb, unit string) (quick, interactive *exec.Cmd) {
	if scope == config.User || IsRoot() {
		return Systemctl(scope, verb, unit), nil
	}
	quick = exec.Command("sudo", "-n", "systemctl", verb, unit)
	interactive = ShellScript(fmt.Sprintf(`echo "svcman: systemctl %s %s (sudo)"; sudo systemctl %s -- %s`,
		verb, shq(unit), verb, shq(unit)))
	return quick, interactive
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

// ShellScript returns a `sh -c` command. On failure it pauses so the user can
// read the output before the TUI comes back.
func ShellScript(script string, env ...string) *exec.Cmd {
	// The script runs in its own `sh -e` (errexit is ignored inside `a || b`).
	wrapper := `sh -ec "$SVCMAN_SCRIPT"; rc=$?
if [ $rc -ne 0 ] && [ $rc -ne 3 ]; then echo; printf 'svcman: command failed (exit %s) — press Enter to return ' $rc; read _; fi
exit $rc`
	cmd := exec.Command("sh", "-c", wrapper)
	cmd.Env = append(append(os.Environ(), env...), "SVCMAN_SCRIPT="+script)
	return cmd
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

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

// DaemonReloadCmd reloads the systemd manager of the scope.
func DaemonReloadCmds(scope config.Scope) (quick, interactive *exec.Cmd) {
	if scope == config.User || IsRoot() {
		return Systemctl(scope, "daemon-reload"), nil
	}
	return exec.Command("sudo", "-n", "systemctl", "daemon-reload"),
		ShellScript(`echo "svcman: systemctl daemon-reload (sudo)"; sudo systemctl daemon-reload`)
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
	env := []string{"SVCMAN_UNIT=" + unit, "SVCMAN_FILE=" + file, "SVCMAN_DROPINS=" + filepath.Join(dir, unit+".d")}
	body := func(priv, ctl string) string {
		return ctl + ` disable --now -- "$SVCMAN_UNIT" || true
` + priv + `rm -f -- "$SVCMAN_FILE"
` + priv + `rm -rf -- "$SVCMAN_DROPINS"
` + ctl + ` daemon-reload
` + ctl + ` reset-failed -- "$SVCMAN_UNIT" 2>/dev/null || true`
	}
	if scope == config.User || IsRoot() {
		quick = exec.Command("sh", "-ec", body("", ctl(scope)))
		quick.Env = append(os.Environ(), env...)
		return quick, nil
	}
	quick = exec.Command("sh", "-ec", body("sudo -n ", "sudo -n systemctl"))
	quick.Env = append(os.Environ(), env...)
	interactive = ShellScript(`echo "svcman: removing $SVCMAN_UNIT (sudo)"`+"\n"+body("sudo ", "sudo systemctl"), env...)
	return quick, interactive
}

// ---------------------------------------------------------------- editing

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

// UnitDir is where locally-administered unit files of a scope live.
func UnitDir(scope config.Scope) string {
	if scope == config.User {
		return filepath.Join(xdgConfig(), "systemd", "user")
	}
	return "/etc/systemd/system"
}

func xdgConfig() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config")
}

// IsVendorPath reports whether a unit file belongs to the distro/packages,
// in which case it should be customised with a drop-in instead.
func IsVendorPath(p string) bool {
	return strings.HasPrefix(p, "/usr/lib/") || strings.HasPrefix(p, "/lib/") ||
		strings.HasPrefix(p, "/usr/share/") || strings.HasPrefix(p, "/run/")
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
	env := []string{"SVCMAN_EDITOR=" + editor, "SVCMAN_FILE=" + path}
	var s string
	if needsPrivilege(scope, path) {
		s = `sudo mkdir -p "$(dirname "$SVCMAN_FILE")"
SUDO_EDITOR="$SVCMAN_EDITOR" sudoedit "$SVCMAN_FILE"
sudo systemctl daemon-reload`
	} else {
		s = `mkdir -p "$(dirname "$SVCMAN_FILE")"
eval "$SVCMAN_EDITOR \"\$SVCMAN_FILE\""
` + ctl(scope) + ` daemon-reload`
	}
	return ShellScript(s, env...)
}

// EditFileCmd opens an arbitrary file (app config, env file) in the editor,
// through sudoedit when it lives outside the home directory. No reload.
func EditFileCmd(editor, path string) *exec.Cmd {
	env := []string{"SVCMAN_EDITOR=" + editor, "SVCMAN_FILE=" + path}
	if needsPrivilege(config.System, path) {
		if f, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
			f.Close() // writable anyway (e.g. group-owned): no sudo
		} else {
			return ShellScript(`SUDO_EDITOR="$SVCMAN_EDITOR" sudoedit "$SVCMAN_FILE"`, env...)
		}
	}
	return ShellScript(`mkdir -p "$(dirname "$SVCMAN_FILE")"
eval "$SVCMAN_EDITOR \"\$SVCMAN_FILE\""`, env...)
}

// ViewCmd opens a (user-readable) file in the editor without reloading.
func ViewCmd(editor, path string) *exec.Cmd {
	return ShellScript(`eval "$SVCMAN_EDITOR \"\$SVCMAN_FILE\""`, "SVCMAN_EDITOR="+editor, "SVCMAN_FILE="+path)
}

// ---------------------------------------------------------------- new units

type NewUnit struct {
	Name        string // without or with .service
	Description string
	Scope       config.Scope
	ExecStart   string
	WorkingDir  string
	RunAs       string // User= (system scope only)
	Environment string
	EnvFile     string
	Restart     string
	Network     bool
	Enable      bool
	Start       bool
}

func (u NewUnit) UnitName() string { return config.NormalizeUnit(u.Name) }

// Render produces the unit file text.
func (u NewUnit) Render() string {
	var b strings.Builder
	desc := u.Description
	if desc == "" {
		desc = strings.TrimSuffix(u.UnitName(), ".service")
	}
	b.WriteString("# -*- mode: conf-unix -*-  (created by svcman)\n")
	b.WriteString("[Unit]\nDescription=" + desc + "\n")
	if u.Network && u.Scope == config.System {
		b.WriteString("Wants=network-online.target\nAfter=network-online.target\n")
	}
	b.WriteString("\n[Service]\nType=simple\n")
	if u.Scope == config.System && u.RunAs != "" {
		b.WriteString("User=" + u.RunAs + "\n")
	}
	if u.WorkingDir != "" {
		b.WriteString("WorkingDirectory=" + u.WorkingDir + "\n")
	}
	if u.EnvFile != "" {
		b.WriteString("EnvironmentFile=" + u.EnvFile + "\n")
	}
	if u.Environment != "" {
		b.WriteString("Environment=" + u.Environment + "\n")
	}
	b.WriteString("ExecStart=" + resolveExec(u.ExecStart) + "\n")
	if u.Restart != "" && u.Restart != "no" {
		b.WriteString("Restart=" + u.Restart + "\nRestartSec=3\n")
	}
	target := "multi-user.target"
	if u.Scope == config.User {
		target = "default.target"
	}
	b.WriteString("\n[Install]\nWantedBy=" + target + "\n")
	return b.String()
}

// resolveExec makes the executable absolute so the unit is unambiguous.
func resolveExec(cmdline string) string {
	cmdline = strings.TrimSpace(cmdline)
	f := strings.Fields(cmdline)
	if len(f) == 0 || strings.HasPrefix(f[0], "/") || strings.ContainsAny(f[0][:1], "-@:+!") {
		return cmdline
	}
	if p, err := exec.LookPath(f[0]); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs + strings.TrimPrefix(cmdline, f[0])
		}
	}
	return cmdline
}

// Exists reports whether a unit file already exists in the admin directory.
func (u NewUnit) Exists() bool {
	_, err := os.Stat(filepath.Join(UnitDir(u.Scope), u.UnitName()))
	return err == nil
}

// InstallCmd writes the unit file (optionally editing it first), reloads,
// enables and starts it. Exit status 3 means the user cancelled.
func (u NewUnit) InstallCmd(editor string, editFirst bool) (*exec.Cmd, error) {
	tmp, err := os.CreateTemp("", "svcman-*-"+u.UnitName())
	if err != nil {
		return nil, err
	}
	if _, err := tmp.WriteString(u.Render()); err != nil {
		return nil, err
	}
	tmp.Close()
	dest := filepath.Join(UnitDir(u.Scope), u.UnitName())
	env := []string{"SVCMAN_EDITOR=" + editor, "SVCMAN_FILE=" + tmp.Name(), "SVCMAN_DEST=" + dest, "SVCMAN_UNIT=" + u.UnitName()}
	priv := ""
	if needsPrivilege(u.Scope, dest) {
		priv = sudo()
	}
	var s strings.Builder
	s.WriteString(`trap 'rm -f "$SVCMAN_FILE"' EXIT` + "\n")
	if editFirst {
		s.WriteString(`eval "$SVCMAN_EDITOR \"\$SVCMAN_FILE\""
printf 'Install %s to %s? [Y/n] ' "$SVCMAN_UNIT" "$SVCMAN_DEST"; read a
case "$a" in n*|N*) exit 3;; esac
`)
	}
	s.WriteString(`echo "svcman: installing $SVCMAN_DEST"` + "\n")
	s.WriteString(priv + `mkdir -p "$(dirname "$SVCMAN_DEST")"` + "\n")
	s.WriteString(priv + `install -m 0644 "$SVCMAN_FILE" "$SVCMAN_DEST"` + "\n")
	s.WriteString(ctl(u.Scope) + " daemon-reload\n")
	if u.Enable {
		s.WriteString(ctl(u.Scope) + ` enable "$SVCMAN_UNIT"` + "\n")
	}
	if u.Start {
		s.WriteString(ctl(u.Scope) + ` start "$SVCMAN_UNIT"` + "\n")
	}
	return ShellScript(s.String(), env...), nil
}
