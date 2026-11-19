package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The conventions for the services ServiceMan manages: their own config in
// ~/.config/<binary>/ and their secrets in ~/.secrets/<binary>.env.

// SecretsDir holds services' env files (secrets), outside ~/.config so
// dotfile repos, sync and backups can leave it out. It should be 0700.
func SecretsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".secrets")
}

// DefaultEnvFile is the conventional env file of a service: ~/.secrets/<binary>.env.
func DefaultEnvFile(binary string) string { return filepath.Join(SecretsDir(), binary+".env") }

// EnvFileProblem describes why an env file isn't safe or usable ("" if fine):
// missing, or readable by other users (secrets should be 0600).
func EnvFileProblem(path string) string {
	fi, err := os.Stat(strings.TrimPrefix(path, "-"))
	switch {
	case err != nil && os.IsNotExist(err):
		if strings.HasPrefix(path, "-") {
			return "" // "-" tells systemd a missing file is fine
		}
		return "missing"
	case err != nil:
		return "" // e.g. a root-only directory: can't tell, don't guess
	case fi.Mode().Perm()&0o077 != 0:
		return fmt.Sprintf("readable by others (%04o) — chmod 600", fi.Mode().Perm())
	}
	return ""
}

// ExpandEnvFile expands "~" in an EnvironmentFile= value, keeping systemd's
// "-" (ignore if missing) prefix; systemd itself doesn't expand "~".
func ExpandEnvFile(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "-") {
		return "-" + ExpandTilde(v[1:])
	}
	return ExpandTilde(v)
}

// TildeEnvFile abbreviates an EnvironmentFile= value with "~", keeping the
// "-" prefix (the inverse of ExpandEnvFile).
func TildeEnvFile(v string) string {
	if rest, ok := strings.CutPrefix(v, "-"); ok {
		return "-" + Tilde(rest)
	}
	return Tilde(v)
}

// AppDir is where a service's own config lives: ~/.config/<binary>.
func AppDir(binary string) string { return filepath.Join(ConfigHome(), binary) }

// BinaryName is the file name of a binary's path ("" when there is none, e.g.
// for an empty path or "/").
func BinaryName(path string) string {
	if b := filepath.Base(path); b != "." && b != "/" {
		return b
	}
	return ""
}

// DefaultAppConfig finds a service's config file by the convention
// ~/.config/<binary>/config.<ext> ("" when there is none). Backups and
// examples next to it are ignored.
func DefaultAppConfig(binary string) string {
	if binary == "" || BinaryName(binary) != binary { // a bare name only
		return ""
	}
	matches, _ := filepath.Glob(filepath.Join(AppDir(binary), "config.*"))
	for _, p := range matches {
		base := filepath.Base(p)
		if strings.HasSuffix(base, ".bak") || strings.HasSuffix(base, ".tmp") ||
			strings.Contains(base, ".example") || strings.Contains(base, ".template") {
			continue
		}
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}
