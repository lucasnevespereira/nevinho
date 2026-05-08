package safeio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	if err := WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestWriteFilePreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")

	if err := os.WriteFile(path, []byte("orig"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 (preserved)", info.Mode().Perm())
	}
}

func TestWriteFilePreservesExecBit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.sh")

	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755 (preserved)", info.Mode().Perm())
	}
}

func TestWriteFileCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "file.txt")

	if err := WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("file missing: %v", err)
	}
}

func TestWriteFileNoTempLeftOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.txt")

	if err := WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		if name == "ok.txt" {
			continue
		}
		t.Errorf("leftover file in dir: %s", name)
	}
}
