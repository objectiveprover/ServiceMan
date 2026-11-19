# ServiceMan

A terminal UI for the systemd services *you* care about: your own apps plus
anything else you pick. It saves their logs to files and can restart them when
you deploy a new binary. Navigation uses Emacs keys.

---

<img width="1090" height="820" alt="image" src="https://github.com/user-attachments/assets/da6db6dc-f003-4cef-8f4d-144613855027" />

---

## Quick start

```
make install                 # installs ~/.local/bin/serviceman and ~/.config/serviceman/config.json
serviceman daemon install    # starts the ServiceMan daemon (logging + watching)
serviceman                   # opens the TUI
```

- If a config already exists, `make install` asks whether to **keep** it (default),
  **update** it with new settings, or **replace** it (a backup is saved first).
- `make uninstall` removes ServiceMan; see [Uninstalling](#uninstalling).

## What it does

- Shows only the services you track, with their state, boot setting and health.
- Creates and edits user units through a form, with an optional preview of the unit file.
- Follows a service's journal live and saves its logs to files.
- **Watching:** restarts a service when its binary is replaced, so a deploy is just
  `make install` or `go build -o ~/.local/bin/app`.
- Polls a health-check URL per service.

---

<img width="1327" height="927" alt="image" src="https://github.com/user-attachments/assets/19b36ffe-bab1-4be6-94b8-9b18aa2f5be2" />

---

## Common tasks

| Want to…                              | How                                               |
|---------------------------------------|---------------------------------------------------|
| Track existing services               | `t`, or `serviceman track nginx myapp`            |
| Run a binary from `~/.local/bin`      | `c`, pick the binary, `C-c C-c` to install        |
| Run any other command as a service    | `c`, then **Custom command or path**              |
| Edit a service                        | `e`, then `C-c C-c` to save                       |
| Watch logs live                       | `RET`                                             |
| Restart it on every deploy            | `w` (watching on)                                 |
| Check that a web service is up        | `M-x` **Set URL**; the HEALTH column shows it     |
| Give a service its secrets            | `M-x` **Set env file**                            |
| Open the app's own config             | `M-x` **Edit app config**                         |

ServiceMan creates and edits only **user** units (no sudo). System units can be
tracked, started, stopped and restarted, but not edited; ServiceMan asks for your
sudo password when it needs it.

## Keys

<img width="1256" height="927" alt="image" src="https://github.com/user-attachments/assets/296f3e15-f01d-4dfa-954f-92577d3d0d31" />


| Key              | Does                                   |
|------------------|----------------------------------------|
| `C-n` / `C-p`    | Next / previous                        |
| `C-s`            | Filter                                 |
| `C-g`            | Cancel                                 |
| `t`              | Track existing services                |
| `c`              | Create a service                       |
| `e`              | Edit the service                       |
| `s`              | Start / stop                           |
| `r`              | Restart                                |
| `RET`            | Follow the journal                     |
| `w`              | Watching on / off                      |
| `D`              | ServiceMan daemon on / off             |
| `C-c C-c`        | Save (in the New / Edit Service form)  |
| `q`              | Quit                                   |

**Every other command is in `M-x`**: type to find it by name. `?` lists all keys.
`ESC` works as Meta (`ESC x` = `M-x`).

## The ServiceMan daemon

- A user service, `serviceman-daemon.service`, that does the logging and watching.
- Turn it on with `serviceman daemon install` or `D` in the TUI.
- Logs go to `~/.local/share/serviceman/logs/<unit>.log` and are rotated by size.
- With watching on, it restarts the service when its binary is replaced (unless you
  stopped the service) and the details pane shows whether the restart worked.

## Conventions

ServiceMan fills in sensible defaults when your apps follow these:

- Binaries live in `~/.local/bin`; `c` lists them.
- An app's own config lives in `~/.config/<binary>/`.
- An app's secrets live in `~/.secrets/<binary>.env`; the New Service form uses it
  as the service's .env file when it exists.

<img width="1088" height="849" alt="image" src="https://github.com/user-attachments/assets/51d850a0-29af-466a-b29d-e48bf4183a3f" />

## Configuration

The config is `~/.config/serviceman/config.json`:

| Setting           | Meaning                                                        |
|-------------------|----------------------------------------------------------------|
| `editor`          | Editor command; empty uses Emacs (`emacsclient -t` if a server is running) |
| `bin_dir`         | Where `c` looks for binaries (`~/.local/bin`)                  |
| `log_dir`         | Where log files go                                             |
| `max_log_size_mb` | Rotate a log file at this size (20)                            |
| `max_log_files`   | Rotated files to keep (5)                                      |
| `services`        | Tracked services (managed by ServiceMan)                       |

```
serviceman config version    # your config's version vs. the current one
serviceman config update     # add new settings (never changes existing ones)
serviceman config path       # where the config is
serviceman config template   # the default config
```

## Command line

```
serviceman track [--user|--system] [--url URL] [--watch] [--config PATH] UNIT...
serviceman untrack UNIT...
serviceman list
serviceman daemon install | uninstall | status
serviceman paths             # config, log and state locations
```

## Uninstalling

```
make uninstall                                     # removes the daemon and the binary
rm -rf ~/.config/serviceman ~/.local/state/serviceman ~/.local/share/serviceman   # config, state, logs
```

Services you tracked or created are left as they are.

## License

See [LICENSE](LICENSE).
