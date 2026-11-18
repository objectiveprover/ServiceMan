// Package logger implements the serviceman daemon. It follows the journal of
// every tracked unit and appends to rotating files, remembering a journal
// cursor per unit so restarts never lose or duplicate lines. It also runs the
// watching, which restarts units whose executable was replaced.
package logger

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
	"serviceman/internal/watcher"
)

const (
	// UnitName is the serviceman daemon's systemd user unit.
	UnitName = "serviceman-daemon.service"
	// LegacyUnitName is the unit's old name; Install migrates it to UnitName.
	LegacyUnitName = "serviceman-logger.service"
)

// Run is the daemon main loop.
func Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	d := &daemon{followers: map[string]*follower{}}
	if w, err := watcher.New(config.WatchStatePath(), watcher.SystemdRestart); err != nil {
		log.Printf("binary watcher disabled: %v", err)
	} else {
		d.watch = w
		defer w.Close()
		// Under systemd, also restart ourselves when serviceman is reinstalled.
		if os.Getenv("INVOCATION_ID") != "" {
			migrateLegacy()
			if exe, err := os.Executable(); err == nil {
				if r, err := filepath.EvalSymlinks(exe); err == nil {
					exe = r
				}
				d.self = exe
			}
		}
	}
	d.reload(ctx)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case s := <-sigs:
			if s == syscall.SIGHUP {
				log.Print("SIGHUP: reloading config")
				d.reload(ctx)
				continue
			}
			log.Printf("%s: shutting down", s)
			cancel()
			d.stopAll()
			return nil
		case <-tick.C:
			if fi, err := os.Stat(config.Path()); err == nil && !fi.ModTime().Equal(d.mtime) {
				d.reload(ctx)
			} else {
				d.syncWatch() // picks up ExecStart changes after unit edits
			}
		}
	}
}

type daemon struct {
	mtime     time.Time
	cfg       *config.Config
	followers map[string]*follower
	watch     *watcher.Watcher
	self      string // serviceman's own binary, when running as the user service
}

func (d *daemon) reload(ctx context.Context) {
	if fi, err := os.Stat(config.Path()); err == nil {
		d.mtime = fi.ModTime()
	}
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config: %v (keeping current followers)", err)
		return
	}
	d.cfg = cfg
	defer d.syncWatch()
	if err := os.MkdirAll(cfg.LogDir, 0o700); err != nil {
		log.Printf("log dir: %v", err)
	}
	want := map[string]config.Service{}
	for _, s := range cfg.Services {
		if !s.NoRecord {
			want[s.Key()] = s
		}
	}
	for k, f := range d.followers {
		if _, ok := want[k]; !ok {
			log.Printf("stop logging %s", k)
			f.stop()
			delete(d.followers, k)
		}
	}
	for k, s := range want {
		if _, ok := d.followers[k]; ok {
			continue
		}
		log.Printf("start logging %s -> %s", k, cfg.LogPath(s))
		fctx, fcancel := context.WithCancel(ctx)
		f := &follower{svc: s, path: cfg.LogPath(s), maxSize: int64(cfg.MaxLogSizeMB) << 20,
			maxFiles: cfg.MaxLogFiles, cancel: fcancel, done: make(chan struct{})}
		d.followers[k] = f
		go f.run(fctx)
	}
}

// syncWatch points the binary watcher at the executables of watched units.
func (d *daemon) syncWatch() {
	if d.watch == nil || d.cfg == nil {
		return
	}
	var byExec []config.Service // watched units whose ExecStart binary is watched
	for _, s := range d.cfg.Services {
		if s.Watch && s.WatchPath == "" {
			byExec = append(byExec, s)
		}
	}
	st, err := systemd.ShowServices(byExec)
	if err != nil {
		return // keep the current watches rather than dropping them
	}
	var targets []watcher.Target
	for _, s := range d.cfg.Services {
		if !s.Watch {
			continue
		}
		p := config.ExpandTilde(s.WatchPath)
		if p == "" {
			p = st[s.Key()].ExecPath
		}
		targets = append(targets, watcher.Target{Key: s.Key(), Scope: s.Scope, Unit: s.Unit, Path: p})
	}
	if d.self != "" {
		targets = append(targets, watcher.Target{Key: "self", Scope: config.User, Unit: InstalledUnit(), Path: d.self, Self: true})
	}
	d.watch.Sync(targets)
}

func (d *daemon) stopAll() {
	var wg sync.WaitGroup
	for _, f := range d.followers {
		wg.Add(1)
		go func(f *follower) { defer wg.Done(); f.stop() }(f)
	}
	wg.Wait()
}

// ------------------------------------------------------------------ follower

type follower struct {
	svc      config.Service
	path     string
	maxSize  int64
	maxFiles int
	cancel   context.CancelFunc
	done     chan struct{}
}

func (f *follower) stop() {
	f.cancel()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
	}
}

func (f *follower) cursorPath() string {
	return filepath.Join(config.StateDir(), "cursors", string(f.svc.Scope)+"--"+f.svc.Unit)
}

func (f *follower) run(ctx context.Context) {
	defer close(f.done)
	backoff := time.Second
	for {
		start := time.Now()
		n, err := f.once(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Printf("%s: journalctl exited after %d lines: %v", f.svc.Unit, n, err)
		if n == 0 && time.Since(start) < 3*time.Second {
			// Probably an invalid/vacuumed cursor: start fresh.
			os.Remove(f.cursorPath())
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

type entry struct {
	Cursor   string          `json:"__CURSOR"`
	Realtime string          `json:"__REALTIME_TIMESTAMP"`
	Message  json.RawMessage `json:"MESSAGE"`
	Priority string          `json:"PRIORITY"`
	PID      string          `json:"_PID"`
	Ident    string          `json:"SYSLOG_IDENTIFIER"`
	Comm     string          `json:"_COMM"`
}

func (f *follower) once(ctx context.Context) (int, error) {
	args := []string{"-f", "-o", "json", "--no-pager",
		"--output-fields=MESSAGE,PRIORITY,_PID,SYSLOG_IDENTIFIER,_COMM"}
	if f.svc.Scope == config.User {
		args = append(args, "--user-unit", f.svc.Unit)
	} else {
		args = append(args, "-u", f.svc.Unit)
	}
	if c, err := os.ReadFile(f.cursorPath()); err == nil && len(c) > 0 {
		args = append(args, "--after-cursor", strings.TrimSpace(string(c)))
	} else {
		args = append(args, "-n", "1000") // first time: grab recent history
	}
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	w, err := openRotating(f.path, f.maxSize, f.maxFiles)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return 0, err
	}
	defer w.Close()

	n := 0
	lastCursor, saved := "", ""
	lastSave := time.Now()
	save := func() {
		if lastCursor != "" && lastCursor != saved {
			os.MkdirAll(filepath.Dir(f.cursorPath()), 0o755)
			tmp := f.cursorPath() + ".tmp"
			if os.WriteFile(tmp, []byte(lastCursor), 0o644) == nil {
				os.Rename(tmp, f.cursorPath())
				saved = lastCursor
			}
		}
		lastSave = time.Now()
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if _, err := w.Write([]byte(format(e))); err != nil {
			log.Printf("%s: write: %v", f.svc.Unit, err)
		}
		lastCursor = e.Cursor
		n++
		if time.Since(lastSave) > 2*time.Second {
			save()
		}
	}
	save()
	err = cmd.Wait()
	if s := strings.TrimSpace(stderr.String()); s != "" {
		err = fmt.Errorf("%v: %s", err, s)
	}
	return n, err
}

var prioTag = map[string]string{"0": "EMERG", "1": "ALERT", "2": "CRIT", "3": "ERR", "4": "WARN"}

func format(e entry) string {
	ts := time.Now()
	if us, err := strconv.ParseInt(e.Realtime, 10, 64); err == nil {
		ts = time.UnixMicro(us)
	}
	ident := e.Ident
	if ident == "" {
		ident = e.Comm
	}
	if ident == "" {
		ident = "-"
	}
	if e.PID != "" {
		ident += "[" + e.PID + "]"
	}
	msg := decodeMessage(e.Message)
	msg = strings.ReplaceAll(strings.TrimRight(msg, "\n"), "\n", "\n    ")
	tag := ""
	if t, ok := prioTag[e.Priority]; ok {
		tag = " <" + t + ">"
	}
	return fmt.Sprintf("%s %s%s: %s\n", ts.Format("2006-01-02T15:04:05.000-0700"), ident, tag, msg)
}

// decodeMessage handles journal MESSAGE fields, which are strings or (for
// non-UTF-8 data) arrays of bytes.
func decodeMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bs []int
	if json.Unmarshal(raw, &bs) == nil {
		b := make([]byte, len(bs))
		for i, v := range bs {
			b[i] = byte(v)
		}
		return strings.ToValidUTF8(string(b), "?")
	}
	return string(raw)
}

// ------------------------------------------------------------------ rotation

type rotating struct {
	path     string
	max      int64
	maxFiles int
	f        *os.File
	size     int64
}

// Log files are private (0600, new directories 0700): they copy journals
// only you and admins can read, and services sometimes print secrets.
func openRotating(path string, max int64, maxFiles int) (*rotating, error) {
	r := &rotating{path: path, max: max, maxFiles: maxFiles}
	for i := 1; i <= maxFiles; i++ {
		makePrivate(fmt.Sprintf("%s.%d", path, i)) // rotated by an older serviceman
	}
	return r, r.open()
}

// makePrivate takes group and other access away from a file (log files
// written by an older serviceman were 0644).
func makePrivate(p string) {
	if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o077 != 0 {
		os.Chmod(p, fi.Mode().Perm()&0o700)
	}
}

func (r *rotating) open() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	makePrivate(r.path)
	fi, _ := f.Stat()
	r.f, r.size = f, fi.Size()
	return nil
}

func (r *rotating) Write(p []byte) (int, error) {
	if r.size+int64(len(p)) > r.max && r.size > 0 {
		r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotating) rotate() {
	r.f.Close()
	os.Remove(fmt.Sprintf("%s.%d", r.path, r.maxFiles))
	for i := r.maxFiles - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	os.Rename(r.path, r.path+".1")
	if err := r.open(); err != nil {
		log.Printf("rotate %s: %v", r.path, err)
	}
}

func (r *rotating) Close() error { return r.f.Close() }

// ------------------------------------------------------------------ install

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

// State is the serviceman daemon's systemd state ("not installed", "active",
// "inactive", "failed", …) and whether it is still under the old unit name.
func State() (state string, legacy bool) {
	u := InstalledUnit()
	if u == "" {
		return "not installed", false
	}
	return systemd.IsActive(config.User, u), u == LegacyUnitName
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
