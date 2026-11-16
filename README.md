# serviceman

A terminal UI for the systemd services *you* care about (your web apps plus
anything else you pick). It also records their logs continuously. Navigation
uses Emacs keys.

```
make install                 # -> ~/.local/bin/serviceman
serviceman logger install    # start the log recorder (systemd user service)
serviceman                   # open the TUI
```

## Workflow

| Want to…                     | How                                                              |
|------------------------------|------------------------------------------------------------------|
| Track an existing service    | `a` in the TUI (filterable picker, `RET` toggles), or `serviceman track nginx myapp` |
| Run a binary from `~/.local/bin` as a service | `b` lists the executables there; `RET` opens a prefilled New service form (user unit, auto-restart on). `RET` on one that already has a service offers to remove it |
| Restart on redeploy          | `w` toggles auto-restart: the daemon restarts the unit when its binary is replaced (see below) |
| Open the app's own config    | `C` opens it in Emacs (asks for the path the first time) |
| Create a new service         | `+` opens a form with a live unit-file preview; `C-c C-c` installs it, `C-c C-e` opens it in Emacs first. It's tracked automatically. |
| Edit a unit file             | `e` opens it in Emacs and runs `daemon-reload` afterwards. Vendor units (`/usr/lib/...`) get a drop-in `override.conf` instead; `E` always edits the drop-in. |
| Watch logs live              | `RET` follows the journal (`C-s` isearch, `f` follow, `w` wrap)  |
| Read recorded logs           | `v` opens the recorded file in Emacs                             |
| Health-check a web service   | `u` sets a URL; the HEALTH column polls it every 10 s; `o` opens it |

**Editor:** uses `emacsclient -t` when an Emacs server is running, otherwise
`emacs -nw`. Override it with `"editor"` in the config. Root-owned unit files
are edited through `sudoedit`, so Emacs runs as you with your own init file.

**sudo:** system-unit actions first try `sudo -n` (works when credentials are
cached). If sudo needs a password, serviceman hands the terminal over for the
prompt and then returns to the TUI. User-scope units never need sudo.

## Keys

`C-n`/`C-p` move · `C-v`/`M-v` page · `M-<`/`M->` ends · `C-s` filter ·
`C-g` cancel · `M-x` run a command by name · `?` help · `C-x C-c` or `q` quit

`s` start · `S` stop · `r` restart · `R` reload · `t` enable/disable ·
`D` daemon-reload · `i` status · `x` recording on/off · `C-k` untrack ·
`M-p`/`M-n` reorder · `g` refresh · `L` install/restart the daemon ·
`b` bin picker · `w` auto-restart on/off · `C` edit app config

M-x only: `set-config-path`, `set-watch-path`, `edit-env-file`,
`remove-service` (stop, disable, delete the unit file + drop-ins; never the binary)

The detail pane under the list shows where each service is configured: the
unit file and its drop-ins, `EnvironmentFile`s, the app's own config file
(set with `C` / `set-config-path`), and the auto-restart status.

## Binaries in ~/.local/bin and auto-restart

`b` lists the executables in `~/.local/bin` (`"bin_dir"` in the config). Pick
one to create a **user** unit for it (`~/.config/systemd/user`, no sudo; it
runs at boot thanks to lingering, which `logger install` enables). User units
can't bind ports below 1024, so use a higher port or a reverse proxy.

With auto-restart on (`w`, or `--watch` on `serviceman track`), the serviceman
daemon watches the unit's `ExecStart` binary (or `watch_path`, for services
started through an interpreter) and, when it is replaced:

- waits until the file is quiet for 1.5 s and executable, then compares a
  SHA-256 of it: `touch`/`chmod` alone do nothing;
- restarts the unit if it is running or failed, but leaves a unit you
  stopped alone (`skipped`);
- checks that it is still active 5 s later and reports `ok`/`failed` in
  the TUI (W column, detail pane) and in the daemon's journal
  (`journalctl --user -u serviceman-logger`).

`go build -o ~/.local/bin/app`, `make install` / `install(1)` and
rsync-style rename-into-place deploys are all detected (the parent
directory is watched). A binary replaced while the daemon was down is
restarted when the daemon comes back. System-scope units can be watched
too, but they are only restarted when `sudo -n` works.

The daemon also watches its own binary, so `make install` of serviceman restarts
it with the new version. After upgrading from a version without the binary
watcher, run `serviceman logger install` (or `L`) once.

## Log recording

`serviceman-logger.service` is a systemd **user** service running `serviceman daemon`
(log recorder + binary watcher). It follows each tracked unit's journal and appends to
`~/.local/share/serviceman/logs/<unit>.log`. Files rotate at 20 MB and 5 are kept
(`max_log_size_mb`, `max_log_files`). The daemon saves a journal cursor per
unit, so a restart neither loses nor duplicates lines. It picks up config
changes within 5 s.

`logger install` enables *lingering* so the recorder runs at boot without a
login. To read system units' journals you need the `adm` or `systemd-journal`
group.

## Files

- `~/.config/serviceman/config.json`: tracked services and settings (`serviceman paths` shows all locations)
- `~/.local/state/serviceman/cursors/`: the logger's journal cursors
- `~/.local/state/serviceman/watch.json`: auto-restart status, written by the daemon, read by the TUI

## Uninstalling

```
serviceman logger uninstall          # stop + remove serviceman-logger.service
rm -f ~/.local/bin/serviceman
rm -rf ~/.config/serviceman ~/.local/state/serviceman ~/.local/share/serviceman   # config, state, recorded logs
```

Services you tracked or created stay as they are: they are ordinary systemd
units, and serviceman only stored metadata about them. `logger install` enabled
lingering. User units still need it to start at boot, so only run
`loginctl disable-linger` if nothing else depends on it.
