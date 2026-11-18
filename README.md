# serviceman

A terminal UI for the systemd services *you* care about (your web apps plus
anything else you pick). It also logs them to files continuously. Navigation
uses Emacs keys.

---

<img width="1110" height="808" alt="Serviceman" src="https://github.com/user-attachments/assets/7c6abb5c-a401-4af7-93c1-9464ed320a2c" />

---

```
make install                 # -> ~/.local/bin/serviceman + ~/.config/serviceman/config.json
serviceman daemon install    # start the serviceman daemon: logging + watching
serviceman                   # open the TUI
```




`make install` copies `config.template.json` to `~/.config/serviceman/config.json`
the first time. If a config is already there it asks: keep it (the default),
update it (`serviceman config update` adds the template's new fields and never
changes or removes existing ones), or replace it after saving a backup such as
`config.json.20261117-103000.bak`. Run without a terminal, it always keeps the
existing config. `make clean` removes the built executable.

The config has a `version` that goes up whenever a setting is added or removed.
`serviceman config version` shows yours and the current one (the TUI also warns
at startup when yours is older), `serviceman config update` brings it up to
date, `serviceman config path` prints where it is and `serviceman config
template` prints the defaults.

A tracked service's own config defaults to `~/.config/<binary>/config.*` (the
binary named in its `ExecStart`): the Config row shows it with "(default
location)" and `C` opens it. Set another path with `C` / M-x **Set config path**.

### Env files (secrets)

Secrets go in an env file outside `~/.config`, by default
`~/.secrets/<binary>.env` (directory `700`, files `600`), so dotfile repos,
sync and backups can leave one directory out. The New service form pre-fills
its EnvironmentFile field with that path when the file exists (`~` is expanded
in the unit), and M-x **Set env file** attaches one to an existing service
through a drop-in (`<unit>.d/env-file.conf`) without touching the unit file;
restart the service to load it. The Env row in the details box flags an env
file that is missing or readable by other users.

## Workflow

| Want to…                     | How                                                              |
|------------------------------|------------------------------------------------------------------|
| Track existing services      | `a` (Add services) in the TUI: type to filter, `C-n` or `RET` into the list, `RET` toggles tracking, `C-g` or `ESC ESC` goes back. Or `serviceman track nginx myapp` |
| Run a binary from `~/.local/bin` as a service | `c` lists the executables there; `RET` on one opens a prefilled New Service form (user unit, watching on). `RET` on one that already has a service opens its Edit Service form |
| Restart on redeploy          | `w` toggles watching: the serviceman daemon restarts the unit when its binary is replaced (see below) |
| Open the app's own config    | `C` opens it in Emacs (asks for the path the first time) |
| Create a new service         | `c` first asks for the program: pick a binary from `~/.local/bin` (type to filter the list), or move past the list to **Custom command or path** and type any command. `RET` opens the New Service form with a live unit-file preview. New services are always **user** units in `~/.config/systemd/user` (no sudo); system units can be tracked and edited but not created; they run in your home directory (to use another one, add `WorkingDirectory=` with `C-c C-e` or M-x **Edit unit file**); `C-c C-c` installs it, `C-c C-e` opens it in Emacs first. It's tracked automatically. |
| Edit a service               | `e` opens the Edit Service form, the same form filled in from the unit file (the name is read-only; **Run as User** and **Wait for network** appear only for system units); `C-c C-c` saves, runs `systemctl daemon-reload`, enables/disables it and restarts it if **Restart now** is on. Units the form can't represent without losing settings (vendor units in `/usr/lib/...`, directives like `ExecStartPre=`) open in Emacs instead (vendor units get a drop-in `override.conf`); M-x **Edit unit file** always uses Emacs and `E` always edits the drop-in. |
| Watch logs live              | `RET` follows the journal (`C-s` isearch, `f` follow, `w` wrap, `M-↑`/`M-↓` top/bottom; `RET` or `q` goes back) |
| Read log files               | `L` opens the service's log file in Emacs                        |
| Health-check a web service   | `u` sets a URL; the HEALTH column polls it every 10 s; `o` opens it |

**Editor:** uses `emacsclient -t` when an Emacs server is running, otherwise
`emacs -nw`. Override it with `"editor"` in the config. Root-owned unit files
are edited through `sudoedit`, so Emacs runs as you with your own init file.

**sudo:** system-unit actions first try `sudo -n` (works when credentials are
cached). If sudo needs a password, serviceman hands the terminal over for the
prompt and then returns to the TUI. User-scope units never need sudo.

## Keys

`M-` keys can also be typed with `ESC` as a prefix (`ESC x` = `M-x`).
`ESC ESC` cancels, like `C-g`.

### Navigation

- `C-n` / `C-p` — move
- `C-v` / `M-v` — page
- `M-↑` / `M-↓` — first / last (top / bottom in the journal)
- `C-s` — filter
- `C-g` — cancel
- `M-x` — run a command by name
- `?` — help
- `C-x C-c` or `q` — quit

### Actions

- `c` — create a service
- `a` — add services (track existing ones)
- `e` — edit the service
- `s` — start/stop (stops it if it's running, starts it otherwise)
- `r` — restart
- `R` — reload
- `t` — enable/disable
- `i` — status
- `l` — logging on/off
- `C-k` — untrack
- `M-p` / `M-n` — reorder
- `g` — refresh
- `D` — turn the serviceman daemon on/off (twice restarts it)
- `w` — watching on/off
- `C` — edit app config

### M-x only

- **Edit unit file**: edit the unit file in Emacs instead of the Edit Service form
- **Set config path**
- **Reload unit files**: make systemd re-read unit files (`systemctl daemon-reload`; `e` already does it after editing)
- **Set watching path**
- **Edit env file**
- **Set env file**: attach an env file (default `~/.secrets/<binary>.env`) through a drop-in
- **Remove service**: stop, disable, delete the unit file + drop-ins; never the binary

The detail pane under the list shows where each service lives: the binary it
runs (`Run`), the unit file and its drop-ins, `EnvironmentFile`s, the app's own
config file (set with `C` / M-x **Set config path**), the health URL, the log file,
and whether watching and logging are `Off`, `On` or `Inactive` (turned on, but
the serviceman daemon is off, so nothing happens until `D` turns it on).

## Binaries in ~/.local/bin and watching

`c` lists the executables in `~/.local/bin` (`"bin_dir"` in the config). Pick one to create a **user** unit for it (`~/.config/systemd/user`, no sudo; it
runs at boot thanks to lingering, which `serviceman daemon install` enables). User units
can't bind ports below 1024, so use a higher port or a reverse proxy.

Binaries that already have a service come first in that list, then the ones
that look like services by convention (a `~/.config/<binary>/config.*` or a
`~/.secrets/<binary>.env`, marked "likely a service"), then the rest. Nothing
is run to find out.

When a service's program exits on its own within 5 s of starting, its STATE
says so: `exited in 0.2s` (status 0, probably not a long-running program, e.g.
a CLI that printed its help) or `crashed in 0.2s` (non-zero status or a signal:
a real service failing at startup; the journal says why). Stopping a service
yourself and `Type=oneshot` units never count.

With watching on (`w`, or `--watch` on `serviceman track`), the serviceman
daemon watches the unit's `ExecStart` binary (or the file set with M-x **Set watching path**, for services
started through an interpreter) and, when it is replaced:

- waits until the file is quiet for 1.5 s and executable, then compares a
  SHA-256 of it: `touch`/`chmod` alone do nothing;
- restarts the unit if it is running or failed, but leaves a unit you
  stopped alone (`skipped`);
- checks that it is still active 5 s later and reports `ok`/`failed` in
  the TUI (Watching row in the detail pane) and in the serviceman daemon's journal
  (`journalctl --user -u serviceman-daemon`).

`go build -o ~/.local/bin/app`, `make install` / `install(1)` and
rsync-style rename-into-place deploys are all detected (the parent
directory is watched). A binary replaced while the serviceman daemon was down
is restarted when it comes back. System-scope units can be watched
too, but they are only restarted when `sudo -n` works.

The serviceman daemon also watches its own binary, so `make install` of serviceman restarts
it with the new version. After upgrading from a version without
watching, run `serviceman daemon install` (or `D`) once.

The unit used to be called `serviceman-logger.service`. When the new binary
starts under that old name, the serviceman daemon migrates itself: a transient
`systemd-run` job stops and removes the old unit and installs and starts
`serviceman-daemon.service` (drop-in overrides move along). `serviceman daemon
install` and `D` do the same, and `serviceman logger ...` still works as an
alias for `serviceman daemon ...`.

## Logging

`serviceman-daemon.service` is a systemd **user** service running `serviceman daemon`
(the serviceman daemon: logging + watching). It follows each tracked unit's journal and appends to
`~/.local/share/serviceman/logs/<unit>.log`. Files rotate at 20 MB and 5 are kept
(`max_log_size_mb`, `max_log_files`). The serviceman daemon saves a journal cursor per
unit, so a restart neither loses nor duplicates lines. It picks up config
changes within 5 s.

`serviceman daemon install` enables *lingering* so the serviceman daemon runs at boot without a
login. To read system units' journals you need the `adm` or `systemd-journal`
group.

---

<img width="1110" height="808" alt="Live Journal" src="https://github.com/user-attachments/assets/57d146de-48a1-468d-8c7c-8ca872cce429" />

---

## Files

- `~/.config/serviceman/config.json`: tracked services and settings, created from `config.template.json` (`serviceman paths` shows all locations)
- `~/.local/state/serviceman/cursors/`: the serviceman daemon's journal cursors
- `~/.local/state/serviceman/watch.json`: watching status, written by the serviceman daemon, read by the TUI

## Uninstalling

```
make uninstall                       # stop + remove serviceman-daemon.service, remove ~/.local/bin/serviceman
rm -rf ~/.config/serviceman ~/.local/state/serviceman ~/.local/share/serviceman   # config, state, log files
```

Services you tracked or created stay as they are: they are ordinary systemd
units, and serviceman only stored metadata about them. `serviceman daemon install` enabled
lingering. User units still need it to start at boot, so only run
`loginctl disable-linger` if nothing else depends on it.
