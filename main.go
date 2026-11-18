// serviceman — manage a hand-picked set of systemd services from a TUI and
// continuously log them to files.
package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"serviceman/internal/config"
	"serviceman/internal/logger"
	"serviceman/internal/systemd"
	"serviceman/internal/tui"
)

// configTemplate is the default config (also installed by `make install`);
// `serviceman config update` adds its new fields to an existing config.
//
//go:embed config.template.json
var configTemplate []byte

const usage = `serviceman — manage your tracked systemd services

Usage:
  serviceman                     open the TUI
  serviceman track [--user|--system] [--url URL] [--watch] [--config PATH] UNIT...
                                 track existing units (scope auto-detected);
                                 --watch restarts them when their binary changes
  serviceman untrack UNIT...     stop tracking units
  serviceman list                list tracked units
  serviceman daemon install      install + start the serviceman daemon (logging + watching)
                                 as serviceman-daemon.service (replaces serviceman-logger.service)
  serviceman daemon uninstall    remove the serviceman daemon (keeps log files)
  serviceman daemon status       show the serviceman daemon status
  serviceman daemon [run]        run the serviceman daemon in the foreground
                                 ("serviceman logger ..." still works)
  serviceman paths               show config / log locations
  serviceman config version      show the config's version and the current one
  serviceman config update       add new settings to the config and raise its version
                                 (never changes or removes existing settings)
  serviceman config path         print where the config file is
  serviceman config template     print the default config

Config: ` + "%s" + `
`

func main() {
	log.SetFlags(0)
	args := os.Args[1:]
	if len(args) == 0 {
		if err := tui.Run(); err != nil {
			log.Fatal(err)
		}
		return
	}
	var err error
	switch args[0] {
	case "track":
		err = track(args[1:])
	case "untrack":
		err = untrack(args[1:])
	case "list", "ls":
		err = list()
	case "daemon":
		if len(args) == 1 || args[1] == "run" {
			log.SetFlags(log.LstdFlags)
			err = logger.Run()
		} else {
			err = daemonCmd(args[1:])
		}
	case "logger": // old name of the daemon subcommands
		if len(args) == 1 {
			args = append(args, "status")
		}
		err = daemonCmd(args[1:])
	case "paths":
		err = paths()
	case "config":
		err = configCmd(args[1:])
	case "help", "-h", "--help":
		fmt.Printf(usage, config.Path())
	default:
		fmt.Fprintf(os.Stderr, usage, config.Path())
		os.Exit(2)
	}
	if err != nil {
		log.Fatal("serviceman: ", err)
	}
}

func track(args []string) error {
	fs := flag.NewFlagSet("track", flag.ExitOnError)
	user := fs.Bool("user", false, "user-scope unit")
	system := fs.Bool("system", false, "system-scope unit")
	url := fs.String("url", "", "health-check URL")
	watch := fs.Bool("watch", false, "watching: restart the unit when its binary changes")
	cfgPath := fs.String("config", "", "the app's own config file (shown in the TUI)")
	fs.Parse(args)
	if fs.NArg() == 0 {
		return fmt.Errorf("track: no units given")
	}
	if (*url != "" || *cfgPath != "") && fs.NArg() > 1 {
		return fmt.Errorf("track: --url and --config apply to a single unit")
	}
	cp := config.ExpandTilde(*cfgPath)
	if cp != "" && !filepath.IsAbs(cp) {
		if abs, err := filepath.Abs(cp); err == nil {
			cp = abs
		}
	}
	var added []config.Service
	for _, name := range fs.Args() {
		unit := config.NormalizeUnit(name)
		if !config.ValidUnitName(unit) {
			return fmt.Errorf("invalid unit name %q", unit)
		}
		scope := config.System
		switch {
		case *user:
			scope = config.User
		case *system:
		default:
			sc, ok := systemd.DetectScope(unit)
			if !ok {
				fmt.Fprintf(os.Stderr, "warning: %s not found in system or user manager; tracking as system\n", unit)
			}
			scope = sc
		}
		added = append(added, config.Service{Unit: unit, Scope: scope, URL: config.NormalizeURL(*url), Watch: *watch, ConfigPath: cp})
	}
	_, err := config.Update(func(c *config.Config) {
		for _, s := range added {
			if c.Track(s) {
				fmt.Printf("tracking %s (%s)\n", s.Unit, s.Scope)
				continue
			}
			// Already tracked: apply the flags that were given.
			t := c.Find(s.Unit, s.Scope)
			if s.URL != "" {
				t.URL = s.URL
			}
			if s.ConfigPath != "" {
				t.ConfigPath = s.ConfigPath
			}
			if s.Watch {
				t.Watch = true
			}
			fmt.Printf("already tracked: %s (%s)\n", s.Unit, s.Scope)
		}
	})
	return err
}

func untrack(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("untrack: no units given")
	}
	_, err := config.Update(func(c *config.Config) {
		for _, name := range args {
			unit := config.NormalizeUnit(name)
			n := 0
			for _, sc := range []config.Scope{config.System, config.User} {
				if c.Untrack(unit, sc) {
					fmt.Printf("untracked %s (%s)\n", unit, sc)
					n++
				}
			}
			if n == 0 {
				fmt.Printf("not tracked: %s\n", unit)
			}
		}
	})
	return err
}

func list() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Services) == 0 {
		fmt.Println("no tracked services (use `serviceman track UNIT` or the TUI)")
		return nil
	}
	st, _ := systemd.ShowServices(cfg.Services)
	fmt.Printf("%-36s %-6s %-20s %-10s %s\n", "UNIT", "SCOPE", "STATE", "BOOT", "URL")
	for _, s := range cfg.Services {
		x := st[s.Key()]
		fmt.Printf("%-36s %-6s %-20s %-10s %s\n", s.Unit, s.Scope, x.ActiveState+"/"+x.SubState, x.UnitFileState, s.URL)
	}
	return nil
}

func paths() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Printf("config:  %s\nlogs:    %s\ncursors: %s/cursors\nwatch:   %s\nbin dir: %s\ndaemon:  %s\n",
		config.Path(), cfg.LogDir, config.StateDir(), config.WatchStatePath(), cfg.BinDir, logger.UnitPath())
	return nil
}

func daemonCmd(args []string) error {
	switch args[0] {
	case "install":
		notes, err := logger.Install()
		for _, n := range notes {
			fmt.Println(n)
		}
		return err
	case "uninstall":
		return logger.Uninstall()
	case "status":
		u := logger.InstalledUnit()
		if u == "" {
			fmt.Println("serviceman daemon: not installed (serviceman daemon install)")
			return nil
		}
		c := systemd.Systemctl(config.User, "status", "--no-pager", u)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		c.Run()
		return nil
	}
	return fmt.Errorf("daemon: unknown subcommand %q (install, uninstall, status, run)", args[0])
}

func configCmd(args []string) error {
	const use = "usage: serviceman config version|update|path|template [CONFIG]"
	if len(args) == 0 || len(args) > 2 {
		return fmt.Errorf(use)
	}
	path := config.Path()
	if len(args) == 2 {
		path = config.ExpandTilde(args[1])
	}
	switch args[0] {
	case "path":
		fmt.Println(path)
	case "template":
		os.Stdout.Write(configTemplate)
	case "version":
		v, err := config.FileVersion(path)
		if err != nil {
			return err
		}
		switch {
		case v < config.CurrentVersion:
			fmt.Printf("%s: version %d, current is %d (serviceman config update adds the new settings)\n", config.Tilde(path), v, config.CurrentVersion)
		case v > config.CurrentVersion:
			fmt.Printf("%s: version %d, newer than this serviceman's %d\n", config.Tilde(path), v, config.CurrentVersion)
		default:
			fmt.Printf("%s: version %d (current)\n", config.Tilde(path), v)
		}
	case "update":
		res, err := config.UpdateFromTemplate(path, configTemplate)
		if err != nil {
			return err
		}
		if len(res.Added) == 0 && res.OldVersion == res.NewVersion {
			fmt.Printf("%s is up to date (version %d)\n", config.Tilde(path), res.NewVersion)
			return nil
		}
		msg := fmt.Sprintf("updated %s: version %d → %d", config.Tilde(path), res.OldVersion, res.NewVersion)
		if len(res.Added) > 0 {
			msg += ", added " + strings.Join(res.Added, ", ")
		}
		fmt.Println(msg)
	default:
		return fmt.Errorf(use)
	}
	return nil
}
