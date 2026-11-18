package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	// Colors are the terminal's own ANSI palette, so the TUI follows its
	// theme (no hex values, no background detection).
	cAccent = lipgloss.Color("6") // cyan
	cGreen  = lipgloss.Color("2")
	cRed    = lipgloss.Color("1")
	cYellow = lipgloss.Color("3")
	cDim    = lipgloss.Color("15") // bright white

	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sHeader = lipgloss.NewStyle().Bold(true).Foreground(cDim)
	sDim    = lipgloss.NewStyle().Foreground(cDim)
	// Descriptions (of units, commands, form fields) are the only italic
	// text: dim, or normal color on the cursor row.
	sDesc    = sDim.Italic(true)
	sDescSel = lipgloss.NewStyle().Italic(true)
	sGreen   = lipgloss.NewStyle().Foreground(cGreen)
	sRed     = lipgloss.NewStyle().Foreground(cRed)
	sYellow  = lipgloss.NewStyle().Foreground(cYellow)
	sBold    = lipgloss.NewStyle().Bold(true)
	sKey     = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	// sChosen is the selected option of a selector (Yes/No …), green like
	// the ✓ beside it.
	sChosen = sGreen.Bold(true)
	// Highlights (cursor rows, search matches) are bold accent text, never a
	// background color.
	sHighlight = sKey
	sMatch     = sKey
)

// gutter is the left margin of every line. The cursor's ▸ sits in it, so
// content always starts at the same column.
const gutter = "  "

// rule is a separator line w cells wide, inset by the gutter on both sides
// so it lines up with the text.
func rule(w int) string { return ruleOf("─", w) }

// heavyRule is a thicker rule, for under the top bar.
func heavyRule(w int) string { return ruleOf("━", w) }

func ruleOf(line string, w int) string {
	return gutter + sDim.Render(strings.Repeat(line, max(0, w-2*len(gutter))))
}

// colGap separates the columns of every list: cells are cut to their width,
// so neighbouring columns never come closer than this.
const colGap = "  "

// cursorMark is the gutter of a row: ▸ when the cursor is on it.
func cursorMark(on bool) string {
	if on {
		return sKey.Render("▸ ")
	}
	return gutter
}

// pad truncates or right-pads s to exactly w cells.
func pad(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if n := ansi.StringWidth(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

// markedName is a row's name cell, w cells wide: the name (bold accent on
// the cursor row) followed by its mark, e.g. a green ✓. Long names are
// shortened so the mark always shows.
func markedName(name, mark string, w int, here bool) string {
	name = trunc(name, max(1, w-ansi.StringWidth(mark)))
	if here {
		name = sHighlight.Render(name)
	}
	return pad(name+mark, w)
}

// inputLead starts the input line under a field's label (accent while the
// field is focused).
func inputLead(focused bool) string {
	if focused {
		return " " + sKey.Render("┗ ")
	}
	return " " + sDim.Render("┗ ")
}

// choiceLabel is how a selector shows an option value: "on-failure" reads
// "On failure".
func choiceLabel(v string) string {
	v = strings.ReplaceAll(v, "-", " ")
	if v == "" {
		return v
	}
	return strings.ToUpper(v[:1]) + v[1:]
}

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// emacsKey turns bubbletea key names into Emacs notation.
func emacsKey(k string) string {
	r := strings.NewReplacer("ctrl+", "C-", "alt+", "M-", "shift+", "S-", "enter", "RET",
		"esc", "ESC", "tab", "TAB", "backspace", "DEL", " ", " ")
	return r.Replace(k)
}
