// Package serviceinfo reads what a server says about itself with its
// `serviceman-info` subcommand: the JSON its New Service form is filled from
// (description, arguments, port and health check, config and env file).
// CaddyMan asks the same question as `caddyman-info`; a server answers both
// with the same JSON, which carries both tools' version keys.
package serviceinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Command is the subcommand a server describes itself with.
const Command = "serviceman-info"

// Info is the output of `<binary> serviceman-info`.
type Info struct {
	// Version is the format version ("serviceman_info"); 1 or more.
	Version     int      `json:"serviceman_info"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Args        []string `json:"args"`
	// Port is nil until the server's first start assigns one.
	Port       *int    `json:"port"`
	HealthPath string  `json:"health_path"`
	ConfigPath string  `json:"config_path"`
	EnvFile    *string `json:"env_file"`
	Proxy      Proxy   `json:"proxy"`
}

// Proxy holds suggested values for a CaddyMan site.
type Proxy struct {
	HTTPS        bool   `json:"https"`
	TrustedOnly  bool   `json:"trusted_only"`
	RedirectRoot string `json:"redirect_root"`
}

// HealthURL is the health check on this machine ("" before a port is assigned).
func (i *Info) HealthURL() string {
	if i.Port == nil || *i.Port <= 0 {
		return ""
	}
	path := i.HealthPath
	if path == "" {
		path = "/"
	} else if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("http://localhost:%d%s", *i.Port, path)
}

// ExecStart is the binary followed by the server's arguments, quoted for
// systemd where needed.
func (i *Info) ExecStart(bin string) string {
	parts := []string{quote(bin)}
	for _, a := range i.Args {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Timeout bounds how long a binary may take to answer.
const Timeout = 3 * time.Second

// marker is what a binary that has the subcommand contains: the command's
// name. A generic name won't do: every Bun or Node binary carries the MIME
// type "…-service-information", for instance.
var marker = []byte(Command)

// Supports tells whether bin looks like it has a `serviceman-info`
// subcommand, by looking for its name in the file. Binaries without it are
// never run, so picking an unrelated program can't start it with an unknown
// argument.
func Supports(bin string) bool {
	f, err := os.Open(bin)
	if err != nil {
		return false
	}
	defer f.Close()
	const chunk = 1 << 20
	keep := len(marker) - 1
	buf := make([]byte, keep+chunk)
	carry := 0
	for {
		n, err := io.ReadFull(f, buf[carry:])
		data := buf[:carry+n]
		if bytes.Contains(data, marker) {
			return true
		}
		if err != nil { // EOF or a short last read
			return false
		}
		carry = copy(buf, data[max(0, len(data)-keep):])
	}
}

// ErrUnsupported means the binary doesn't have a `serviceman-info` subcommand.
var ErrUnsupported = errors.New("no " + Command + " command")

// Query runs `bin serviceman-info` and parses its answer. It returns
// ErrUnsupported for binaries without the subcommand.
func Query(bin string) (*Info, error) {
	if !Supports(bin) {
		return nil, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, Command)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s %s: no answer within %s", bin, Command, Timeout)
	}
	if err != nil {
		return nil, ErrUnsupported // e.g. "unknown command": the marker was a coincidence
	}
	return Parse(out)
}

// Parse reads `serviceman-info` output.
func Parse(out []byte) (*Info, error) {
	var i Info
	if err := json.Unmarshal(out, &i); err != nil || i.Version < 1 {
		return nil, ErrUnsupported
	}
	return &i, nil
}

// Cache remembers binaries' answers, so lists that are drawn again and again
// (the New Service and Track Services pickers) run each binary once. An
// answer holds while the binary is unchanged (size and modification time)
// and, for a server, while its config file is: the port a server picks on
// its first start is written there. A binary that didn't answer in time is
// asked again. The zero value is ready to use; a nil Cache doesn't cache.
type Cache struct {
	mu sync.Mutex
	m  map[string]cached // by the binary's path, symlinks resolved
}

type cached struct {
	size, mod int64 // the binary's size and mtime (ns)
	cfgMod    int64 // the server's config file's mtime (ns; 0 if missing)
	info      *Info
	err       error
}

// Query is the package's Query with the answer remembered.
func (c *Cache) Query(bin string) (*Info, error) {
	if c == nil {
		return Query(bin)
	}
	path, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return nil, ErrUnsupported
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, ErrUnsupported
	}
	if e, ok := c.lookup(path, fi); ok {
		return e.info, e.err
	}
	info, err := Query(bin)
	if err != nil && !errors.Is(err, ErrUnsupported) {
		return nil, err // no answer in time: ask again next time
	}
	e := cached{size: fi.Size(), mod: fi.ModTime().UnixNano(), info: info, err: err}
	if info != nil {
		e.cfgMod = configMod(info)
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]cached{}
	}
	c.m[path] = e
	c.mu.Unlock()
	return info, err
}

// Peek is what the Cache already knows about bin, without running it
// (ok false when it would have to).
func (c *Cache) Peek(bin string) (info *Info, ok bool) {
	if c == nil {
		return nil, false
	}
	path, err := filepath.EvalSymlinks(bin)
	if err != nil {
		return nil, false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	e, ok := c.lookup(path, fi)
	return e.info, ok
}

// lookup is the remembered answer for path, if it still holds.
func (c *Cache) lookup(path string, fi os.FileInfo) (cached, bool) {
	c.mu.Lock()
	e, ok := c.m[path]
	c.mu.Unlock()
	if !ok || e.size != fi.Size() || e.mod != fi.ModTime().UnixNano() ||
		(e.info != nil && e.cfgMod != configMod(e.info)) {
		return cached{}, false
	}
	return e, true
}

// configMod is the modification time of a server's config file (0 when it
// has none or it doesn't exist yet).
func configMod(i *Info) int64 {
	p := i.ConfigPath
	if p == "" {
		return 0
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return 0
		}
		p = filepath.Join(home, p[2:])
	}
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.ModTime().UnixNano()
}
