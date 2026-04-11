package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindFiles(t *testing.T) {
	r, dir := newTestRegistry(t)

	// Create test directory structure
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.MkdirAll(filepath.Join(dir, "src", "pkg"), 0755)
	os.MkdirAll(filepath.Join(dir, ".git", "objects"), 0755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "dep"), 0755)
	os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "pkg", "util.go"), []byte("package pkg"), 0644)
	os.WriteFile(filepath.Join(dir, "src", "style.css"), []byte("body{}"), 0644)
	os.WriteFile(filepath.Join(dir, ".git", "objects", "config.go"), []byte("git"), 0644)
	os.WriteFile(filepath.Join(dir, "node_modules", "dep", "index.go"), []byte("dep"), 0644)

	t.Run("find go files", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "*.go",
			"path":    dir,
		})
		got := r.findFiles(context.Background(), input, "u1")
		if !strings.Contains(got, "main.go") {
			t.Errorf("expected main.go, got: %s", got)
		}
		if !strings.Contains(got, "util.go") {
			t.Errorf("expected util.go, got: %s", got)
		}
		// Should NOT include .git or node_modules
		if strings.Contains(got, ".git") {
			t.Errorf("should exclude .git, got: %s", got)
		}
		if strings.Contains(got, "node_modules") {
			t.Errorf("should exclude node_modules, got: %s", got)
		}
	})

	t.Run("find css files", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "*.css",
			"path":    dir,
		})
		got := r.findFiles(context.Background(), input, "u1")
		if !strings.Contains(got, "style.css") {
			t.Errorf("expected style.css, got: %s", got)
		}
		if strings.Contains(got, ".go") {
			t.Errorf("should not include .go files, got: %s", got)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "*.xyz",
			"path":    dir,
		})
		got := r.findFiles(context.Background(), input, "u1")
		if !strings.Contains(got, "no files found") {
			t.Errorf("expected 'no files found', got: %s", got)
		}
	})

	t.Run("relative paths", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "*.go",
			"path":    dir,
		})
		got := r.findFiles(context.Background(), input, "u1")
		// Paths should be relative, not absolute
		if strings.Contains(got, dir) {
			t.Errorf("expected relative paths, got absolute: %s", got)
		}
	})

	t.Run("limit enforcement", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"pattern": "*.go",
			"path":    dir,
			"limit":   1,
		})
		got := r.findFiles(context.Background(), input, "u1")
		if !strings.Contains(got, "limit reached") {
			t.Errorf("expected limit notice, got: %s", got)
		}
	})

	t.Run("missing pattern", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"path": dir,
		})
		got := r.findFiles(context.Background(), input, "u1")
		if !strings.Contains(got, "pattern is required") {
			t.Errorf("expected validation error, got: %s", got)
		}
	})
}
