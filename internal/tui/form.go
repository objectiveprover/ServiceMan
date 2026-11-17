package tui

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

type field struct {
	id      string
	label   string
	help    string
	ti      textinput.Model
	choices []string
	choice  int
}

func (f *field) value() string {
	if f.choices != nil {
		return f.choices[f.choice]
	}
	return strings.TrimSpace(f.ti.Value())
}

type form struct {
	fields []*field
	focus  int
	err    string
	title  string
	back   mode // where C-g returns to
}

// formPrefill seeds the new-service form (used by the bin picker).
type formPrefill struct {
	name, exec, wd string
	scope          config.Scope
	watch          bool
}

func (m *Model) openForm(pre *formPrefill) tea.Cmd {
	cwd, _ := os.Getwd()
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	text := func(id, label, help, placeholder, initial string) *field {
		ti := newTextInput()
		ti.Placeholder = placeholder
		ti.SetValue(initial)
		return &field{id: id, label: label, help: help, ti: ti}
	}
	choice := func(id, label, help string, choices ...string) *field {
		return &field{id: id, label: label, help: help, choices: choices}
	}
	f := &form{fields: []*field{
		text("name", "Name", "unit name; .service is appended", "myapp", ""),
		text("desc", "Description", "", "My web app", ""),
		choice("scope", "Scope", "system: /etc/systemd/system (sudo) · user: ~/.config/systemd/user", "system", "user"),
		text("exec", "ExecStart", "command to run; relative commands are resolved via PATH", "/usr/bin/node server.js --port 3000", ""),
		text("wd", "WorkingDirectory", "", "/srv/myapp", cwd),
		text("user", "Run as User", "system scope only", "www-data", username),
		text("env", "Environment", "space-separated KEY=VALUE pairs", "PORT=3000 NODE_ENV=production", ""),
		text("envfile", "EnvironmentFile", "optional; secrets live in ~/.secrets/<binary>.env (prefix - to ignore if missing)", "~/.secrets/myapp.env", ""),
		choice("restart", "Restart", "", "on-failure", "always", "no"),
		choice("network", "Wait for network", "After=network-online.target", "yes", "no"),
		text("url", "Health URL", "serviceman only: polled for the HEALTH column", "http://localhost:3000/health", ""),
		text("config", "App config file", "serviceman only; empty = ~/.config/<binary>/config.* when it exists", "~/.config/myapp/config.toml", ""),
		choice("watch", "Watching", "restart when the ExecStart binary is replaced (needs the serviceman daemon; D turns it on)", "yes", "no"),
		choice("enable", "Enable at boot", "", "yes", "no"),
		choice("start", "Start now", "", "yes", "no"),
	}, title: " New service"}
	f.get("watch").choice = 1 // off unless coming from the bin picker
	if pre != nil {
		f.get("name").ti.SetValue(pre.name)
		f.get("exec").ti.SetValue(pre.exec)
		f.get("wd").ti.SetValue(pre.wd)
		if pre.scope == config.User {
			f.get("scope").choice = 1
		}
		if pre.watch {
			f.get("watch").choice = 0
		}
		bin := filepath.Base(pre.exec)
		if p := config.DefaultAppConfig(bin); p != "" {
			f.get("config").ti.SetValue(config.Tilde(p))
		}
		if p := config.DefaultEnvFile(bin); config.EnvFileProblem(p) != "missing" {
			f.get("envfile").ti.SetValue(config.Tilde(p))
		}
		f.title = " New service for " + config.Tilde(pre.exec)
		f.back = modeBinPicker
	}
	m.form, m.mode = f, modeForm
	f.setFocus(0)
	m.setMsg("Fill in the fields; C-c C-c to install, C-c C-e to review in Emacs first")
	return nil
}

func (f *form) get(id string) *field {
	for _, fl := range f.fields {
		if fl.id == id {
			return fl
		}
	}
	return nil
}

// disabled reports whether field i does not apply (user scope).
func (f *form) disabled(i int) bool {
	id := f.fields[i].id
	return (id == "user" || id == "network") && f.get("scope").value() == "user"
}

// move focuses the next applicable field in direction d.
func (f *form) move(d int) {
	i := f.focus
	for range f.fields {
		i = (i + d + len(f.fields)) % len(f.fields)
		if !f.disabled(i) {
			break
		}
	}
	f.setFocus(i)
}

func (f *form) setFocus(i int) {
	f.focus = (i + len(f.fields)) % len(f.fields)
	for j, fl := range f.fields {
		if fl.choices == nil {
			if j == f.focus {
				fl.ti.Focus()
			} else {
				fl.ti.Blur()
				fl.ti.CursorStart()
			}
		}
	}
}

func (f *form) unit() systemd.NewUnit {
	scope := config.Scope(f.get("scope").value())
	return systemd.NewUnit{
		Name:        f.get("name").value(),
		Description: f.get("desc").value(),
		Scope:       scope,
		ExecStart:   f.get("exec").value(),
		WorkingDir:  f.get("wd").value(),
		RunAs:       f.get("user").value(),
		Environment: f.get("env").value(),
		EnvFile:     config.ExpandEnvFile(f.get("envfile").value()),
		Restart:     f.get("restart").value(),
		Network:     f.get("network").value() == "yes",
		Enable:      f.get("enable").value() == "yes",
		Start:       f.get("start").value() == "yes",
	}
}

func (f *form) validate() error {
	u := f.unit()
	if u.Name == "" {
		return fmt.Errorf("Name is required")
	}
	if !config.ValidUnitName(u.UnitName()) || strings.Contains(u.UnitName(), "/") {
		return fmt.Errorf("invalid unit name %q", u.UnitName())
	}
	if u.ExecStart == "" {
		return fmt.Errorf("ExecStart is required")
	}
	if u.Exists() {
		return fmt.Errorf("%s already exists in %s — track it with `a` instead", u.UnitName(), systemd.UnitDir(u.Scope))
	}
	return nil
}

func (f *form) key(m *Model, msg tea.KeyMsg) tea.Cmd {
	cur := f.fields[f.focus]
	switch k := msg.String(); k {
	case "ctrl+g", "esc":
		m.form, m.mode = nil, f.back
		m.setMsg("Cancelled")
		return nil
	case "tab", "ctrl+n", "down":
		f.move(1)
		return nil
	case "shift+tab", "ctrl+p", "up":
		f.move(-1)
		return nil
	case "enter":
		if cur.choices != nil {
			cur.choice = (cur.choice + 1) % len(cur.choices)
		} else {
			f.move(1)
		}
		return nil
	}
	if cur.choices != nil {
		switch msg.String() {
		case " ", "ctrl+f", "right":
			cur.choice = (cur.choice + 1) % len(cur.choices)
		case "ctrl+b", "left":
			cur.choice = (cur.choice - 1 + len(cur.choices)) % len(cur.choices)
		}
		return nil
	}
	var cmd tea.Cmd
	cur.ti, cmd = cur.ti.Update(msg)
	return cmd
}

// prefixKey handles C-c chords.
func (f *form) prefixKey(m *Model, combo string) (tea.Cmd, bool) {
	switch combo {
	case "ctrl+c ctrl+c":
		return f.submit(m, false), true
	case "ctrl+c ctrl+e":
		return f.submit(m, true), true
	}
	return nil, false
}

func (f *form) submit(m *Model, editFirst bool) tea.Cmd {
	if err := f.validate(); err != nil {
		f.err = err.Error()
		m.setErr("%s", f.err)
		return nil
	}
	f.err = ""
	u := f.unit()
	url := f.get("url").value()
	if url != "" && !strings.Contains(url, "://") {
		url = "http://" + url
	}
	cmd, err := u.InstallCmd(m.editor(), editFirst)
	if err != nil {
		m.setErr("%v", err)
		return nil
	}
	svc := config.Service{Unit: u.UnitName(), Scope: u.Scope, URL: url,
		Watch: f.get("watch").value() == "yes", ConfigPath: config.ExpandTilde(f.get("config").value())}
	return m.execInteractive(cmd, "install "+svc.Unit, func(m *Model) tea.Cmd {
		m.form, m.mode = nil, modeList
		if m.updateConfig(func(c *config.Config) { c.Track(svc) }) == nil {
			m.filter = ""
			m.cursor = m.cfg.Index(svc.Unit, svc.Scope)
			m.setMsg("Installed and now tracking %s", svc.Unit)
			if svc.Watch && m.loggerState != "active" {
				m.setMsg("Installed %s — watching needs the serviceman daemon (it's %s; D turns it on)", svc.Unit, m.loggerState)
			}
		}
		return tea.Batch(m.refreshCmd(), m.healthCmd())
	})
}

func (f *form) view(w, h int) string {
	labelW := 18
	var left []string
	titleW := w
	if w >= 110 {
		titleW = w/2 - 1
	}
	left = append(left, sTitle.Render(trunc(f.title, titleW)), "")
	scope := f.get("scope").value()
	for i, fl := range f.fields {
		label := pad(fl.label, labelW)
		focused := i == f.focus
		if focused {
			label = sKey.Render("▸ " + label)
		} else {
			label = "  " + sDim.Render(label)
		}
		var val string
		if fl.choices != nil {
			var opts []string
			for j, c := range fl.choices {
				if j == fl.choice {
					if focused {
						opts = append(opts, sSelected.Render(" "+c+" "))
					} else {
						opts = append(opts, sBold.Render("["+c+"]"))
					}
				} else {
					opts = append(opts, sDim.Render(" "+c+" "))
				}
			}
			val = strings.Join(opts, " ")
		} else {
			if tw := max(10, min(60, w/2-labelW-6)); fl.ti.Width != tw {
				fl.ti.Width = tw
				// Recompute the visible window: a value set while Width was 0
				// would otherwise render in full and wrap.
				pos := fl.ti.Position()
				fl.ti.CursorEnd()
				fl.ti.SetCursor(pos)
			}
			val = fl.ti.View()
		}
		if (fl.id == "user" || fl.id == "network") && scope == "user" {
			val = sDim.Render("(n/a for user scope)")
		}
		left = append(left, label+" "+val)
		if focused && fl.help != "" {
			left = append(left, "    "+sDim.Render(trunc(fl.help, w/2)))
		}
	}
	if f.err != "" {
		left = append(left, "", " "+sRed.Render(f.err))
	}
	left = append(left, "", sDim.Render(" TAB/C-n next · S-TAB/C-p prev · SPC cycle"),
		sDim.Render(" C-c C-c install · C-c C-e review in Emacs"))

	u := f.unit()
	if u.Name == "" {
		u.Name = "myapp"
	}
	preview := []string{sHeader.Render("Preview: " + systemd.UnitDir(u.Scope) + "/" + u.UnitName()), ""}
	for _, l := range strings.Split(u.Render(), "\n") {
		if strings.HasPrefix(l, "[") {
			l = sKey.Render(l)
		}
		preview = append(preview, l)
	}

	leftS := strings.Join(left, "\n")
	if w >= 110 {
		lw := w / 2
		return lipgloss.JoinHorizontal(lipgloss.Top,
			lipgloss.NewStyle().Width(lw).MaxWidth(lw).Render(leftS),
			lipgloss.NewStyle().MaxWidth(w-lw).Render(strings.Join(preview, "\n")))
	}
	if len(left)+len(preview)+1 <= h {
		return leftS + "\n\n" + strings.Join(preview, "\n")
	}
	return leftS
}
