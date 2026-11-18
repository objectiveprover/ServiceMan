package systemd

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"serviceman/internal/config"
)

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

	// The main process's last run, for spotting programs that exit right
	// after starting (see QuickExit).
	Type       string
	ExitCode   int           // ExecMainCode: 1 exited, 2 killed, 3 dumped core; 0 = hasn't exited
	ExitStatus int           // the exit status, or the signal number when killed
	RunTime    time.Duration // how long the last main process ran (0 = unknown)
	SelfExit   bool          // it ended on its own, not because the unit was stopped
}

var showProps = "Id,Description,LoadState,ActiveState,SubState,UnitFileState,FragmentPath,MainPID,MemoryCurrent,ActiveEnterTimestamp,NRestarts,NeedDaemonReload,ExecStart,DropInPaths,EnvironmentFiles," +
	"Type,ExecMainCode,ExecMainStatus,ExecMainStartTimestampMonotonic,ExecMainExitTimestampMonotonic,ActiveExitTimestampMonotonic"

// QuickExitAfter is how soon after starting an exit counts as "right away".
const QuickExitAfter = 5 * time.Second

// QuickExitKind tells how a service whose program exited right away ended.
type QuickExitKind int

const (
	NoQuickExit    QuickExitKind = iota
	CleanQuickExit               // status 0: probably not a long-running program
	CrashQuickExit               // non-zero status or a signal: a service failing at startup
)

// QuickExit reports whether the service isn't running because its program
// exited on its own within QuickExitAfter of starting. Oneshot units are
// meant to exit and never count; neither does stopping a service yourself.
func (st Status) QuickExit() QuickExitKind {
	switch {
	case st.Type == "oneshot", st.MainPID > 0, st.ActiveState == "active",
		!st.SelfExit, st.RunTime <= 0, st.RunTime >= QuickExitAfter:
		return NoQuickExit
	case st.ExitCode == 1 && st.ExitStatus == 0:
		return CleanQuickExit
	}
	return CrashQuickExit
}

// setLastRun fills RunTime and SelfExit from monotonic timestamps (µs). On a
// stop the unit leaves the active state before its process exits; when the
// process exits by itself, the unit leaves it afterwards.
func (st *Status) setLastRun(start, exit, activeExit int64) {
	if start > 0 && exit > start {
		st.RunTime = time.Duration(exit-start) * time.Microsecond
		st.SelfExit = activeExit >= exit
	}
}

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
		var start, exit, activeExit int64
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
			case "Type":
				st.Type = v
			case "ExecMainCode":
				st.ExitCode, _ = strconv.Atoi(v)
			case "ExecMainStatus":
				st.ExitStatus, _ = strconv.Atoi(v)
			case "ExecMainStartTimestampMonotonic":
				start, _ = strconv.ParseInt(v, 10, 64)
			case "ExecMainExitTimestampMonotonic":
				exit, _ = strconv.ParseInt(v, 10, 64)
			case "ActiveExitTimestampMonotonic":
				activeExit, _ = strconv.ParseInt(v, 10, 64)
			}
		}
		st.setLastRun(start, exit, activeExit)
		res[units[i]] = st
	}
	return res, nil
}

// ShowServices fetches the status of services of both scopes, keyed by
// Service.Key. The error is the first one a scope gave.
func ShowServices(svcs []config.Service) (map[string]Status, error) {
	by := map[config.Scope][]string{}
	for _, s := range svcs {
		by[s.Scope] = append(by[s.Scope], s.Unit)
	}
	res := map[string]Status{}
	var firstErr error
	for sc, units := range by {
		m, err := Show(sc, units)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for u, st := range m {
			res[config.Service{Unit: u, Scope: sc}.Key()] = st
		}
	}
	return res, firstErr
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

// IsRunning reports whether an ActiveState means the unit is up or on its
// way up (so the TUI's s stops it and Edit Service offers "Restart now").
func IsRunning(activeState string) bool {
	switch activeState {
	case "active", "activating", "reloading":
		return true
	}
	return false
}
