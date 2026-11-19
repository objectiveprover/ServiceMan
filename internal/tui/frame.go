package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
)

func (m *Model) footer() string {
	var b strings.Builder
	b.WriteString(rule(m.w) + "\n")
	switch {
	case m.prompt != nil:
		b.WriteString(m.prompt.view(m.w))
	case m.msg != "":
		st := sDim
		if m.msgErr {
			st = sRed
		}
		b.WriteString(trunc(gutter+st.Render(m.msg), m.w))
	default:
		b.WriteString(m.keyHints())
	}
	return b.String()
}

// keyHints is the bottom line when the echo area is empty: the main keys of
// the current mode.
func (m *Model) keyHints() string {
	var keys []string
	switch m.mode {
	case modePager:
		// Follow and Wrap show their keys in the top bar.
		keys = []string{"Back", "RET/q", "Search", "C-s", "Top/Bottom", "M-↑/M-↓"}
		if m.pager.filterFn != nil {
			keys[2] = "Filter"
		}
	case modePicker:
		keys = []string{"Back", "C-g", "Type to filter", "", "Track/Untrack", "RET", "Move", "C-n/C-p"}
	case modeBinPicker:
		keys = []string{"Back", "C-g", "Create/Edit service", "RET", "Delete unit", "C-d", "Move", "C-n/C-p", "Type to filter", ""}
	case modeForm:
		keys = []string{"Cancel", "C-g", "Install", "C-c C-c", "Emacs & install", "C-c C-e", "Field", "TAB/S-TAB", "Cycle choice", "SPC"}
		if m.form.edit != nil {
			keys[2], keys[4] = "Save", "Emacs & save"
		}
	default:
		keys = []string{"Help", "?", "Quit", "q", "Commands", "M-x", "Start/Stop", "s"}
		// System services are only tracked: e refuses them.
		if s := m.selected(); s != nil && s.Scope == config.User {
			keys = append(keys, "Edit", "e")
		}
		keys = append(keys, "Filter", "C-s", "Journal", "RET")
	}
	return hintLine(m.w, "", keys)
}

// hintLine renders "Name (key)   Name (key) …" (name/key pairs, like the
// details box labels) after an optional lead, dropping pairs from the end
// when they don't fit.
func hintLine(w int, lead string, kv []string) string {
	render := func(d, k string) string {
		if k == "" {
			return sDim.Render(d)
		}
		return sDim.Render(d+" (") + sKey.Render(k) + sDim.Render(")")
	}
	sep := sDim.Render("   ")
	out := gutter
	if lead != "" {
		out += sBold.Render(lead) + "   "
	}
	for i := 0; i+1 < len(kv); i += 2 {
		next := render(kv[i], kv[i+1])
		if i > 0 {
			next = sep + next
		}
		if i > 0 && ansi.StringWidth(out+next) > w {
			break
		}
		out += next
	}
	return trunc(out, w)
}

// topBar is the first line of every view plus the separator under it:
// SERVICEMAN, the view's name and its context on the left, the view's main
// indicators on the right. The left side gives way when space runs out.
func topBar(w int, view string, left []string, right string) string {
	items := []string{gutter + sTitle.Render("SERVICEMAN")}
	if view != "" {
		items = append(items, sBold.Render(view))
	}
	items = append(items, left...)
	if right != "" {
		right += gutter
	}
	// Four spaces between items; two when narrow.
	var l string
	for _, gap := range []string{"    ", "  "} {
		l = strings.Join(items, gap)
		if ansi.StringWidth(l)+ansi.StringWidth(right)+2 <= w {
			break
		}
	}
	l = trunc(l, max(0, w-ansi.StringWidth(right)-2))
	bar := l + strings.Repeat(" ", max(0, w-ansi.StringWidth(l)-ansi.StringWidth(right))) + right
	return bar + "\n" + heavyRule(w)
}
