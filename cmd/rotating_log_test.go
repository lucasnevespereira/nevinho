package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingLogBoundsSizeAndKeepsRecentBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.log")
	r, err := openRotatingLog(path, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"first", "second", "third", "fourth"} {
		if _, err := r.Write([]byte(entry)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		path:        "fourth",
		path + ".1": "third",
		path + ".2": "second",
	} {
		got, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", file, got, want)
		}
		if info, err := os.Stat(file); err != nil || info.Size() > 8 || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: info=%v err=%v", file, info, err)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected extra backup: %v", err)
	}
}

func TestRotatingLogTruncatesSingleOversizedEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.log")
	r, err := openRotatingLog(path, 8, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := r.Write([]byte("abcdefghijkl")); err != nil || n != 12 {
		t.Fatalf("write = %d, %v", n, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "efghijkl" {
		t.Fatalf("oversized entry = %q", got)
	}
}

func TestRotatingLogRecoversAfterBackupFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.log")
	r, err := openRotatingLog(path, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	// A nonempty directory at the backup path makes rotation fail on all
	// platforms, without relying on permission behavior of the test user.
	backup := path + ".1"
	if err := os.Mkdir(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "blocker"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("next")); err == nil {
		t.Fatal("expected backup failure")
	}
	if err := os.RemoveAll(backup); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("next")); err != nil {
		t.Fatalf("write after backup failure: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "next" {
		t.Fatalf("active log = %q", got)
	}
}
