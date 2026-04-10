package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{"absolute path stays absolute", "/tmp/foo.txt", ""},
		{"home expansion works", "~/test.txt", ""},
		{"relative path rejected", "notes.txt", "relative paths are not supported"},
		{"dot-dot path rejected", "../../etc/passwd", "relative paths are not supported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolvePath(tt.path, "u1")

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.path == "/tmp/foo.txt" && resolved != "/tmp/foo.txt" {
				t.Errorf("absolute path changed: got %q", resolved)
			}
		})
	}
}

// newTestRegistry creates a Registry backed by a temp directory.
func newTestRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	dir := t.TempDir()
	r := &Registry{
		approved: make(map[string]bool),
		pending:  make(map[string]*Pending),
		files:    make(map[string][]FileDisplay),
		permFile: filepath.Join(dir, "approved_paths.json"),
	}
	// Pre-approve the temp dir so writes don't block on approval.
	r.approved[dir] = true
	return r, dir
}

func marshalInput(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestFileReadPagination(t *testing.T) {
	r, dir := newTestRegistry(t)

	// Write a file with 10 numbered lines.
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name            string
		offset          int
		limit           int
		summaryContains string
		displayContains string
		displayExcludes string
	}{
		{"full read", 0, 0, "shown to user", "line 1", ""},
		{"offset only", 5, 0, "lines 5-10", "line 5", "line 4"},
		{"limit only", 0, 3, "lines 1-3", "line 1", "line 4"},
		{"offset and limit", 3, 2, "lines 3-4", "line 3", "line 1"},
		{"offset beyond end", 99, 0, "offset 99 exceeds", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := marshalInput(t, map[string]any{
				"path":   path,
				"offset": tt.offset,
				"limit":  tt.limit,
			})
			got := r.fileRead(input, "u1")
			if !strings.Contains(got, tt.summaryContains) {
				t.Errorf("summary missing %q\ngot: %s", tt.summaryContains, got)
			}
			displays := r.DrainFileDisplays("u1")
			if tt.displayContains != "" {
				if len(displays) == 0 {
					t.Fatal("expected file display but got none")
				}
				if !strings.Contains(displays[0].Content, tt.displayContains) {
					t.Errorf("display missing %q", tt.displayContains)
				}
				if tt.displayExcludes != "" && strings.Contains(displays[0].Content, tt.displayExcludes) {
					t.Errorf("display should not contain %q", tt.displayExcludes)
				}
			}
		})
	}
}

func TestFileList(t *testing.T) {
	r, dir := newTestRegistry(t)

	// Create a sub-dir and a file inside the temp dir.
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	input := marshalInput(t, map[string]string{"path": dir})
	got := r.fileList(input, "u1")

	if !strings.Contains(got, "subdir/") {
		t.Errorf("expected directory entry 'subdir/'\ngot: %s", got)
	}
	if !strings.Contains(got, "hello.txt") {
		t.Errorf("expected file entry 'hello.txt'\ngot: %s", got)
	}
}

func TestFileListRequiresPath(t *testing.T) {
	r, _ := newTestRegistry(t)
	input := marshalInput(t, map[string]string{})
	got := r.fileList(input, "u1")
	if !strings.Contains(got, "path is required") {
		t.Errorf("expected path required error, got: %s", got)
	}
}

func TestFileEdit(t *testing.T) {
	r, dir := newTestRegistry(t)
	path := filepath.Join(dir, "edit_me.txt")

	initial := "hello world\nfoo bar\n"
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("successful edit", func(t *testing.T) {
		input := marshalInput(t, map[string]string{
			"path":     path,
			"old_text": "foo bar",
			"new_text": "baz qux",
		})
		got := r.fileEdit(input, "u1")
		if !strings.Contains(got, "edited") {
			t.Errorf("expected success message, got: %s", got)
		}
		if !strings.Contains(got, "--- before") || !strings.Contains(got, "+++ after") {
			t.Errorf("expected diff output, got: %s", got)
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "baz qux") {
			t.Errorf("file content not updated: %s", string(data))
		}
	})

	t.Run("old_text not found", func(t *testing.T) {
		input := marshalInput(t, map[string]string{
			"path":     path,
			"old_text": "does not exist",
			"new_text": "replacement",
		})
		got := r.fileEdit(input, "u1")
		if !strings.Contains(got, "not found") {
			t.Errorf("expected 'not found', got: %s", got)
		}
	})

	t.Run("old_text matches multiple times", func(t *testing.T) {
		_ = os.WriteFile(path, []byte("x x x"), 0644)
		input := marshalInput(t, map[string]string{
			"path":     path,
			"old_text": "x",
			"new_text": "y",
		})
		got := r.fileEdit(input, "u1")
		if !strings.Contains(got, "3 times") {
			t.Errorf("expected ambiguity error, got: %s", got)
		}
	})
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{500, "500B"},
		{1536, "1.5KB"},
		{2 * 1024 * 1024, "2.0MB"},
	}
	for _, tt := range tests {
		got := formatSize(tt.bytes)
		if got != tt.want {
			t.Errorf("formatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
