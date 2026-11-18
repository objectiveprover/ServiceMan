package tui

import (
	"testing"
	"time"
)

func TestHumanDur(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{-time.Second, "0s"},
		{45 * time.Second, "45s"},
		{time.Minute, "1m"},
		{12*time.Minute + 30*time.Second, "12m 30s"},
		{2 * time.Hour, "2h"},
		{5*time.Hour + 7*time.Minute + 59*time.Second, "5h 7m"},
		{24 * time.Hour, "1d"},
		{3*24*time.Hour + 4*time.Hour + 30*time.Minute, "3d 4h"},
		{123*24*time.Hour + 23*time.Hour, "123d 23h"},
	} {
		if got := humanDur(c.d); got != c.want {
			t.Errorf("humanDur(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
