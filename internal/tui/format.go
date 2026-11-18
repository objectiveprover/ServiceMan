package tui

import (
	"fmt"
	"time"

	"serviceman/internal/config"
)

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func humanBytes(n uint64) string {
	f := float64(n)
	for _, u := range []string{"B", "K", "M", "G"} {
		if f < 1024 {
			if u == "B" {
				return fmt.Sprintf("%.0f%s", f, u)
			}
			return fmt.Sprintf("%.1f%s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1fT", f)
}

// runDur is how long a quick-exiting program ran: tenths of a second under
// 10 s ("0.2s"), humanDur above.
func runDur(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return humanDur(d)
}

// humanDur is a duration as its two largest units, spaced and without
// leading zeros: "45s", "12m 30s", "5h 7m", "3d 4h". A zero second unit is
// left out ("2h", "3d").
func humanDur(d time.Duration) string {
	s := int(max(d, 0).Seconds())
	units := []struct {
		n    int
		name string
	}{{s / 86400, "d"}, {s / 3600 % 24, "h"}, {s / 60 % 60, "m"}, {s % 60, "s"}}
	for i, u := range units {
		if u.n == 0 && i < len(units)-1 {
			continue
		}
		out := fmt.Sprintf("%d%s", u.n, u.name)
		if i+1 < len(units) && units[i+1].n > 0 {
			out += fmt.Sprintf(" %d%s", units[i+1].n, units[i+1].name)
		}
		return out
	}
	return "0s"
}

// scopeLabel is a scope as the lists' SCOPE column shows it.
func scopeLabel(sc config.Scope) string {
	if sc == config.User {
		return "user"
	}
	return "sys"
}
