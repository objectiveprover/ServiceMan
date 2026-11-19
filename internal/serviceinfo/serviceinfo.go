// Package serviceinfo reads what a server says about itself with its
// `service-info` subcommand: the JSON its New Service form is filled from
// (description, arguments, port and health check, config and env file).
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
	"strings"
	"time"
)

// Info is the output of `<binary> service-info`.
type Info struct {
	// Version is the format version ("service_info"); 1 or more.
	Version     int      `json:"service_info"`
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

// markers are what a binary that has the subcommand contains: the JSON key
// or the command's name.
var markers = [][]byte{[]byte("service_info"), []byte("service-info")}

// Supports tells whether bin looks like it has a `service-info` subcommand,
// by looking for its name in the file. Binaries without it are never run, so
// picking an unrelated program can't start it with an unknown argument.
func Supports(bin string) bool {
	f, err := os.Open(bin)
	if err != nil {
		return false
	}
	defer f.Close()
	const chunk = 1 << 20
	keep := len(markers[0]) - 1
	buf := make([]byte, keep+chunk)
	carry := 0
	for {
		n, err := io.ReadFull(f, buf[carry:])
		data := buf[:carry+n]
		for _, m := range markers {
			if bytes.Contains(data, m) {
				return true
			}
		}
		if err != nil { // EOF or a short last read
			return false
		}
		carry = copy(buf, data[len(data)-keep:])
	}
}

// ErrUnsupported means the binary doesn't have a `service-info` subcommand.
var ErrUnsupported = errors.New("no service-info command")

// Query runs `bin service-info` and parses its answer. It returns
// ErrUnsupported for binaries without the subcommand.
func Query(bin string) (*Info, error) {
	if !Supports(bin) {
		return nil, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "service-info")
	cmd.Stdin = nil
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%s service-info: no answer within %s", bin, Timeout)
	}
	if err != nil {
		return nil, ErrUnsupported // e.g. "unknown command": the marker was a coincidence
	}
	return Parse(out)
}

// Parse reads `service-info` output.
func Parse(out []byte) (*Info, error) {
	var i Info
	if err := json.Unmarshal(out, &i); err != nil || i.Version < 1 {
		return nil, ErrUnsupported
	}
	return &i, nil
}
