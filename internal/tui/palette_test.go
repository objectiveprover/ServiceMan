package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/config"
	"serviceman/internal/systemd"
)

// M-x is a full-height screen with the standard top bar; the footer keeps
// the M-x input.
func TestPaletteFullScreen(t *testing.T) {
	m := &Model{cfg: &config.Config{}, status: map[string]systemd.Status{}, w: 100, h: 30}
	m.openPalette()
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if len(lines) != m.h {
		t.Fatalf("view has %d lines, want %d", len(lines), m.h)
	}
	for _, want := range []string{"SERVICEMAN", "Commands", "commands", "Cancel (C-g/ESC ESC)"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("top bar %q lacks %q", lines[0], want)
		}
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, gutter+"M-x ") {
		t.Errorf("last line = %q, want the M-x input", last)
	}
	rows := 0
	for _, l := range lines[2 : len(lines)-2] {
		if strings.TrimSpace(l) != "" {
			rows++
		}
	}
	if want := min(len(m.prompt.candidates), m.h-4); rows != want {
		t.Errorf("%d command rows, want %d", rows, want)
	}
}
