// Package daemon is the serviceman daemon. It follows the journal of
// every tracked unit and appends to rotating files, remembering a journal
// cursor per unit so restarts never lose or duplicate lines. It also runs the
// watching, which restarts units whose executable was replaced.
package daemon

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
	"serviceman/internal/watcher"
)

// Run is the daemon main loop.
func Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	d := &daemon{followers: map[string]*follower{}}
	if w, err := watcher.New(config.WatchStatePath(), SystemdRestart); err != nil {
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
