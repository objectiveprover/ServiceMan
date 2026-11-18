package systemd

import (
	"testing"
	"time"
)

func TestQuickExit(t *testing.T) {
	const s = int64(time.Second / time.Microsecond)
	// last builds a status whose main process started at 100 s and exited
	// after run; the unit left the active state at activeExit.
	last := func(run, activeExit int64) Status {
		st := Status{Type: "simple", ActiveState: "inactive", ExitCode: 1}
		st.setLastRun(100*s, 100*s+run, activeExit)
		return st
	}
	clean := last(s/5, 100*s+s/5+1) // exited by itself 0.2 s in
	crash := clean
	crash.ExitStatus = 1
	killed := clean
	killed.ExitCode, killed.ExitStatus = 2, 11
	stopped := last(s, 100*s+s/2) // stopped: left active before the exit
	slow := last(10*s, 110*s+1)   // ran 10 s
	oneshot := clean
	oneshot.Type = "oneshot"
	running := clean
	running.ActiveState, running.MainPID = "active", 42
	restarting := crash
	restarting.ActiveState = "activating"
	never := Status{Type: "simple", ActiveState: "inactive"}

	for name, c := range map[string]struct {
		st   Status
		want QuickExitKind
	}{
		"clean exit":            {clean, CleanQuickExit},
		"non-zero status":       {crash, CrashQuickExit},
		"killed by a signal":    {killed, CrashQuickExit},
		"auto-restarting crash": {restarting, CrashQuickExit},
		"stopped by the user":   {stopped, NoQuickExit},
		"ran a while":           {slow, NoQuickExit},
		"oneshot":               {oneshot, NoQuickExit},
		"running again":         {running, NoQuickExit},
		"never ran":             {never, NoQuickExit},
	} {
		if got := c.st.QuickExit(); got != c.want {
			t.Errorf("%s: QuickExit() = %v, want %v (status %+v)", name, got, c.want, c.st)
		}
	}
	if clean.RunTime != 200*time.Millisecond {
		t.Errorf("RunTime = %v, want 200ms", clean.RunTime)
	}
}

func TestParseExecPath(t *testing.T) {
	cases := map[string]string{
		"{ path=/usr/sbin/sshd ; argv[]=/usr/sbin/sshd -D $SSHD_OPTS ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }": "/usr/sbin/sshd",
		"{ path=/home/u/.local/bin/hello ; argv[]=/home/u/.local/bin/hello ; ignore_errors=no }":                                                                   "/home/u/.local/bin/hello",
		"": "",
	}
	for in, want := range cases {
		if got := parseExecPath(in); got != want {
			t.Errorf("parseExecPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEnvFile(t *testing.T) {
	cases := map[string]string{
		"/etc/default/ssh (ignore_errors=yes)": "/etc/default/ssh",
		"/srv/app/.env (ignore_errors=no)":     "/srv/app/.env",
		"/plain":                               "/plain",
	}
	for in, want := range cases {
		if got := parseEnvFile(in); got != want {
			t.Errorf("parseEnvFile(%q) = %q, want %q", in, got, want)
		}
	}
}
