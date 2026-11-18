package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The log files copy journals that only you (and admins) can read, and
// services print secrets now and then, so the copies are private too: new
// directories 0700, files 0600, older world-readable ones tightened.
func TestLogFilesArePrivate(t *testing.T) {
	old := syscall.Umask(0o002)
	defer syscall.Umask(old)
	dir := filepath.Join(t.TempDir(), "logs")
	path := filepath.Join(dir, "app.service.log")

	w, err := openRotating(path, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("TOKEN=one\n"))
	w.Write([]byte("TOKEN=two\n")) // rotates
	w.Close()
	for _, p := range []string{dir, path, path + ".1"} {
		want := os.FileMode(0o600)
		if p == dir {
			want = 0o700
		}
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v, want %04o", p, fi.Mode().Perm(), want)
		}
	}

	// Files written by an older serviceman (0644) are tightened on open.
	for i, p := range []string{path, path + ".1", path + ".2"} {
		os.WriteFile(p, []byte(fmt.Sprint(i)), 0o644)
		os.Chmod(p, 0o644)
	}
	w, err = openRotating(path, 1<<20, 3)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	for _, p := range []string{path, path + ".1", path + ".2"} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s left at %04o", p, fi.Mode().Perm())
		}
	}
}
