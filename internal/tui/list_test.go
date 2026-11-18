package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/systemd"
)

func TestStateCellQuickExit(t *testing.T) {
	st := systemd.Status{Type: "simple", ActiveState: "inactive", SubState: "dead", ExitCode: 1}
	st.RunTime, st.SelfExit = 200_000_000, true // 0.2 s
	if got := ansi.Strip(stateCell(st, true)); got != "○ exited in 0.2s" {
		t.Errorf("clean quick exit: %q", got)
	}
	if !strings.Contains(ansi.Strip(quickExitNote(st)), "Probably not a service") {
		t.Errorf("note: %q", ansi.Strip(quickExitNote(st)))
	}
	st.ExitStatus = 1
	if got := ansi.Strip(stateCell(st, true)); got != "✖ crashed in 0.2s" {
		t.Errorf("crash: %q", got)
	}
	if note := ansi.Strip(quickExitNote(st)); !strings.Contains(note, "exited with status 1 0.2s after starting") {
		t.Errorf("crash note: %q", note)
	}
}
