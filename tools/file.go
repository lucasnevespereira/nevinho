package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxFileSize = 100 * 1024 // 100KB

type fileReadInput struct {
	Path string `json:"path"`
}

func (r *Registry) fileRead(input json.RawMessage, userID string) string {
	var in fileReadInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	resolved, err := resolvePath(in.Path, userID)
	if err != nil {
		return fmt.Sprintf("invalid path: %v", err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("file not found: %s", in.Path)
		}
		return fmt.Sprintf("failed to read: %v", err)
	}

	content := string(data)
	if len(content) > maxResponseLen {
		content = content[:maxResponseLen] + "\n...(truncated)"
	}

	return content
}

type fileWriteInput struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (r *Registry) fileWrite(input json.RawMessage, userID string) string {
	var in fileWriteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if len(in.Content) > maxFileSize {
		return fmt.Sprintf("content too large (max %dKB)", maxFileSize/1024)
	}

	resolved, err := resolvePath(in.Path, userID)
	if err != nil {
		return fmt.Sprintf("invalid path: %v", err)
	}

	if !isWorkspacePath(resolved) {
		if err := r.checkWritePermission(resolved, userID); err != nil {
			return err.Error()
		}
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Sprintf("failed to create directory: %v", err)
	}

	if err := os.WriteFile(resolved, []byte(in.Content), 0644); err != nil {
		return fmt.Sprintf("failed to write: %v", err)
	}

	return fmt.Sprintf("saved to %s", in.Path)
}

// --- path helpers ---

func resolvePath(path, userID string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		resolved, err := expandHome(path)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}

	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	// Relative paths go to per-user workspace
	base := filepath.Join(configDir(), "workspace", userID)
	full := filepath.Join(base, filepath.Clean(path))

	absBase, _ := filepath.Abs(base)
	absFull, _ := filepath.Abs(full)
	if !strings.HasPrefix(absFull, absBase) {
		return "", fmt.Errorf("path traversal blocked")
	}

	return full, nil
}

func expandHome(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot resolve home dir: %w", err)
		}
		return filepath.Join(home, path[2:]), nil
	}
	return filepath.Clean(path), nil
}

func isWorkspacePath(path string) bool {
	abs, _ := filepath.Abs(path)
	wsAbs, _ := filepath.Abs(filepath.Join(configDir(), "workspace"))
	return strings.HasPrefix(abs, wsAbs)
}

func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}
