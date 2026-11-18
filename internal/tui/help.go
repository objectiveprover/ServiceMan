package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
)

// helpSection is a heading and its rows on the Keybindings page; the filter
// keeps a heading while any of its rows match.
type helpSection struct {
	title string // "" for the paths at the bottom
	rows  []string
}

func (m *Model) showHelp() tea.Cmd {
	var secs []helpSection
	section := func(t string) { secs = append(secs, helpSection{title: t}) }
	row := func(r string) { secs[len(secs)-1].rows = append(secs[len(secs)-1].rows, r) }
	add := func(k, d string) { row(gutter + sKey.Render(pad(k, 20)) + colGap + d) }

	section("Navigation")
	add("C-n / n / ↓", "Next service")
	add("C-p / p / ↑", "Previous service")
	add("C-v / M-v", "Page down / up")
	add("M-↑ / M-↓", "First / last")
	add("C-g / ESC ESC", "Cancel, or clear the filter")
	add("ESC key", "Works like Meta (ESC x is the same as M-x)")
	add("C-x C-c", "Quit")
	section("Commands")
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
	row("")
	row(gutter + sDim.Render("Press M-x to see all available commands"))
	section("Log / status viewer")
	add("C-n C-p C-v M-v", "Scroll (also SPC / DEL)")
	add("M-↑ / M-↓", "Top / bottom")
	add("C-s / C-r", "Search forward / backward")
	add("f / w", "Turn following / line wrapping on or off")
	add("RET / q / C-g", "Back to the list")
	section("Keybindings (?)")
	add("C-s", "Filter (separate words with spaces)")
	add("C-g", "Clear the filter")
	add("RET / q", "Back to the list")
	section("Track Services (t)")
	add("type", "Filter (separate words with spaces)")
	add("C-n / RET", "Move from the filter into the list")
	add("RET", "Track or untrack the unit")
	add("C-g / ESC ESC", "Back to the services")
	section("New Service (c): pick a binary")
	add("type", "Filter the binaries in "+config.Tilde(m.cfg.BinDir))
	add("C-n / C-p", "Move from the filter to the binaries and \"Custom command or path\"")
	add("RET on a binary", "Open the filled-in form, or Edit Service if it already has a service")
	add("RET on Custom…", "Open the form for that command (a blank form if it's empty)")
	add("C-d on a binary", "Delete its systemd unit, tracked or not (asks first)")
	add("C-g", "Back")
	section("New Service / Edit Service (e) form")
	add("TAB C-n / S-TAB C-p", "Next / previous field")
	add("SPC / C-f / C-b", "Go through a choice field's options")
	add("C-c C-c", "Save the unit, reload systemd, enable and start it (or restart it when editing), and add it to the list")
	add("C-c C-e", "Same, but open the unit in Emacs first")
	add("C-c C-p", "Show or hide the unit preview")
	section("")
	row(sDim.Render("Config: " + config.Path()))
	row(sDim.Render("Log files: " + m.cfg.LogDir))
	row(sDim.Render("Watching state: " + config.WatchStatePath()))

	p := newPager("Keybindings", "")
	p.follow = false
	p.filterFn = func(q string) []string { return helpLines(secs, q) }
	p.setLines(p.filterFn(""))
	m.pager, m.mode = p, modePager
	return nil
}

// helpLines lays out the sections, keeping only the rows that match q (all
// its words, like the other filters) under their headings.
func helpLines(secs []helpSection, q string) []string {
	var lines []string
	for _, s := range secs {
		var rows []string
		for _, r := range s.rows {
			if matchAll(q, ansi.Strip(r)) {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		if s.title != "" {
			lines = append(lines, sBold.Render(s.title))
		}
		lines = append(lines, rows...)
	}
	return lines
}
