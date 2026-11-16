package systemd

import "testing"

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
