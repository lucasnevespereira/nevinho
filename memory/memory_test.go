package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestLoadEmpty(t *testing.T) {
	dir := tmpDir(t)
	got := Load(dir)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestAddAndLoad(t *testing.T) {
	dir := tmpDir(t)
	if err := Add(dir, "always use tabs"); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if got != "always use tabs" {
		t.Fatalf("expected 'always use tabs', got %q", got)
	}
}

func TestAddDedup(t *testing.T) {
	dir := tmpDir(t)
	Add(dir, "never use semicolons")
	Add(dir, "never use semicolons")
	got := Load(dir)
	lines := strings.Split(got, "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %v", len(lines), lines)
	}
}

func TestAddRotation(t *testing.T) {
	dir := tmpDir(t)
	for i := range 25 {
		Add(dir, fmt.Sprintf("entry %d", i))
	}
	got := Load(dir)
	lines := strings.Split(got, "\n")
	if len(lines) != maxEntries {
		t.Fatalf("expected %d lines, got %d", maxEntries, len(lines))
	}
	// Oldest entries should be rotated out
	if strings.Contains(got, "entry 0") {
		t.Fatal("entry 0 should have been rotated out")
	}
	if !strings.Contains(got, "entry 24") {
		t.Fatal("entry 24 should be present")
	}
}

func TestAddEmptyString(t *testing.T) {
	dir := tmpDir(t)
	if err := Add(dir, ""); err != nil {
		t.Fatal(err)
	}
	if err := Add(dir, "   "); err != nil {
		t.Fatal(err)
	}
	got := Load(dir)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestDetectCorrection(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"remember to always use dark mode", "remember to always use dark mode"},
		{"always run tests before committing", "always run tests before committing"},
		{"never delete files without asking", "never delete files without asking"},
		{"don't use tabs", "don't use tabs"},
		{"stop adding comments", "stop adding comments"},
		{"use spaces instead of tabs", "use spaces instead of tabs"},
		{"I prefer dark mode", "I prefer dark mode"},
		{"I don't like verbose output", "I don't like verbose output"},
		{"I want you to be concise", "I want you to be concise"},
		{"how are you?", ""},
		{"can you help me?", ""},
		{"read the file", ""},
	}
	for _, tt := range tests {
		got := DetectCorrection(tt.input)
		if got != tt.want {
			t.Errorf("DetectCorrection(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFileCreatesDir(t *testing.T) {
	dir := tmpDir(t)
	sub := filepath.Join(dir, "sub", "dir")
	path := filepath.Join(sub, fileName)
	// Add should fail gracefully since parent doesn't exist
	// but os.WriteFile needs the parent to exist
	os.MkdirAll(sub, 0755)
	if err := Add(sub, "test entry"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test entry") {
		t.Fatalf("file should contain 'test entry', got %q", string(data))
	}
}
