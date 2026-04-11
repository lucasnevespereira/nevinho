package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrepSearch(t *testing.T) {
	r, dir := newTestRegistry(t)

	// Create test files
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "util.go"), []byte("package main\n\nfunc helper() string {\n\treturn \"hello world\"\n}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Project\n\nThis is a test project.\n"), 0644)

	t.Run("basic match", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "hello",
			"path":    dir,
		})
		got := r.grepSearch(context.Background(), input, "u1")
		if !strings.Contains(got, "hello") {
			t.Errorf("expected matches, got: %s", got)
		}
		if strings.Contains(got, dir) {
			t.Errorf("expected relative paths, got absolute: %s", got)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern":     "HELLO",
			"path":        dir,
			"ignore_case": true,
		})
		got := r.grepSearch(context.Background(), input, "u1")
		if !strings.Contains(got, "hello") {
			t.Errorf("expected case-insensitive match, got: %s", got)
		}
	})

	t.Run("glob filter", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "hello",
			"path":    dir,
			"glob":    "*.go",
		})
		got := r.grepSearch(context.Background(), input, "u1")
		if !strings.Contains(got, "hello") {
			t.Errorf("expected go file matches, got: %s", got)
		}
		// Should not match readme.md (no "hello" in it anyway, but glob should filter)
	})

	t.Run("no matches", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "nonexistent_string_xyz",
			"path":    dir,
		})
		got := r.grepSearch(context.Background(), input, "u1")
		if !strings.Contains(got, "no matches") {
			t.Errorf("expected 'no matches', got: %s", got)
		}
	})

	t.Run("limit enforcement", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "main",
			"path":    dir,
			"limit":   1,
		})
		got := r.grepSearch(context.Background(), input, "u1")
		lines := strings.Split(strings.TrimSpace(got), "\n")
		// Should have at most 1 match line + truncation notice
		matchLines := 0
		for _, line := range lines {
			if strings.Contains(line, ":") && !strings.HasPrefix(line, "[") {
				matchLines++
			}
		}
		if matchLines > 1 {
			t.Errorf("expected at most 1 match line, got %d: %s", matchLines, got)
		}
	})

	t.Run("missing pattern", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"path": dir,
		})
		got := r.grepSearch(context.Background(), input, "u1")
		if !strings.Contains(got, "pattern is required") {
			t.Errorf("expected validation error, got: %s", got)
		}
	})

	t.Run("missing path", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "test",
		})
		got := r.grepSearch(context.Background(), input, "u1")
		if !strings.Contains(got, "path is required") {
			t.Errorf("expected validation error, got: %s", got)
		}
	})
}
