package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
)

func (m *Model) showHelp() tea.Cmd {
	var lines []string
	add := func(k, d string) { lines = append(lines, gutter+sKey.Render(pad(k, 20))+colGap+d) }
	lines = append(lines, sBold.Render("Navigation"))
	add("C-n / n / ↓", "Next service")
	add("C-p / p / ↑", "Previous service")
	add("C-v / M-v", "Page down / up")
	add("M-↑ / M-↓", "First / last")
	add("C-g / ESC ESC", "Cancel, or clear the filter")
	add("ESC key", "Works like Meta (ESC x is the same as M-x)")
	add("C-x C-c", "Quit")
	lines = append(lines, "", sBold.Render("Commands"))
	nameW := 0
	for _, c := range commands {
		if c.keys != "" {
			nameW = max(nameW, ansi.StringWidth(c.name))
		}
	}
	for _, c := range commands {
		if c.keys == "" {
			continue
		}
		var ks []string
		for _, k := range strings.Fields(c.keys) {
			ks = append(ks, emacsKey(k))
		}
		add(strings.Join(ks, " / "), sDim.Render(pad(c.name, nameW)+colGap)+c.desc)
	}
	lines = append(lines, gutter+sDim.Render("M-x shows these and more commands."))
	lines = append(lines, "", sBold.Render("Log / status viewer"))
	add("C-n C-p C-v M-v", "Scroll (also SPC / DEL)")
	add("M-↑ / M-↓", "Top / bottom")
	add("C-s / C-r", "Search forward / backward")
	add("f / w", "Turn following / line wrapping on or off")
	add("RET / q / C-g", "Back to the list")
	lines = append(lines, "", sBold.Render("Add Services (a)"))
	add("type", "Filter (separate words with spaces)")
	add("C-n / RET", "Move from the filter into the list")
	add("RET", "Track or untrack the unit")
	add("C-g / ESC ESC", "Back to the services")
	lines = append(lines, "", sBold.Render("New Service (c): pick a binary"))
	add("type", "Filter the binaries in "+config.Tilde(m.cfg.BinDir))
	add("C-n / C-p", "Move from the filter to the binaries and \"Custom command or path\"")
	add("RET on a binary", "Open the filled-in form, or Edit Service if it already has a service")
	add("RET on Custom…", "Open the form for that command (a blank form if it's empty)")
	add("C-g", "Back")
	lines = append(lines, "", sBold.Render("New Service / Edit Service (e) form"))
	add("TAB C-n / S-TAB C-p", "Next / previous field")
	add("SPC / C-f / C-b", "Go through a choice field's options")
	add("C-c C-c", "Save the unit, reload systemd, enable and start it (or restart it when editing), and add it to the list")
	add("C-c C-e", "Same, but open the unit in Emacs first")
	add("C-c C-p", "Show or hide the unit preview")
	lines = append(lines, "", sDim.Render("Config: "+config.Path()), sDim.Render("Log files: "+m.cfg.LogDir),
		sDim.Render("Watching state: "+config.WatchStatePath()))
	p := newPager("Keybindings", "")
	p.follow = false
	p.setLines(lines)
	m.pager, m.mode = p, modePager
	return nil
}
