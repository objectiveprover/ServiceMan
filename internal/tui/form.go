package tui

import (
	"fmt"
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

// setChoice selects v, adding it as an extra choice when it isn't one (so
// editing a unit keeps e.g. Restart=on-abnormal).
func (f *field) setChoice(v string) {
	for i, c := range f.choices {
		if c == v {
			f.choice = i
			return
		}
	}
	f.choices = append(f.choices, v)
	f.choice = len(f.choices) - 1
}

func (f *field) setBool(on bool) { f.setChoice(map[bool]string{true: "yes", false: "no"}[on]) }

// form is the New Service and Edit Service view.
type form struct {
	fields  []*field
	focus   int
	offset  int // first visible line of the (scrolling) field list
	err     string
	title   string          // view name in the top bar
	context string          // shown dim next to it
	back    mode            // where C-g returns to
	edit    *config.Service // the service being edited; nil for a new one
	scope   config.Scope    // always user for a new service
	wd      string          // WorkingDirectory= of the unit being edited, kept as is
}

// formPrefill seeds the new-service form (used by the New Service picker).
type formPrefill struct {
	name, exec string
	watch      bool
}

// newFields builds the form's fields. Run as User and Wait for network only
// apply to system units, which can be edited but not created.
func newFields(scope config.Scope) []*field {
	username := ""
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	text := func(id, label, help, initial string) *field {
		ti := newTextInput()
		ti.SetValue(initial)
		return &field{id: id, label: label, help: help, ti: ti}
	}
	choice := func(id, label, help string, choices ...string) *field {
		return &field{id: id, label: label, help: help, choices: choices}
	}
	system := scope == config.System
	var fields []*field
	add := func(fl *field, ok bool) {
		if ok {
			fields = append(fields, fl)
		}
	}
	add(text("name", "Name", "The unit's name; .service is added for you", ""), true)
	add(text("desc", "Description", "", ""), true)
	add(text("exec", "ExecStart", "The command to run; commands without a path are looked up in PATH", ""), true)
	add(text("user", "Run as User", "", username), system)
	add(text("env", "Environment", "KEY=VALUE pairs separated by spaces", ""), true)
	add(text("envfile", "EnvironmentFile", "Optional file with secrets, usually ~/.secrets/<binary>.env (start it with - to skip it when missing)", ""), true)
	add(choice("restart", "Restart", "", "on-failure", "always", "no"), true)
	add(choice("network", "Wait for network", "Start only after the network is up (After=network-online.target)", "yes", "no"), system)
	return append(fields,
		text("url", "Health URL", "Checked regularly to fill the HEALTH column (used only by serviceman)", ""),
		text("config", "App config file", "The app's own config; when empty, ~/.config/<binary>/config.* if it exists (used only by serviceman)", ""),
		choice("watch", "Watching", "Restart the service when its binary is replaced (needs the serviceman daemon; D turns it on)", "yes", "no"),
		choice("enable", "Enable at boot", "", "yes", "no"),
		choice("start", "Start now", "", "yes", "no"),
	)
}

// formBack is where C-g leaves a form: the New Service picker when it was
// opened from there.
func (m *Model) formBack() mode {
	if m.bins != nil {
		return modeBinPicker
	}
	return modeList
}

func (m *Model) openForm(pre *formPrefill) tea.Cmd {
	f := &form{fields: newFields(config.User), title: "New Service", back: m.formBack(), scope: config.User}
	f.get("watch").choice = 1 // off unless coming from a binary
	if pre != nil {
		f.get("name").ti.SetValue(pre.name)
		f.get("desc").ti.SetValue(pre.name)
		f.get("exec").ti.SetValue(pre.exec)
		if pre.watch {
			f.get("watch").choice = 0
		}
		if bin := filepath.Base(strings.Fields(pre.exec + " x")[0]); bin != "." && bin != "/" {
			if p := config.DefaultAppConfig(bin); p != "" {
				f.get("config").ti.SetValue(config.Tilde(p))
			}
			if p := config.DefaultEnvFile(bin); config.EnvFileProblem(p) != "missing" {
				f.get("envfile").ti.SetValue(config.Tilde(p))
			}
		}
		f.context = config.Tilde(pre.exec)
	}
	m.form, m.mode = f, modeForm
	f.setFocus(0)
	return nil
}

// openEditForm opens the Edit Service form for a tracked service, filled in
// from its unit file. Units the form can't represent without losing
// settings (vendor units, unknown directives) open in Emacs instead.
func (m *Model) openEditForm(svc config.Service) tea.Cmd {
	st := m.status[svcKey(svc)]
	path := st.FragmentPath
	if st.LoadState == "not-found" || path == "" || systemd.IsVendorPath(path) {
		return m.edit(&svc, false, "")
	}
	u, unsupported, err := systemd.ParseUnit(path, svc.Scope)
	switch {
	case err != nil:
		return m.edit(&svc, false, err.Error())
	case len(unsupported) > 0:
		why := "the form doesn't cover " + trunc(unsupported[0], 40)
		if n := len(unsupported) - 1; n > 0 {
			why += fmt.Sprintf(" and %d more", n)
		}
		return m.edit(&svc, false, why)
	}

	f := &form{fields: newFields(svc.Scope), title: "Edit Service", context: svc.Unit + " (" + string(svc.Scope) + ")",
		back: m.formBack(), edit: &svc, scope: svc.Scope}
	f.get("name").ti.SetValue(svc.Unit)
	f.get("name").help = "Can't be changed while editing"
	f.get("desc").ti.SetValue(u.Description)
	f.get("exec").ti.SetValue(u.ExecStart)
	f.wd = u.WorkingDir
	if fl := f.get("user"); fl != nil {
		fl.ti.SetValue(u.RunAs)
	}
	f.get("env").ti.SetValue(u.Environment)
	envFile := config.Tilde(strings.TrimPrefix(u.EnvFile, "-"))
	if strings.HasPrefix(u.EnvFile, "-") {
		envFile = "-" + envFile
	}
	f.get("envfile").ti.SetValue(envFile)
	f.get("restart").setChoice(u.Restart)
	if fl := f.get("network"); fl != nil {
		fl.setBool(u.Network)
	}
	f.get("url").ti.SetValue(svc.URL)
	f.get("config").ti.SetValue(config.Tilde(svc.ConfigPath))
	f.get("watch").setBool(svc.Watch)
	f.get("enable").setBool(st.UnitFileState == "enabled")
	start := f.get("start")
	start.label, start.help = "Restart now", "Apply the changes right away (starts the service if it's stopped)"
	switch st.ActiveState {
	case "active", "activating", "reloading":
		start.setBool(true)
	default:
		start.setBool(false)
	}
	m.form, m.mode = f, modeForm
	f.setFocus(1) // Name is read-only
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

// val is the value of field id, or "" when the form doesn't have it.
func (f *form) val(id string) string {
	if fl := f.get(id); fl != nil {
		return fl.value()
	}
	return ""
}

// disabled reports whether field i is read-only (the name while editing).
func (f *form) disabled(i int) bool {
	return f.fields[i].id == "name" && f.edit != nil
}

// move focuses the next applicable field in direction d, stopping at the
// first and last fields (no wrapping around).
func (f *form) move(d int) {
	for i := f.focus + d; i >= 0 && i < len(f.fields); i += d {
		if !f.disabled(i) {
			f.setFocus(i)
			return
		}
	}
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
	return systemd.NewUnit{
		Name:        f.get("name").value(),
		Description: f.get("desc").value(),
		Scope:       f.scope,
		ExecStart:   f.get("exec").value(),
		WorkingDir:  f.wd,
		RunAs:       f.val("user"),
		Environment: f.get("env").value(),
		EnvFile:     config.ExpandEnvFile(f.get("envfile").value()),
		Restart:     f.get("restart").value(),
		Network:     f.val("network") == "yes",
		Enable:      f.get("enable").value() == "yes",
		Start:       f.get("start").value() == "yes",
		Edit:        f.edit != nil,
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
	if f.edit == nil && u.Exists() {
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
	case "ctrl+c ctrl+p":
		m.hidePreview = !m.hidePreview
		return nil, true
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
	verb, done := "install", "Installed and now tracking"
	if f.edit != nil {
		svc.Unit = f.edit.Unit // as tracked, even if normalizing would differ
		verb, done = "save", "Saved"
	}
	edit := f.edit != nil
	return m.execInteractive(cmd, verb+" "+svc.Unit, func(m *Model) tea.Cmd {
		m.form, m.bins, m.mode = nil, nil, modeList
		if m.updateConfig(func(c *config.Config) {
			if i := c.Index(svc.Unit, svc.Scope); edit && i >= 0 {
				c.Services[i].URL, c.Services[i].Watch, c.Services[i].ConfigPath = svc.URL, svc.Watch, svc.ConfigPath
			} else {
				c.Track(svc)
			}
		}) == nil {
			m.filter = ""
			m.cursor = m.cfg.Index(svc.Unit, svc.Scope)
			m.setMsg("%s %s", done, svc.Unit)
			if svc.Watch && m.loggerState != "active" {
				m.setMsg("%s %s — watching needs the serviceman daemon (it's %s; D turns it on)", done, svc.Unit, m.loggerState)
			}
		}
		return tea.Batch(m.refreshCmd(), m.healthCmd())
	})
}

// fieldLines renders the fields for a column w wide: per field a label line
// (name and gray description), the input below it (after a ❯ for text
// fields, a ┗ for choices) and a ─ line under it, except under the last
// field. start and end are the lines to keep on screen: the focused field's
// first line and the line after its last, stretched to the top (bottom) of
// the list when no field above (below) it can be focused, so read-only
// fields there don't stay hidden behind a "more" marker.
func (f *form) fieldLines(w int) (lines []string, start, end int) {
	first, last := -1, -1
	for i := range f.fields {
		if !f.disabled(i) {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	for i, fl := range f.fields {
		focused := i == f.focus
		disabled := f.disabled(i)
		bottom := i < len(f.fields)-1
		label := sBold.Render(fl.label)
		switch {
		case focused:
			label = sHighlight.Render(fl.label)
			start = len(lines)
			if i == first {
				start = 0
			}
		case disabled:
			label = sDim.Render(fl.label)
		}

		if fl.choices == nil {
			var val string
			if disabled {
				val = sDim.Render(fl.value())
			} else {
				if tw := inputTextWidth(w); fl.ti.Width != tw {
					fl.ti.Width = tw
					// Recompute the visible window: a value set while Width was 0
					// would otherwise render in full and wrap.
					pos := fl.ti.Position()
					fl.ti.CursorEnd()
					fl.ti.SetCursor(pos)
				}
				val = fl.ti.View()
			}
			tf := textField(cursorMark(focused), label, fl.help, val, focused, w)
			if !bottom {
				tf = tf[:2]
			}
			lines = append(lines, tf...)
		} else {
			head := cursorMark(focused) + label
			if fl.help != "" {
				head += "  " + sDesc.Render(fl.help)
			}
			var val string
			if disabled {
				val = sDim.Render(choiceLabel(fl.value()))
			} else {
				// The chosen option is bold green, like its ✓; the others keep a
				// blank in the ✓'s place so labels don't shift.
				var opts []string
				for j, c := range fl.choices {
					c = choiceLabel(c)
					switch {
					case j != fl.choice:
						opts = append(opts, sDim.Render(c)+"  ")
					default:
						opts = append(opts, sChosen.Render(c)+" "+sGreen.Render("✓"))
					}
				}
				val = strings.Join(opts, "   ")
			}
			lines = append(lines, trunc(head, w), trunc(gutter+inputLead(focused)+val, w))
			if bottom {
				lines = append(lines, fieldBottom(focused, w))
			}
		}
		if focused {
			end = len(lines)
		}
	}
	if f.focus == last {
		end = len(lines)
	}
	return lines, start, end
}

// scroll returns the h lines of the field list to show, keeping lines start
// to end (the focused field, see fieldLines) visible; ↑/↓ markers show there
// is more.
func (f *form) scroll(lines []string, start, end, h int) []string {
	if len(lines) <= h {
		f.offset = 0
		return lines
	}
	margin := 1 // room for the ↑/↓ markers
	if h < 5 {
		margin = 0
	}
	if f.offset > start-margin {
		f.offset = start - margin
	}
	if bottom := end + margin; bottom > f.offset+h {
		f.offset = bottom - h
	}
	f.offset = max(0, min(f.offset, len(lines)-h))
	out := append([]string(nil), lines[f.offset:f.offset+h]...)
	if margin > 0 {
		if f.offset > 0 {
			out[0] = gutter + sDim.Render("↑ more")
		}
		if f.offset+h < len(lines) {
			out[h-1] = gutter + sDim.Render("↓ more")
		}
	}
	return out
}

// view is the form screen: the fields, and the unit preview unless C-c C-p
// hid it. Wide screens show the preview beside the fields, narrow ones
// below them, in up to half the height.
func (f *form) view(m *Model, w, h int) string {
	var ctx []string
	if f.context != "" {
		ctx = append(ctx, sDim.Render(f.context))
	}
	right := onOff("Preview", "C-c C-p", !m.hidePreview) + "    " + m.daemonCell()
	out := []string{topBar(w, f.title, ctx, right)}
	h -= 2
	if f.err != "" {
		out = append(out, trunc(gutter+sRed.Render(f.err), w))
		h--
	}
	h = max(1, h)

	var preview []string
	if !m.hidePreview {
		preview = f.previewLines()
	}
	switch {
	case preview == nil || w < 110 && h/2 < 4:
		// No preview, or no room for one below the fields.
		lines, start, end := f.fieldLines(w - len(colGap))
		return strings.Join(append(out, f.scroll(lines, start, end, h)...), "\n")
	case w < 110:
		// Below the fields, after a blank line.
		ph := min(len(preview)+1, h/2)
		lines, start, end := f.fieldLines(w - len(colGap))
		fields := f.scroll(lines, start, end, h-ph)
		for len(fields) < h-ph {
			fields = append(fields, "")
		}
		out = append(out, fields...)
		out = append(out, "")
		for _, l := range preview[:ph-1] {
			out = append(out, trunc(gutter+l, w))
		}
		return strings.Join(out, "\n")
	}
	colW := w / 2
	lines, start, end := f.fieldLines(colW - len(colGap))
	left := strings.Join(f.scroll(lines, start, end, h), "\n")
	if len(preview) > h {
		preview = preview[:h]
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(colW).MaxWidth(colW).Render(left),
		lipgloss.NewStyle().MaxWidth(w-colW).Render(strings.Join(preview, "\n")))
	return strings.Join(append(out, body), "\n")
}

// previewLines is the unit file the form would write, under a header with
// its path.
func (f *form) previewLines() []string {
	u := f.unit()
	if u.Name == "" {
		u.Name = "myapp"
	}
	preview := []string{sHeader.Render("Preview: " + config.Tilde(systemd.UnitDir(u.Scope)+"/"+u.UnitName())), ""}
	for _, l := range strings.Split(strings.TrimRight(u.Render(), "\n"), "\n") {
		if strings.HasPrefix(l, "[") {
			l = sKey.Render(l)
		}
		preview = append(preview, l)
	}
	return preview
}
