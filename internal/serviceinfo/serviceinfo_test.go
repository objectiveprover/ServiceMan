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

const answer = `{"serviceman_info": 1, "caddyman_info": 1, "name": "app", "description": "An app", "args": ["serve", "--dir", "/my files"],
"port": 20003, "health_path": "/api/health", "config_path": "~/.config/app/config.json", "env_file": null,
"proxy": {"https": true, "trusted_only": false, "redirect_root": "/web/"}}`

func TestQuery(t *testing.T) {
	bin := script(t, `[ "$1" = serviceman-info ] && cat <<'J'
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

// A program that never mentions serviceman-info is not run at all, nor is
// one that only contains look-alikes: the old command name, CaddyMan's, the
// JSON keys, or the MIME type every Bun or Node binary carries.
func TestUnrelatedBinaryIsNotRun(t *testing.T) {
	for _, body := range []string{
		"",
		"# service-info service_info\n",
		"# caddyman-info caddyman_info serviceman_info\n",
		"# application/vnd.3gpp-v2x-local-service-information\n",
	} {
		ran := filepath.Join(t.TempDir(), "ran")
		bin := script(t, body+"touch "+ran+"\n")
		if _, err := Query(bin); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%q: err = %v, want ErrUnsupported", body, err)
		}
		if _, err := os.Stat(ran); err == nil {
			t.Errorf("%q: the binary was run", body)
		}
	}
}

// One that mentions it but fails or prints something else isn't a server
// with the command.
func TestFailingOrBadAnswer(t *testing.T) {
	for _, body := range []string{"# serviceman-info\nexit 2\n", "# serviceman-info\necho hello\n", "# serviceman-info\necho '{\"service_info\": 1}'\n"} {
		if _, err := Query(script(t, body)); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%q: err = %v, want ErrUnsupported", body, err)
		}
	}
}

// The marker is found across the boundary between two read chunks.
func TestSupportsAcrossChunks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big")
	data := make([]byte, 1<<20+20)
	copy(data[1<<20-5:], "serviceman-info")
	os.WriteFile(p, data, 0o755)
	if !Supports(p) {
		t.Error("marker split across chunks not found")
	}
	os.WriteFile(p, make([]byte, 3<<20), 0o755)
	if Supports(p) || Supports(filepath.Join(t.TempDir(), "missing")) {
		t.Error("found a marker that isn't there")
	}
}

// The cache runs a binary once, and again when the binary or its config
// file changes.
func TestCache(t *testing.T) {
	dir := t.TempDir()
	cfg, count := filepath.Join(dir, "config.json"), filepath.Join(dir, "count")
	bin := script(t, `echo x >> `+count+`
[ "$1" = serviceman-info ] && echo '{"serviceman_info": 1, "config_path": "`+cfg+`"}'
`)
	runs := func() int { b, _ := os.ReadFile(count); return strings.Count(string(b), "x") }
	var c Cache
	if _, ok := c.Peek(bin); ok {
		t.Error("Peek knows a binary never asked")
	}
	for range 3 {
		if i, err := c.Query(bin); err != nil || i.ConfigPath != cfg {
			t.Fatalf("Query = %+v, %v", i, err)
		}
	}
	if i, ok := c.Peek(bin); !ok || i == nil || runs() != 1 {
		t.Errorf("Peek = %v, %v after %d runs, want the answer after 1", i, ok, runs())
	}
	// The server writes its port into its config: ask again.
	os.WriteFile(cfg, []byte("{}"), 0o644)
	if _, ok := c.Peek(bin); ok {
		t.Error("Peek still holds after the config changed")
	}
	c.Query(bin)
	c.Query(bin)
	if runs() != 2 {
		t.Errorf("%d runs, want 2", runs())
	}
	// Redeployed.
	os.WriteFile(bin, append(must(os.ReadFile(bin)), '\n'), 0o755)
	c.Query(bin)
	if runs() != 3 {
		t.Errorf("%d runs, want 3", runs())
	}
	// An unrelated binary isn't run, and that is remembered too.
	other := script(t, "exit 0\n")
	if _, err := c.Query(other); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v", err)
	}
	if i, ok := c.Peek(other); !ok || i != nil {
		t.Errorf("Peek(other) = %v, %v; want known unsupported", i, ok)
	}
	var nilCache *Cache
	if _, err := nilCache.Query(bin); err != nil {
		t.Errorf("nil cache: %v", err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
