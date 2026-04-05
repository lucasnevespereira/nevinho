package tools

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		userID  string
		wantErr string
	}{
		{"absolute path stays absolute", "/tmp/foo.txt", "u1", ""},
		{"home expansion works", "~/test.txt", "u1", ""},
		{"relative path goes to workspace", "notes.txt", "u1", ""},
		{"path traversal is blocked", "../../etc/passwd", "u1", "traversal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, err := resolvePath(tt.path, tt.userID)

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

			if !filepath.IsAbs(tt.path) && tt.path != "../../etc/passwd" && !strings.HasPrefix(tt.path, "~/") {
				wsDir := filepath.Join(configDir(), "workspace", tt.userID)
				if !strings.HasPrefix(resolved, wsDir) {
					t.Errorf("relative path %q resolved outside workspace: %q", tt.path, resolved)
				}
			}
		})
	}
}

func TestIsWorkspacePath(t *testing.T) {
	ws := filepath.Join(configDir(), "workspace", "u1", "file.txt")
	if !isWorkspacePath(ws) {
		t.Errorf("expected %q to be a workspace path", ws)
	}

	if isWorkspacePath("/etc/passwd") {
		t.Error("expected /etc/passwd to NOT be a workspace path")
	}
}
