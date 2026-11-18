package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTest(t *testing.T) (*Watcher, string, chan Target) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "app")
	if err := os.WriteFile(bin, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := make(chan Target, 10)
	w, err := New(filepath.Join(dir, "state.json"), func(tg Target, _ time.Time) (string, string) {
		calls <- tg
		return "ok", ""
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	w.Debounce = 100 * time.Millisecond
	w.MinInterval = 0
	w.Sync([]Target{{Key: "user/app.service", Unit: "app.service", Path: bin}})
	return w, bin, calls
}

func expect(t *testing.T, calls chan Target, n int) {
	t.Helper()
	deadline := time.After(1500 * time.Millisecond)
	got := 0
	for {
		select {
		case <-calls:
			got++
		case <-deadline:
			if got != n {
				t.Fatalf("got %d restarts, want %d", got, n)
			}
			return
		}
	}
}

func TestTouchAndChmodDoNotRestart(t *testing.T) {
	_, bin, calls := newTest(t)
	now := time.Now().Add(time.Minute)
	os.Chtimes(bin, now, now)
	os.Chmod(bin, 0o700)
	expect(t, calls, 0)
}

func TestUnlinkAndRecreateRestartsOnce(t *testing.T) {
	w, bin, calls := newTest(t)
	os.Remove(bin)
	f, _ := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY, 0o644)
	for i := 0; i < 50; i++ {
		f.Write([]byte("chunk of version two "))
	}
	f.Close()
	os.Chmod(bin, 0o755) // like install(1): chmod after writing
	expect(t, calls, 1)
	st := w.States()["user/app.service"]
	if st.Result != "ok" || st.RestartedAt.IsZero() {
		t.Fatalf("state not recorded: %+v", st)
	}
}

func TestRenameIntoPlace(t *testing.T) {
	_, bin, calls := newTest(t)
	tmp := bin + ".tmp"
	os.WriteFile(tmp, []byte("v2"), 0o755)
	os.Rename(tmp, bin)
	expect(t, calls, 1)
	// Same content again: no restart.
	os.WriteFile(tmp, []byte("v2"), 0o755)
	os.Rename(tmp, bin)
	expect(t, calls, 0)
}

func TestNotExecutableWaits(t *testing.T) {
	_, bin, calls := newTest(t)
	os.Remove(bin)
	os.WriteFile(bin, []byte("v2"), 0o644)
	expect(t, calls, 0)
	os.Chmod(bin, 0o755)
	expect(t, calls, 1)
}

func TestChangeWhileNotWatched(t *testing.T) {
	w, bin, _ := newTest(t)
	state := w.StatePath
	w.Close()
	os.WriteFile(bin+".tmp", []byte("v2"), 0o755)
	os.Rename(bin+".tmp", bin)

	calls := make(chan Target, 10)
	w2, err := New(state, func(tg Target, _ time.Time) (string, string) { calls <- tg; return "ok", "" })
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	w2.Debounce = 100 * time.Millisecond
	w2.Sync([]Target{{Key: "user/app.service", Unit: "app.service", Path: bin}})
	expect(t, calls, 1)
}

// The daemon calls Sync every few seconds: a Sync that changes nothing
// leaves the state file alone, and one that drops a target rewrites it.
func TestSyncSavesOnlyOnChange(t *testing.T) {
	w, bin, _ := newTest(t)
	fi, err := os.Stat(w.StatePath)
	if err != nil {
		t.Fatalf("first Sync didn't write the state file: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(w.StatePath, old, old)

	w.Sync([]Target{{Key: "user/app.service", Unit: "app.service", Path: bin}})
	if fi, _ = os.Stat(w.StatePath); !fi.ModTime().Equal(old) {
		t.Error("an unchanged Sync rewrote the state file")
	}

	w.Sync(nil)
	if fi, _ = os.Stat(w.StatePath); fi.ModTime().Equal(old) {
		t.Error("dropping a target didn't rewrite the state file")
	}
	if st, _ := LoadStates(w.StatePath); len(st) != 0 {
		t.Errorf("state file still lists %v", st)
	}
}
