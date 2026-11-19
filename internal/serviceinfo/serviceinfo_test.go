package serviceinfo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

const answer = `{"service_info": 1, "name": "app", "description": "An app", "args": ["serve", "--dir", "/my files"],
"port": 20003, "health_path": "/api/health", "config_path": "~/.config/app/config.json", "env_file": null,
"proxy": {"https": true, "trusted_only": false, "redirect_root": "/web/"}}`

func TestQuery(t *testing.T) {
	bin := script(t, `[ "$1" = service-info ] && cat <<'J'
`+answer+`
J
`)
	i, err := Query(bin)
	if err != nil {
		t.Fatal(err)
	}
	if i.Description != "An app" || i.EnvFile != nil || i.Proxy.RedirectRoot != "/web/" || !i.Proxy.HTTPS {
		t.Errorf("info = %+v", i)
	}
	if u := i.HealthURL(); u != "http://localhost:20003/api/health" {
		t.Errorf("health URL = %q", u)
	}
	if e := i.ExecStart("/home/me/.local/bin/app"); e != `/home/me/.local/bin/app serve --dir "/my files"` {
		t.Errorf("ExecStart = %q", e)
	}
}

func TestNoPortYet(t *testing.T) {
	i, err := Parse([]byte(strings.Replace(answer, "20003", "null", 1)))
	if err != nil || i.Port != nil || i.HealthURL() != "" {
		t.Errorf("info = %+v, %v; want no port and no health URL", i, err)
	}
}

// A program that never mentions service-info is not run at all.
func TestUnrelatedBinaryIsNotRun(t *testing.T) {
	ran := filepath.Join(t.TempDir(), "ran")
	bin := script(t, "touch "+ran+"\n")
	if _, err := Query(bin); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the binary was run")
	}
}

// One that mentions it but fails or prints something else isn't a server
// with the command.
func TestFailingOrBadAnswer(t *testing.T) {
	for _, body := range []string{"# service-info\nexit 2\n", "# service-info\necho hello\n", "# service_info\necho '{}'\n"} {
		if _, err := Query(script(t, body)); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%q: err = %v, want ErrUnsupported", body, err)
		}
	}
}

// The marker is found across the boundary between two read chunks.
func TestSupportsAcrossChunks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big")
	data := make([]byte, 1<<20+20)
	copy(data[1<<20-5:], "service_info")
	os.WriteFile(p, data, 0o755)
	if !Supports(p) {
		t.Error("marker split across chunks not found")
	}
	os.WriteFile(p, make([]byte, 3<<20), 0o755)
	if Supports(p) || Supports(filepath.Join(t.TempDir(), "missing")) {
		t.Error("found a marker that isn't there")
	}
}
