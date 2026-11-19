// Package watcher restarts systemd units when their executable is replaced
// on disk. It watches the parent directory of each binary with inotify (so
// unlink+recreate and rename-into-place deploys are seen), debounces bursts
// of events, and only restarts when the file content actually changed.
package watcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"serviceman/internal/config"
)

// Target is a binary whose replacement should restart a unit.
type Target struct {
	Key   string // "<scope>/<unit>", also the key in the state file
	Scope config.Scope
	Unit  string
	Path  string
	// Self marks ServiceMan's own daemon: it is restarted with --no-block and its
	// result is not awaited (the restart kills us).
	Self bool
}

// State is what the daemon publishes per target for the TUI.
type State struct {
	Path        string    `json:"path"`
	Hash        string    `json:"hash,omitempty"`
	ChangedAt   time.Time `json:"changed_at,omitzero"`
	RestartedAt time.Time `json:"restarted_at,omitzero"`
	// Result of the last change: ok, failed, skipped, error ("" = none yet).
	Result string `json:"result,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Restarter restarts a target after its binary changed and reports the
// outcome. ctime is the inode change time of the new binary.
type Restarter func(t Target, ctime time.Time) (result, detail string)

const watchMask = unix.IN_CLOSE_WRITE | unix.IN_MOVED_TO | unix.IN_CREATE | unix.IN_ATTRIB |
	unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MODIFY | unix.IN_ONLYDIR

type target struct {
	Target
	files     []string // watched absolute paths: Path and its symlink target
	timer     *time.Timer
	hash      string
	lastStart time.Time
	check     sync.Mutex // serialises check+restart
}

type Watcher struct {
	// Debounce is how long a binary must be quiet before it is checked.
	Debounce time.Duration
	// MinInterval is the minimum time between two restarts of one target.
	MinInterval time.Duration
	// StatePath is where State is written ("" = don't persist).
	StatePath string
	Restart   Restarter

	fd int
	f  *os.File

	mu      sync.Mutex
	dirs    map[string]int // dir -> watch descriptor
	wds     map[int]string // watch descriptor -> dir
	targets map[string]*target
	states  map[string]State
}

// New starts an inotify instance. Call Close when done.
func New(statePath string, restart Restarter) (*Watcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("inotify: %w", err)
	}
	w := &Watcher{
		Debounce:    1500 * time.Millisecond,
		MinInterval: 3 * time.Second,
		StatePath:   statePath,
		Restart:     restart,
		fd:          fd,
		// A non-blocking fd goes through the runtime poller, so Close
		// unblocks a pending Read.
		f:       os.NewFile(uintptr(fd), "inotify"),
		dirs:    map[string]int{},
		wds:     map[int]string{},
		targets: map[string]*target{},
		states:  map[string]State{},
	}
	if statePath != "" {
		if b, err := os.ReadFile(statePath); err == nil {
			json.Unmarshal(b, &w.states)
		}
	}
	go w.loop()
	return w, nil
}

func (w *Watcher) Close() error {
	w.mu.Lock()
	for _, t := range w.targets {
		if t.timer != nil {
			t.timer.Stop()
		}
	}
	w.mu.Unlock()
	return w.f.Close()
}

// Sync makes the watched set equal to targets. Unchanged targets keep their
// content baseline; new ones take the current file as baseline, except when
// the state file shows the binary changed while nobody was watching.
func (w *Watcher) Sync(targets []Target) {
	want := map[string]Target{}
	for _, t := range targets {
		if t.Path != "" {
			want[t.Key] = t
		}
	}

	// The daemon calls Sync every few seconds; the state file is only
	// rewritten when the published states changed.
	changed := false
	w.mu.Lock()
	for k, t := range w.targets {
		if nt, ok := want[k]; !ok || nt.Path != t.Path || !sameFiles(t.files, filesFor(nt.Path)) {
			if t.timer != nil {
				t.timer.Stop()
			}
			delete(w.targets, k)
			if !ok {
				delete(w.states, k)
				changed = true
			}
		}
	}
	var added []*target
	for k, nt := range want {
		if _, ok := w.targets[k]; ok {
			continue
		}
		t := &target{Target: nt, files: filesFor(nt.Path)}
		w.targets[k] = t
		added = append(added, t)
	}
	w.mu.Unlock()

	for _, t := range added {
		hash, _ := hashFile(t.Path)
		w.mu.Lock()
		prev, had := w.states[t.Key]
		t.hash = hash
		changedUnseen := had && prev.Path == t.Path && prev.Hash != "" && hash != "" && prev.Hash != hash
		if changedUnseen {
			t.hash = prev.Hash // let the check see the difference
		}
		if !had || prev.Path != t.Path {
			w.states[t.Key] = State{Path: t.Path, Hash: hash}
			changed = true
		}
		w.mu.Unlock()
		if changedUnseen {
			log.Printf("watch %s: %s changed while not watched", t.Unit, t.Path)
			w.schedule(t, 0)
		} else {
			log.Printf("watch %s: %s", t.Unit, t.Path)
		}
	}

	w.mu.Lock()
	dirs := map[string]bool{}
	for _, t := range w.targets {
		for _, f := range t.files {
			dirs[filepath.Dir(f)] = true
		}
	}
	for d, wd := range w.dirs {
		if !dirs[d] {
			unix.InotifyRmWatch(w.fd, uint32(wd))
			delete(w.dirs, d)
			delete(w.wds, wd)
		}
	}
	for d := range dirs {
		if _, ok := w.dirs[d]; ok {
			continue
		}
		wd, err := unix.InotifyAddWatch(w.fd, d, watchMask)
		if err != nil {
			continue // missing dir: retried on the next Sync
		}
		w.dirs[d] = wd
		w.wds[wd] = d
	}
	w.mu.Unlock()
	if changed {
		w.save()
	}
}

// States returns a copy of the published state.
func (w *Watcher) States() map[string]State {
	w.mu.Lock()
	defer w.mu.Unlock()
	res := make(map[string]State, len(w.states))
	for k, v := range w.states {
		res[k] = v
	}
	return res
}

// filesFor returns path and, if it is a symlink, its resolved target.
func filesFor(path string) []string {
	files := []string{path}
	if r, err := filepath.EvalSymlinks(path); err == nil && r != path {
		files = append(files, r)
	}
	return files
}

func sameFiles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ------------------------------------------------------------------ events

func (w *Watcher) loop() {
	buf := make([]byte, 64*1024)
	for {
		n, err := w.f.Read(buf)
		if err != nil {
			if !errors.Is(err, os.ErrClosed) {
				log.Printf("inotify read: %v", err)
			}
			return
		}
		for off := 0; off+unix.SizeofInotifyEvent <= n; {
			ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
			nameStart := off + unix.SizeofInotifyEvent
			nameEnd := nameStart + int(ev.Len)
			if nameEnd > n {
				break
			}
			name := strings.TrimRight(string(buf[nameStart:nameEnd]), "\x00")
			off = nameEnd
			w.event(int(ev.Wd), ev.Mask, name)
		}
	}
}

func (w *Watcher) event(wd int, mask uint32, name string) {
	w.mu.Lock()
	dir, ok := w.wds[wd]
	if mask&unix.IN_IGNORED != 0 {
		// The directory went away; Sync re-adds it when it is back.
		if ok {
			delete(w.dirs, dir)
			delete(w.wds, wd)
		}
		w.mu.Unlock()
		return
	}
	if !ok || name == "" {
		w.mu.Unlock()
		return
	}
	full := filepath.Join(dir, name)
	var hit []*target
	for _, t := range w.targets {
		for _, f := range t.files {
			if f == full {
				hit = append(hit, t)
				break
			}
		}
	}
	w.mu.Unlock()
	for _, t := range hit {
		w.schedule(t, w.Debounce)
	}
}

// schedule (re)arms the debounce timer of t.
func (w *Watcher) schedule(t *target, d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.targets[t.Key] != t {
		return // removed or replaced by Sync
	}
	if t.timer != nil {
		t.timer.Stop()
	}
	t.timer = time.AfterFunc(d, func() { w.check(t) })
}

// check runs after the debounce: if the binary is complete and its content
// changed, restart the unit.
func (w *Watcher) check(t *target) {
	t.check.Lock()
	defer t.check.Unlock()

	fi, err := os.Stat(t.Path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return // mid-deploy (deleted / not yet executable): wait for more events
	}
	time.Sleep(300 * time.Millisecond)
	fi2, err := os.Stat(t.Path)
	if err != nil || fi2.Size() != fi.Size() || !fi2.ModTime().Equal(fi.ModTime()) {
		w.schedule(t, w.Debounce) // still being written
		return
	}
	hash, err := hashFile(t.Path)
	if err != nil || hash == t.hash {
		return
	}
	if wait := w.MinInterval - time.Since(t.lastStart); wait > 0 {
		w.schedule(t, wait)
		return
	}

	now := time.Now()
	log.Printf("watch %s: %s changed", t.Unit, t.Path)
	w.setState(t.Key, func(s *State) {
		s.Path, s.Hash, s.ChangedAt = t.Path, hash, now
	})
	t.hash = hash
	t.lastStart = now
	result, detail := "ok", ""
	if w.Restart != nil {
		result, detail = w.Restart(t.Target, ctime(fi2))
	}
	if detail != "" {
		log.Printf("watch %s: %s (%s)", t.Unit, result, detail)
	} else {
		log.Printf("watch %s: %s", t.Unit, result)
	}
	w.setState(t.Key, func(s *State) {
		s.Result, s.Detail = result, detail
		if result == "ok" || result == "failed" {
			s.RestartedAt = now
		}
	})
}

func (w *Watcher) setState(key string, fn func(*State)) {
	w.mu.Lock()
	s := w.states[key]
	fn(&s)
	w.states[key] = s
	w.mu.Unlock()
	w.save()
}

func (w *Watcher) save() {
	if w.StatePath == "" {
		return
	}
	w.mu.Lock()
	b, err := json.MarshalIndent(w.states, "", "  ")
	w.mu.Unlock()
	if err != nil {
		return
	}
	os.MkdirAll(filepath.Dir(w.StatePath), 0o755)
	tmp := w.StatePath + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o644) == nil {
		os.Rename(tmp, w.StatePath)
	}
}

// LoadStates reads a state file written by a Watcher.
func LoadStates(path string) (map[string]State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	res := map[string]State{}
	return res, json.Unmarshal(b, &res)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ctime(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctim.Sec, st.Ctim.Nsec)
	}
	return fi.ModTime()
}
