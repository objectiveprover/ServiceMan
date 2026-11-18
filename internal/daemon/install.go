package daemon

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

const (
	// UnitName is the serviceman daemon's systemd user unit.
	UnitName = "serviceman-daemon.service"
	// LegacyUnitName is the unit's old name; Install migrates it to UnitName.
	LegacyUnitName = "serviceman-logger.service"
)

// UnitPath is where the serviceman daemon's systemd user unit is installed.
func UnitPath() string { return filepath.Join(systemd.UnitDir(config.User), UnitName) }

// LegacyUnitPath is where the unit lived under its old name.
func LegacyUnitPath() string { return filepath.Join(systemd.UnitDir(config.User), LegacyUnitName) }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// InstalledUnit is the name the serviceman daemon is installed under:
// UnitName, or LegacyUnitName when only the old unit exists ("" if neither).
func InstalledUnit() string {
	switch {
	case exists(UnitPath()):
		return UnitName
	case exists(LegacyUnitPath()):
		return LegacyUnitName
	}
	return ""
}

// migrateLegacy runs when the daemon was started by the old unit: it hands
// `serviceman daemon install` to a transient systemd-run job, because
// stopping the old unit kills this process. The job stops and removes the
// old unit and starts the new one.
func migrateLegacy() {
	if InstalledUnit() != LegacyUnitName {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		log.Printf("migrate %s -> %s: %v", LegacyUnitName, UnitName, err)
		return
	}
	out, err := exec.Command("systemd-run", "--user", "--collect", "--quiet",
		"--unit=serviceman-migrate", exe, "daemon", "install").CombinedOutput()
	if err != nil {
		log.Printf("migrate %s -> %s: systemd-run: %v: %s (run `serviceman daemon install` or press D twice in the TUI)",
			LegacyUnitName, UnitName, err, strings.TrimSpace(string(out)))
		return
	}
	log.Printf("migrating %s -> %s", LegacyUnitName, UnitName)
}

// Install writes, enables and (re)starts the serviceman daemon's systemd user
// unit, replacing the old serviceman-logger.service if it is there.
// It returns human-readable notes (e.g. about lingering).
func Install() ([]string, error) {
	bin, err := os.Executable()
	if err != nil {
		return nil, err
	}
	bin, _ = filepath.EvalSymlinks(bin)
	unit := fmt.Sprintf(`[Unit]
Description=serviceman daemon: logging + watching
After=default.target

[Service]
Type=simple
ExecStart=%s daemon
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`, bin)
	if err := os.MkdirAll(filepath.Dir(UnitPath()), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(UnitPath(), []byte(unit), 0o644); err != nil {
		return nil, err
	}
	notes := []string{"installed " + UnitPath()}
	if exists(LegacyUnitPath()) {
		// Stop the old unit first so two daemons never follow the same journals.
		systemd.Systemctl(config.User, "disable", "--now", LegacyUnitName).Run()
		if err := os.Remove(LegacyUnitPath()); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if d := LegacyUnitPath() + ".d"; exists(d) && !exists(UnitPath()+".d") {
			os.Rename(d, UnitPath()+".d") // keep any drop-in overrides
		}
		notes = append(notes, "migrated from "+LegacyUnitName)
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", UnitName}, {"restart", UnitName}} {
		if out, err := systemd.Systemctl(config.User, args...).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("systemctl --user %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	uid := strconv.Itoa(os.Getuid())
	if out, _ := exec.Command("loginctl", "show-user", uid, "-p", "Linger", "--value").Output(); strings.TrimSpace(string(out)) != "yes" {
		if err := exec.Command("loginctl", "enable-linger").Run(); err == nil {
			notes = append(notes, "enabled lingering: the serviceman daemon keeps running after logout and starts at boot")
		} else {
			notes = append(notes, "run `sudo loginctl enable-linger $USER` so the serviceman daemon runs at boot without a login")
		}
	}
	return notes, nil
}

// Disable stops the serviceman daemon and keeps it from starting at
// login/boot; the unit file stays, so Install turns it back on.
func Disable() error {
	u := InstalledUnit()
	if u == "" {
		return nil
	}
	if out, err := systemd.Systemctl(config.User, "disable", "--now", u).CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user disable --now %s: %v: %s", u, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Uninstall disables and removes the serviceman daemon's unit, under its
// current and old names (log files are kept).
func Uninstall() error {
	for _, u := range []string{UnitName, LegacyUnitName} {
		systemd.Systemctl(config.User, "disable", "--now", u).Run()
		if err := os.Remove(filepath.Join(systemd.UnitDir(config.User), u)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return systemd.Systemctl(config.User, "daemon-reload").Run()
}
