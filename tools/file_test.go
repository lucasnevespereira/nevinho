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
		name     string
		offset   int
		limit    int
		contains string
		excludes string
	}{
		{"full read", 0, 0, "line 1", ""},
		{"offset only", 5, 0, "line 5", "line 4"},
		{"limit only", 0, 3, "line 1", "line 4"},
		{"offset and limit", 3, 2, "line 3", "line 1"},
		{"offset beyond end", 99, 0, "offset 99 exceeds", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := marshalInput(t, map[string]any{
				"path":   path,
				"offset": tt.offset,
				"limit":  tt.limit,
			})
			got := r.fileRead(input, "u1")
			if !strings.Contains(got, tt.contains) {
				t.Errorf("expected %q in result\ngot: %s", tt.contains, got)
			}
			if tt.excludes != "" && strings.Contains(got, tt.excludes) {
				t.Errorf("should not contain %q\ngot: %s", tt.excludes, got)
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
		if !strings.Contains(got, "1 block replaced") {
			t.Errorf("expected block count, got: %s", got)
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "baz qux") {
			t.Errorf("file content not updated: %s", string(data))
		}
		// Verify diff was queued
		displays := r.DrainFileDisplays("u1")
		if len(displays) == 0 {
			t.Fatal("expected diff display to be queued")
		}
		if displays[0].Lang != "diff" {
			t.Errorf("expected diff lang, got: %s", displays[0].Lang)
		}
	})

	t.Run("old_text not found", func(t *testing.T) {
		input := marshalInput(t, map[string]string{
			"path":     path,
			"old_text": "does not exist",
			"new_text": "replacement",
		})
		got := r.fileEdit(input, "u1")
		if !strings.Contains(got, "Could not find") {
			t.Errorf("expected 'Could not find', got: %s", got)
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
		if !strings.Contains(got, "3 occurrences") {
			t.Errorf("expected ambiguity error, got: %s", got)
		}
	})
}

func TestFileEditMultiEdit(t *testing.T) {
	r, dir := newTestRegistry(t)
	path := filepath.Join(dir, "multi.txt")

	_ = os.WriteFile(path, []byte("aaa\nbbb\nccc\nddd\neee\n"), 0644)

	t.Run("two edits", func(t *testing.T) {
		input := marshalInput(t, map[string]any{
			"path": path,
			"edits": []map[string]string{
				{"old_text": "bbb", "new_text": "BBB"},
				{"old_text": "ddd", "new_text": "DDD"},
			},
		})
		got := r.fileEdit(input, "u1")
		if !strings.Contains(got, "2 blocks replaced") {
			t.Errorf("expected 2 blocks, got: %s", got)
		}
		data, _ := os.ReadFile(path)
		content := string(data)
		if !strings.Contains(content, "BBB") || !strings.Contains(content, "DDD") {
			t.Errorf("edits not applied: %s", content)
		}
		if !strings.Contains(content, "aaa") || !strings.Contains(content, "ccc") || !strings.Contains(content, "eee") {
			t.Errorf("untouched lines changed: %s", content)
		}
	})

	t.Run("overlap rejected", func(t *testing.T) {
		_ = os.WriteFile(path, []byte("abc def ghi"), 0644)
		input := marshalInput(t, map[string]any{
			"path": path,
			"edits": []map[string]string{
				{"old_text": "abc def", "new_text": "X"},
				{"old_text": "def ghi", "new_text": "Y"},
			},
		})
		got := r.fileEdit(input, "u1")
		if !strings.Contains(got, "overlap") {
			t.Errorf("expected overlap error, got: %s", got)
		}
	})
}

func TestFileEditFuzzyMatch(t *testing.T) {
	r, dir := newTestRegistry(t)
	path := filepath.Join(dir, "fuzzy.txt")

	t.Run("trailing whitespace", func(t *testing.T) {
		// File has trailing spaces, model's old_text doesn't
		_ = os.WriteFile(path, []byte("hello world   \nfoo bar  \n"), 0644)
		input := marshalInput(t, map[string]string{
			"path":     path,
			"old_text": "hello world\nfoo bar",
			"new_text": "goodbye\nfoo baz",
		})
		got := r.fileEdit(input, "u1")
		if !strings.Contains(got, "edited") {
			t.Errorf("expected fuzzy match success, got: %s", got)
		}
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "goodbye") {
			t.Errorf("fuzzy edit not applied: %s", string(data))
		}
	})

	t.Run("smart quotes", func(t *testing.T) {
		// File has smart quotes, model sends ASCII quotes
		_ = os.WriteFile(path, []byte("say \u201Chello\u201D\n"), 0644)
		input := marshalInput(t, map[string]string{
			"path":     path,
			"old_text": "say \"hello\"",
			"new_text": "say \"world\"",
		})
		got := r.fileEdit(input, "u1")
		if !strings.Contains(got, "edited") {
			t.Errorf("expected fuzzy match on smart quotes, got: %s", got)
		}
	})

	t.Run("unicode dashes", func(t *testing.T) {
		// File has em dash, model sends ASCII hyphen
		_ = os.WriteFile(path, []byte("value \u2014 100\n"), 0644)
		input := marshalInput(t, map[string]string{
			"path":     path,
			"old_text": "value - 100",
			"new_text": "value = 200",
		})
		got := r.fileEdit(input, "u1")
		if !strings.Contains(got, "edited") {
			t.Errorf("expected fuzzy match on dashes, got: %s", got)
		}
	})
}

func TestFileEditBOM(t *testing.T) {
	r, dir := newTestRegistry(t)
	path := filepath.Join(dir, "bom.txt")

	// Write file with BOM
	_ = os.WriteFile(path, []byte("\xef\xbb\xbfhello world\n"), 0644)

	input := marshalInput(t, map[string]string{
		"path":     path,
		"old_text": "hello world",
		"new_text": "goodbye world",
	})
	got := r.fileEdit(input, "u1")
	if !strings.Contains(got, "edited") {
		t.Errorf("expected success with BOM, got: %s", got)
	}
	data, _ := os.ReadFile(path)
	// BOM should be preserved
	if !strings.HasPrefix(string(data), "\xef\xbb\xbf") {
		t.Error("BOM was not preserved")
	}
	if !strings.Contains(string(data), "goodbye world") {
		t.Errorf("edit not applied: %s", string(data))
	}
}

func TestFileEditCRLF(t *testing.T) {
	r, dir := newTestRegistry(t)
	path := filepath.Join(dir, "crlf.txt")

	// Write file with CRLF line endings
	_ = os.WriteFile(path, []byte("line one\r\nline two\r\nline three\r\n"), 0644)

	input := marshalInput(t, map[string]string{
		"path":     path,
		"old_text": "line two",
		"new_text": "LINE TWO",
	})
	got := r.fileEdit(input, "u1")
	if !strings.Contains(got, "edited") {
		t.Errorf("expected success with CRLF, got: %s", got)
	}
	data, _ := os.ReadFile(path)
	content := string(data)
	// CRLF should be preserved
	if !strings.Contains(content, "\r\n") {
		t.Error("CRLF line endings not preserved")
	}
	if !strings.Contains(content, "LINE TWO") {
		t.Errorf("edit not applied: %s", content)
	}
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
