package systemd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"serviceman/internal/config"
)

// NewUnit is a user unit as the New/Edit Service form describes it. Only user
// units are created or edited; system units can only be tracked.
type NewUnit struct {
	Name        string // without or with .service
	Description string
	ExecStart   string
	WorkingDir  string
	Environment string
	EnvFile     string
	Restart     string
	Enable      bool
	Start       bool // Edit: restart instead of start
	// Edit rewrites an existing unit: InstallCmd then enables or disables it
	// per Enable, and restarts it when Start is set.
	Edit bool
}

// ParseUnit reads a unit file into the fields the New/Edit Service form
// covers. unsupported lists the directives the form can't represent (as
// "Key=value"); rewriting such a unit with Render would lose them.
func ParseUnit(path string) (u NewUnit, unsupported []string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return u, nil, err
	}
	u = NewUnit{Name: filepath.Base(path), Restart: "no"}
	seen := map[string]bool{}
	var envs []string
	section := ""
	cont := false // the line continues the previous one (which ended in "\")
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if cont { // the first line already made the directive unsupported
			cont = strings.HasSuffix(line, "\\")
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		once := func() bool { // single-valued keys may appear only once
			if seen[section+k] {
				return false
			}
			seen[section+k] = true
			return true
		}
		known := ok && !strings.HasSuffix(line, "\\") // no continuation lines
		if known {
			switch section + k {
			case "[Unit]Description":
				u.Description, known = v, once()
			case "[Service]Type":
				known = v == "simple" && once()
			case "[Service]WorkingDirectory":
				u.WorkingDir, known = v, once()
			case "[Service]EnvironmentFile":
				u.EnvFile, known = v, once()
			case "[Service]Environment":
				envs = append(envs, v)
			case "[Service]ExecStart":
				u.ExecStart, known = v, v != "" && once()
			case "[Service]Restart":
				u.Restart, known = v, once()
			case "[Service]RestartSec":
				known = v == "3" && once()
			case "[Install]WantedBy":
				known = v == wantedBy && once()
			default:
				known = false
			}
		}
		if !known {
			unsupported = append(unsupported, quoteLine(line))
		}
		cont = strings.HasSuffix(line, "\\")
	}
	u.Environment = strings.Join(envs, " ")
	if u.ExecStart == "" {
		unsupported = append(unsupported, "no ExecStart=")
	}
	if u.Restart != "no" && !seen["[Service]RestartSec"] {
		unsupported = append(unsupported, "Restart= without RestartSec=3")
	}
	return u, unsupported, nil
}

// privateKeys are directives whose values may be secrets.
var privateKeys = map[string]bool{"Environment": true, "SetCredential": true, "SetCredentialEncrypted": true}

// quoteLine is a unit line as messages may show it: the key alone ("Key=…")
// when the value may be a secret or continues on the next line.
func quoteLine(line string) string {
	k, _, ok := strings.Cut(line, "=")
	if k = strings.TrimSpace(k); ok && (privateKeys[k] || strings.HasSuffix(line, "\\")) {
		return k + "=…"
	}
	return line
}

// wantedBy is the target Render installs a user unit into.
const wantedBy = "default.target"

func (u NewUnit) UnitName() string { return NormalizeUnit(u.Name) }

// Render produces the unit file text.
func (u NewUnit) Render() string {
	var b strings.Builder
	desc := u.Description
	if desc == "" {
		desc = strings.TrimSuffix(u.UnitName(), ".service")
	}
	b.WriteString("# -*- mode: conf-unix -*-  (created by ServiceMan)\n")
	b.WriteString("[Unit]\nDescription=" + desc + "\n")
	b.WriteString("\n[Service]\nType=simple\n")
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
	b.WriteString("\n[Install]\nWantedBy=" + wantedBy + "\n")
	return b.String()
}

// resolveExec makes the executable absolute so the unit is unambiguous: a
// bare name is looked up in PATH, and a relative path is taken from the home
// directory (where a unit without WorkingDirectory= runs), never from the
// directory ServiceMan was started in.
func resolveExec(cmdline string) string {
	cmdline = strings.TrimSpace(cmdline)
	f := strings.Fields(cmdline)
	if len(f) == 0 || strings.HasPrefix(f[0], "/") || strings.ContainsAny(f[0][:1], "-@:+!") {
		return cmdline
	}
	rest := strings.TrimPrefix(cmdline, f[0])
	if strings.Contains(f[0], "/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, f[0]) + rest
		}
		return cmdline
	}
	// Only absolute PATH entries: "." or a relative entry would depend on the
	// current directory.
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(dir) {
			continue
		}
		p := filepath.Join(dir, f[0])
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p + rest
		}
	}
	return cmdline
}

// Path is where the unit file is installed.
func (u NewUnit) Path() string { return filepath.Join(UserUnitDir(), u.UnitName()) }

// Exists reports whether the unit file already exists.
func (u NewUnit) Exists() bool {
	_, err := os.Stat(u.Path())
	return err == nil
}

// InstallCmd writes the unit file (optionally editing it first), reloads,
// enables and starts it. Exit status 3 means the user cancelled.
func (u NewUnit) InstallCmd(editor string, editFirst bool) (*exec.Cmd, error) {
	tmp, err := os.CreateTemp("", "serviceman-*-"+u.UnitName())
	if err != nil {
		return nil, err
	}
	_, err = tmp.WriteString(u.Render())
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	dest := u.Path()
	env := []string{"SERVICEMAN_EDITOR=" + editor, "SERVICEMAN_FILE=" + tmp.Name(), "SERVICEMAN_DEST=" + dest, "SERVICEMAN_UNIT=" + u.UnitName()}
	var s strings.Builder
	s.WriteString(`trap 'rm -f "$SERVICEMAN_FILE"' EXIT` + "\n")
	if editFirst {
		s.WriteString(`eval "$SERVICEMAN_EDITOR \"\$SERVICEMAN_FILE\""
printf 'Install %s to %s? [Y/n] ' "$SERVICEMAN_UNIT" "$SERVICEMAN_DEST"; read a
case "$a" in n*|N*) exit 3;; esac
`)
	}
	s.WriteString(`echo "serviceman: installing $SERVICEMAN_DEST"` + "\n")
	s.WriteString(`mkdir -p "$(dirname "$SERVICEMAN_DEST")"` + "\n")
	s.WriteString(`install -m 0644 "$SERVICEMAN_FILE" "$SERVICEMAN_DEST"` + "\n")
	s.WriteString(ctl(config.User) + " daemon-reload\n")
	switch {
	case u.Enable:
		s.WriteString(ctl(config.User) + ` enable "$SERVICEMAN_UNIT"` + "\n")
	case u.Edit:
		s.WriteString(ctl(config.User) + ` disable "$SERVICEMAN_UNIT"` + "\n")
	}
	if u.Start {
		verb := "start"
		if u.Edit {
			verb = "restart"
		}
		s.WriteString(ctl(config.User) + ` ` + verb + ` "$SERVICEMAN_UNIT"` + "\n")
	}
	return ShellScript(s.String(), env...), nil
}

// NormalizeUnit appends ".service" when no unit suffix is given.
func NormalizeUnit(name string) string {
	name = strings.TrimSpace(name)
	for _, suf := range []string{".service", ".socket", ".timer", ".target", ".path", ".mount"} {
		if strings.HasSuffix(name, suf) {
			return name
		}
	}
	return name + ".service"
}

// ValidUnitName rejects names that systemd would not accept.
func ValidUnitName(name string) bool {
	if name == "" || len(name) > 255 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(":-_.\\@", r)) {
			return false
		}
	}
	return true
}
