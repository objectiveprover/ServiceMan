package daemon

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type rotating struct {
	path     string
	max      int64
	maxFiles int
	f        *os.File
	size     int64
}

// Log files are private (0600, new directories 0700): they copy journals
// only you and admins can read, and services sometimes print secrets.
func openRotating(path string, max int64, maxFiles int) (*rotating, error) {
	r := &rotating{path: path, max: max, maxFiles: maxFiles}
	for i := 1; i <= maxFiles; i++ {
		makePrivate(fmt.Sprintf("%s.%d", path, i)) // rotated by an older ServiceMan
	}
	return r, r.open()
}

// makePrivate takes group and other access away from a file (log files
// written by an older ServiceMan were 0644).
func makePrivate(p string) {
	if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o077 != 0 {
		os.Chmod(p, fi.Mode().Perm()&0o700)
	}
}

func (r *rotating) open() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	makePrivate(r.path)
	fi, _ := f.Stat()
	r.f, r.size = f, fi.Size()
	return nil
}

func (r *rotating) Write(p []byte) (int, error) {
	if r.size+int64(len(p)) > r.max && r.size > 0 {
		r.rotate()
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotating) rotate() {
	r.f.Close()
	os.Remove(fmt.Sprintf("%s.%d", r.path, r.maxFiles))
	for i := r.maxFiles - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	os.Rename(r.path, r.path+".1")
	if err := r.open(); err != nil {
		log.Printf("rotate %s: %v", r.path, err)
	}
}

func (r *rotating) Close() error { return r.f.Close() }
