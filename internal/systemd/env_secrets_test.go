package systemd

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const secret = "hunter2-s3cret"

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// A new env file made by "Edit env file" is private from the start: the
// directory 0700 and the file 0600, whatever the umask, even when the editor
// (e.g. an Emacs server) doesn't run with our umask.
func TestEditEnvFileCmdIsPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := syscall.Umask(0o002) // a permissive umask, as on many desktops
	defer syscall.Umask(old)

	// An "editor" that ignores the umask, like a server started elsewhere.
	ed := filepath.Join(t.TempDir(), "ed")
	os.WriteFile(ed, []byte("#!/bin/sh\n(umask 000; echo TOKEN="+secret+" >> \"$1\")\n"), 0o755)

	path := filepath.Join(home, ".secrets", "app.env")
	if out, err := EditEnvFileCmd(ed, path).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if m := mode(t, filepath.Dir(path)); m != 0o700 {
		t.Errorf("~/.secrets is %04o, want 0700", m)
	}
	if m := mode(t, path); m != 0o600 {
		t.Errorf("env file is %04o, want 0600", m)
	}

	// Quitting the editor without saving leaves no empty env file behind.
	other := filepath.Join(home, ".secrets", "other.env")
	if out, err := EditEnvFileCmd("true", other).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(other); !os.IsNotExist(err) {
		t.Errorf("unsaved new env file was left behind (err=%v)", err)
	}

	// An existing file keeps its content and mode (the details box warns
	// about loose modes; ServiceMan never changes your files on its own).
	os.Chmod(path, 0o640)
	if out, err := EditEnvFileCmd("true", path).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), secret) {
		t.Errorf("existing env file changed: %q", b)
	}
	if m := mode(t, path); m != 0o640 {
		t.Errorf("existing env file mode changed to %04o", m)
	}
}

// The reasons the Edit Service form gives for opening Emacs instead quote
// unit lines; lines that hold values meant to stay private show only their key.
func TestParseUnitRedactsSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "app.service")
	os.WriteFile(p, []byte("[Service]\nExecStart=/bin/app\n"+
		"SetCredential=db:"+secret+"\n"+
		"SetCredentialEncrypted=tok: "+secret+"\n"+
		"Environment=A=1 \\\n  TOKEN="+secret+"\n"+
		"Type=forking\n"), 0o600)
	_, unsupported, err := ParseUnit(p)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(unsupported, "\n")
	if strings.Contains(all, secret) {
		t.Errorf("unsupported lines show a secret:\n%s", all)
	}
	for _, want := range []string{"SetCredential=…", "SetCredentialEncrypted=…", "Environment=…", "Type=forking"} {
		if !strings.Contains(all, want) {
			t.Errorf("unsupported lacks %q:\n%s", want, all)
		}
	}
}

// ServiceMan reads which env files a unit has, never the variables:
// systemctl show would print Environment= values.
func TestShowNeverFetchesEnvironment(t *testing.T) {
	for _, p := range strings.Split(showProps, ",") {
		if strings.HasPrefix(p, "Environment") && p != "EnvironmentFiles" || p == "SetCredential" || p == "SetCredentialEncrypted" {
			t.Errorf("showProps fetches %s", p)
		}
	}
}
