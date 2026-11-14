# svcman

A terminal UI for the systemd services *you* care about (your web apps plus
anything else you pick). It also records their logs continuously. Navigation
uses Emacs keys.

```
make install                 # -> ~/.local/bin/svcman
svcman logger install        # start the log recorder (systemd user service)
svcman                       # open the TUI
```

## Workflow

| Want to…                     | How                                                              |
|------------------------------|------------------------------------------------------------------|
| Track an existing service    | `a` in the TUI (filterable picker, `RET` toggles), or `svcman track nginx myapp` |
| Create a new service         | `+` opens a form with a live unit-file preview; `C-c C-c` installs it, `C-c C-e` opens it in Emacs first. It's tracked automatically. |
| Edit a unit file             | `e` opens it in Emacs and runs `daemon-reload` afterwards. Vendor units (`/usr/lib/...`) get a drop-in `override.conf` instead; `E` always edits the drop-in. |
| Watch logs live              | `RET` follows the journal (`C-s` isearch, `f` follow, `w` wrap)  |
| Read recorded logs           | `v` opens the recorded file in Emacs                             |
| Health-check a web service   | `u` sets a URL; the HEALTH column polls it every 10 s; `o` opens it |

**Editor:** uses `emacsclient -t` when an Emacs server is running, otherwise
`emacs -nw`. Override it with `"editor"` in the config. Root-owned unit files
are edited through `sudoedit`, so Emacs runs as you with your own init file.

**sudo:** system-unit actions first try `sudo -n` (works when credentials are
cached). If sudo needs a password, svcman hands the terminal over for the
prompt and then returns to the TUI. User-scope units never need sudo.

## Keys

`C-n`/`C-p` move · `C-v`/`M-v` page · `M-<`/`M->` ends · `C-s` filter ·
`C-g` cancel · `M-x` run a command by name · `?` help · `C-x C-c` or `q` quit

`s` start · `S` stop · `r` restart · `R` reload · `t` enable/disable ·
`D` daemon-reload · `i` status · `x` recording on/off · `C-k` untrack ·
`M-p`/`M-n` reorder · `g` refresh · `L` install/restart the logger

## Log recording

`svcman-logger.service` is a systemd **user** service running `svcman daemon`.
It follows each tracked unit's journal and appends to
`~/.local/share/svcman/logs/<unit>.log`. Files rotate at 20 MB and 5 are kept
(`max_log_size_mb`, `max_log_files`). The daemon saves a journal cursor per
unit, so a restart neither loses nor duplicates lines. It picks up config
changes within 5 s.

`logger install` enables *lingering* so the recorder runs at boot without a
login. To read system units' journals you need the `adm` or `systemd-journal`
group.

## Files

- `~/.config/svcman/config.json`: tracked services and settings (`svcman paths` shows all locations)
- `~/.local/state/svcman/cursors/`: the logger's journal cursors
