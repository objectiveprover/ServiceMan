package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// The TUI uses the terminal's ANSI palette, so the terminal's theme decides
// the actual colors: styles emit basic SGR codes, never 24-bit hex colors.
func TestTerminalPalette(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)
	for name, c := range map[string]struct {
		got, sgr string
	}{
		"accent": {sKey.Render("x"), "36"},
		"green":  {sGreen.Render("x"), "32"},
		"red":    {sRed.Render("x"), "31"},
		"yellow": {sYellow.Render("x"), "33"},
		"dim":    {sDim.Render("x"), "97"},
	} {
		if strings.Contains(c.got, "38;2;") {
			t.Errorf("%s uses a 24-bit color: %q", name, c.got)
		}
		if !strings.Contains(c.got, c.sgr+"m") && !strings.Contains(c.got, ";"+c.sgr) && !strings.Contains(c.got, "["+c.sgr) {
			t.Errorf("%s = %q, want ANSI SGR %s", name, c.got, c.sgr)
		}
	}
	// Only descriptions are italic.
	italic := func(s string) bool {
		return strings.Contains(s, "\x1b[3;") || strings.Contains(s, "\x1b[3m") || strings.Contains(s, ";3;") || strings.Contains(s, ";3m")
	}
	for name, st := range map[string]lipgloss.Style{"desc": sDesc, "selected desc": sDescSel} {
		if got := st.Render("x"); !italic(got) {
			t.Errorf("%s = %q, want italics", name, got)
		}
	}
	for name, st := range map[string]lipgloss.Style{"dim": sDim, "header": sHeader, "accent": sKey, "title": sTitle} {
		if got := st.Render("x"); italic(got) {
			t.Errorf("%s = %q, want no italics", name, got)
		}
	}
	// Normal text keeps the terminal's default foreground.
	if got := sDescSel.Render("x"); got != "\x1b[3mx\x1b[0m" {
		t.Errorf("selected desc = %q, want no foreground color", got)
	}
}
