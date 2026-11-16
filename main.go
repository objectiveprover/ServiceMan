// svcman — manage a hand-picked set of systemd services from a TUI and
// continuously record their logs.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"svcman/internal/config"
	"svcman/internal/logger"
	"svcman/internal/systemd"
	"svcman/internal/tui"
)

const usage = `svcman — manage your tracked systemd services

Usage:
  svcman                         open the TUI
  svcman track [--user|--system] [--url URL] [--watch] [--config PATH] UNIT...
                                 track existing units (scope auto-detected);
                                 --watch restarts them when their binary changes
  svcman untrack UNIT...         stop tracking units
  svcman list                    list tracked units
  svcman logger install          install + start the log recorder (user service)
  svcman logger uninstall        remove the log recorder (keeps recorded logs)
  svcman logger status           show the log recorder status
  svcman daemon                  run the log recorder + binary watcher in the foreground
  svcman paths                   show config / log locations

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
		log.SetFlags(log.LstdFlags)
		err = logger.Run()
	case "logger":
		err = loggerCmd(args[1:])
	case "paths":
		cfg, _ := config.Load()
		fmt.Printf("config:  %s\nlogs:    %s\ncursors: %s/cursors\nwatch:   %s\nbin dir: %s\nlogger:  %s\n",
			config.Path(), cfg.LogDir, config.StateDir(), config.WatchStatePath(), cfg.BinDir, logger.UnitPath())
	case "help", "-h", "--help":
		fmt.Printf(usage, config.Path())
	default:
		fmt.Fprintf(os.Stderr, usage, config.Path())
		os.Exit(2)
	}
	if err != nil {
		log.Fatal("svcman: ", err)
	}
}

func track(args []string) error {
	fs := flag.NewFlagSet("track", flag.ExitOnError)
	user := fs.Bool("user", false, "user-scope unit")
	system := fs.Bool("system", false, "system-scope unit")
	url := fs.String("url", "", "health-check URL")
	watch := fs.Bool("watch", false, "restart the unit when its binary changes")
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
		u := *url
		if u != "" && !strings.Contains(u, "://") {
			u = "http://" + u
		}
		added = append(added, config.Service{Unit: unit, Scope: scope, URL: u, Watch: *watch, ConfigPath: cp})
	}
	_, err := config.Update(func(c *config.Config) {
		for _, s := range added {
			if c.Track(s) {
				fmt.Printf("tracking %s (%s)\n", s.Unit, s.Scope)
				continue
			}
			// Already tracked: apply the flags that were given.
			i := c.Index(s.Unit, s.Scope)
			if s.URL != "" {
				c.Services[i].URL = s.URL
			}
			if s.ConfigPath != "" {
				c.Services[i].ConfigPath = s.ConfigPath
			}
			if s.Watch {
				c.Services[i].Watch = true
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
		fmt.Println("no tracked services (use `svcman track UNIT` or the TUI)")
		return nil
	}
	by := map[config.Scope][]string{}
	for _, s := range cfg.Services {
		by[s.Scope] = append(by[s.Scope], s.Unit)
	}
	st := map[string]systemd.Status{}
	for sc, units := range by {
		m, _ := systemd.Show(sc, units)
		for u, s := range m {
			st[string(sc)+"/"+u] = s
		}
	}
	fmt.Printf("%-36s %-6s %-20s %-10s %s\n", "UNIT", "SCOPE", "STATE", "BOOT", "URL")
	for _, s := range cfg.Services {
		x := st[string(s.Scope)+"/"+s.Unit]
		fmt.Printf("%-36s %-6s %-20s %-10s %s\n", s.Unit, s.Scope, x.ActiveState+"/"+x.SubState, x.UnitFileState, s.URL)
	}
	return nil
}

func loggerCmd(args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
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
		c := systemd.Systemctl(config.User, "status", "--no-pager", logger.UnitName)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		c.Run()
		return nil
	}
	return fmt.Errorf("logger: unknown subcommand %q", args[0])
}
