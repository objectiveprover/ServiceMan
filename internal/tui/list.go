package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"serviceman/internal/systemd"
)

func (m *Model) listKey(msg tea.KeyMsg) tea.Cmd {
	k := msg.String()
	n := len(m.rows())
	page := max(1, m.h-8)
	switch k {
	case "ctrl+n", "n", "down":
		m.cursor = min(m.cursor+1, n-1)
	case "ctrl+p", "p", "up":
		m.cursor = max(m.cursor-1, 0)
	case "ctrl+v", "pgdown":
		m.cursor = min(m.cursor+page, n-1)
	case "alt+v", "pgup":
		m.cursor = max(m.cursor-page, 0)
	case "alt+up", "alt+<", "home":
		m.cursor = 0
	case "alt+down", "alt+>", "end":
		m.cursor = n - 1
	case "ctrl+g", "esc":
		if m.filter != "" {
			m.filter = ""
			m.setMsg("Filter cleared")
		} else {
			m.setMsg("Quit")
		}
	default:
		for _, c := range commands {
			for _, ck := range strings.Fields(c.keys) {
				if ck == k {
					return runCommand(m, c)
				}
			}
		}
		m.setErr("%s is undefined (? for help, M-x for commands)", emacsKey(k))
	}
	m.clampCursor()
	return nil
}

// ------------------------------------------------------------------ view

type column struct {
	title string
	width int
	prio  int // lower = dropped first when narrow
}

func (m *Model) listView(w, h int) string {
	rows := m.rows()
	var b strings.Builder

	keyed := func(name, key string) string { return sDim.Render(name+" (") + sKey.Render(key) + sDim.Render(")") }
	left := []string{keyed("Create service", "c"), keyed("Track services", "t")}
	if m.filter != "" {
		left = append(left, sDim.Render("Filter: ")+sKey.Render(m.filter))
	}
	if m.statusErr != "" {
		left = append(left, sRed.Render(trunc(m.statusErr, w/3)))
	}
	// The Daemon indicator stays on the right; the left side gives way.
	b.WriteString(topBar(w, "", left, m.daemonCell()) + "\n")

	cols := []column{
		{"SCOPE", 5, 3}, {"STATE", 20, 9}, {"BOOT", 9, 5}, {"PID", 7, 1},
		{"RAM", 7, 2}, {"UPTIME", 8, 4}, {"HEALTH", 12, 6},
	}
	unitW := func() int {
		t := 3 // marker (2) + spare (1)
		for _, c := range cols {
			t += len(colGap) + c.width
		}
		return w - t
	}
	count := fmt.Sprintf("UNIT [%d]", len(m.cfg.Services))
	if m.filter != "" {
		count = fmt.Sprintf("UNIT [%d/%d]", len(rows), len(m.cfg.Services))
	}
	// The UNIT column is as wide as the longest name (all services, so it
	// doesn't jump while filtering), capped at maxUnitW; longer names are
	// truncated here and shown in full in the details box.
	const maxUnitW = 32
	need := ansi.StringWidth(count)
	for _, s := range m.cfg.Services {
		n := ansi.StringWidth(s.Unit)
		if m.status[s.Key()].NeedReload {
			n += 2 // " ⟳"
		}
		need = max(need, n)
	}
	need = min(need, maxUnitW)
	for unitW() < min(need, 18) && len(cols) > 1 {
		lo := 0
		for i, c := range cols {
			if c.prio < cols[lo].prio {
				lo = i
			}
		}
		cols = append(cols[:lo], cols[lo+1:]...)
	}
	uw := max(1, min(need, unitW()))
	hdr := sHeader.Render(gutter + pad(count, uw))
	for _, c := range cols {
		hdr += sHeader.Render(colGap + pad(c.title, c.width))
	}
	b.WriteString(hdr + "\n")

	detailH := 10
	listH := h - 3 - detailH // title, separator, column header
	if listH < 1 {
		listH, detailH = h-3, 0
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+listH {
		m.offset = m.cursor - listH + 1
	}
	if len(rows) == 0 {
		msg := "  No tracked services. Press a to add existing units, c to create a new one."
		if m.filter != "" {
			msg = "  No services match the filter (C-g clears it)."
		}
		b.WriteString(sDim.Render(msg) + "\n")
	}
	for i := m.offset; i < len(rows) && i < m.offset+listH; i++ {
		s := rows[i]
		st, known := m.status[s.Key()]
		cells := map[string]string{}
		cells["SCOPE"] = scopeLabel(s.Scope)
		cells["STATE"] = stateCell(st, known)
		cells["BOOT"] = bootCell(st.UnitFileState)
		cells["PID"], cells["RAM"], cells["UPTIME"] = "-", "-", "-"
		if st.MainPID > 0 {
			cells["PID"] = fmt.Sprint(st.MainPID)
		}
		if st.Memory > 0 {
			cells["RAM"] = humanBytes(st.Memory)
		}
		if st.ActiveState == "active" && !st.Since.IsZero() {
			cells["UPTIME"] = humanDur(time.Since(st.Since))
		}
		cells["HEALTH"] = m.healthCell(s)

		unit := s.Unit
		if st.NeedReload {
			unit += " ⟳"
		}
		line := pad(unit, uw)
		if i == m.cursor {
			line = sHighlight.Render(line)
		}
		for _, c := range cols {
			line += colGap + pad(cells[c.title], c.width)
		}
		b.WriteString(cursorMark(i == m.cursor) + line + "\n")
	}
	used := min(len(rows)-m.offset, listH)
	if len(rows) == 0 {
		used = 1
	}
	for i := used; i < listH; i++ {
		b.WriteString("\n")
	}
	if detailH > 0 {
		b.WriteString(m.detailView(w))
	}
	return strings.TrimRight(b.String(), "\n")
}

func stateCell(st systemd.Status, known bool) string {
	if !known {
		return sDim.Render("…")
	}
	if st.LoadState == "not-found" {
		return sRed.Render("✖ not-found")
	}
	switch st.QuickExit() {
	case systemd.CleanQuickExit:
		return sYellow.Render("○ exited in " + runDur(st.RunTime))
	case systemd.CrashQuickExit:
		return sRed.Render("✖ crashed in " + runDur(st.RunTime))
	}
	txt := st.ActiveState + "/" + st.SubState
	switch st.ActiveState {
	case "active":
		return sGreen.Render("● " + txt)
	case "failed":
		return sRed.Render("✖ " + txt)
	case "activating", "deactivating", "reloading":
		return sYellow.Render("◐ " + txt)
	default:
		return sDim.Render("○ " + txt)
	}
}

func bootCell(s string) string {
	switch s {
	case "enabled", "enabled-runtime":
		return sGreen.Render(s)
	case "":
		return sDim.Render("-")
	case "masked":
		return sRed.Render(s)
	default:
		return sDim.Render(s)
	}
}
