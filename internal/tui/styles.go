package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	cAccent = lipgloss.AdaptiveColor{Light: "#5a3fc0", Dark: "#b7a4ff"}
	cGreen  = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#7ee787"}
	cRed    = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#ff7b72"}
	cYellow = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#e3b341"}
	cDim    = lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"}

	sTitle    = lipgloss.NewStyle().Bold(true).Foreground(cAccent)
	sHeader   = lipgloss.NewStyle().Bold(true).Foreground(cDim)
	sDim      = lipgloss.NewStyle().Foreground(cDim)
	sGreen    = lipgloss.NewStyle().Foreground(cGreen)
	sRed      = lipgloss.NewStyle().Foreground(cRed)
	sYellow   = lipgloss.NewStyle().Foreground(cYellow)
	sBold     = lipgloss.NewStyle().Bold(true)
	sSelected = lipgloss.NewStyle().Reverse(true)
	sMatch    = lipgloss.NewStyle().Background(cYellow).Foreground(lipgloss.Color("#000000"))
	sKey      = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
)

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
