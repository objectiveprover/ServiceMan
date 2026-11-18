package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"serviceman/internal/config"
)

type follower struct {
	svc      config.Service
	path     string
	maxSize  int64
	maxFiles int
	cancel   context.CancelFunc
	done     chan struct{}
}

func (f *follower) stop() {
	f.cancel()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
	}
}

// CursorDir holds each follower's last journal cursor, so a restarted
// daemon picks up where it left off.
func CursorDir() string { return filepath.Join(config.StateDir(), "cursors") }

func (f *follower) cursorPath() string {
	return filepath.Join(CursorDir(), string(f.svc.Scope)+"--"+f.svc.Unit)
}

func (f *follower) run(ctx context.Context) {
	defer close(f.done)
	backoff := time.Second
	for {
		start := time.Now()
		n, err := f.once(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Printf("%s: journalctl exited after %d lines: %v", f.svc.Unit, n, err)
		if n == 0 && time.Since(start) < 3*time.Second {
			// Probably an invalid/vacuumed cursor: start fresh.
			os.Remove(f.cursorPath())
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

type entry struct {
	Cursor   string          `json:"__CURSOR"`
	Realtime string          `json:"__REALTIME_TIMESTAMP"`
	Message  json.RawMessage `json:"MESSAGE"`
	Priority string          `json:"PRIORITY"`
	PID      string          `json:"_PID"`
	Ident    string          `json:"SYSLOG_IDENTIFIER"`
	Comm     string          `json:"_COMM"`
}

func (f *follower) once(ctx context.Context) (int, error) {
	args := []string{"-f", "-o", "json", "--no-pager",
		"--output-fields=MESSAGE,PRIORITY,_PID,SYSLOG_IDENTIFIER,_COMM"}
	if f.svc.Scope == config.User {
		args = append(args, "--user-unit", f.svc.Unit)
	} else {
		args = append(args, "-u", f.svc.Unit)
	}
	if c, err := os.ReadFile(f.cursorPath()); err == nil && len(c) > 0 {
		args = append(args, "--after-cursor", strings.TrimSpace(string(c)))
	} else {
		args = append(args, "-n", "1000") // first time: grab recent history
	}
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	w, err := openRotating(f.path, f.maxSize, f.maxFiles)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return 0, err
	}
	defer w.Close()

	n := 0
	lastCursor, saved := "", ""
	lastSave := time.Now()
	save := func() {
		if lastCursor != "" && lastCursor != saved {
			os.MkdirAll(filepath.Dir(f.cursorPath()), 0o755)
			tmp := f.cursorPath() + ".tmp"
			if os.WriteFile(tmp, []byte(lastCursor), 0o644) == nil {
				os.Rename(tmp, f.cursorPath())
				saved = lastCursor
			}
		}
		lastSave = time.Now()
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		if _, err := w.Write([]byte(format(e))); err != nil {
			log.Printf("%s: write: %v", f.svc.Unit, err)
		}
		lastCursor = e.Cursor
		n++
		if time.Since(lastSave) > 2*time.Second {
			save()
		}
	}
	save()
	err = cmd.Wait()
	if s := strings.TrimSpace(stderr.String()); s != "" {
		err = fmt.Errorf("%v: %s", err, s)
	}
	return n, err
}

var prioTag = map[string]string{"0": "EMERG", "1": "ALERT", "2": "CRIT", "3": "ERR", "4": "WARN"}

func format(e entry) string {
	ts := time.Now()
	if us, err := strconv.ParseInt(e.Realtime, 10, 64); err == nil {
		ts = time.UnixMicro(us)
	}
	ident := e.Ident
	if ident == "" {
		ident = e.Comm
	}
	if ident == "" {
		ident = "-"
	}
	if e.PID != "" {
		ident += "[" + e.PID + "]"
	}
	msg := decodeMessage(e.Message)
	msg = strings.ReplaceAll(strings.TrimRight(msg, "\n"), "\n", "\n    ")
	tag := ""
	if t, ok := prioTag[e.Priority]; ok {
		tag = " <" + t + ">"
	}
	return fmt.Sprintf("%s %s%s: %s\n", ts.Format("2006-01-02T15:04:05.000-0700"), ident, tag, msg)
}

// decodeMessage handles journal MESSAGE fields, which are strings or (for
// non-UTF-8 data) arrays of bytes.
func decodeMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bs []int
	if json.Unmarshal(raw, &bs) == nil {
		b := make([]byte, len(bs))
		for i, v := range bs {
			b[i] = byte(v)
		}
		return strings.ToValidUTF8(string(b), "?")
	}
	return string(raw)
}
