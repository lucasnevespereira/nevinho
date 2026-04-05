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
	Path   string `json:"path"`
	Offset int    `json:"offset"` // 1-indexed line to start from
	Limit  int    `json:"limit"`  // max lines to return
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
	lang := langFromExt(resolved)
	totalLines := strings.Count(content, "\n") + 1

	// Paginated read: offset/limit work on lines.
	if in.Offset > 0 || in.Limit > 0 {
		lines := strings.Split(content, "\n")

		start := 0
		if in.Offset > 0 {
			start = in.Offset - 1
		}
		if start >= totalLines {
			return fmt.Sprintf("offset %d exceeds file length (%d lines)", in.Offset, totalLines)
		}

		end := totalLines
		if in.Limit > 0 && start+in.Limit < totalLines {
			end = start + in.Limit
		}

		chunk := strings.Join(lines[start:end], "\n")
		r.QueueFileDisplay(userID, in.Path, lang, chunk)
		return fmt.Sprintf("lines %d-%d of %d shown to user", start+1, end, totalLines)
	}

	// Full read with truncation.
	display := content
	if len(display) > maxResponseLen {
		display = display[:maxResponseLen]
		if idx := strings.LastIndex(display, "\n"); idx != -1 {
			display = display[:idx]
		}
	}

	r.QueueFileDisplay(userID, in.Path, lang, display)
	return fmt.Sprintf("%s (%d lines) shown to user", in.Path, totalLines)
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

	if err := r.checkWritePermission(resolved, userID); err != nil {
		return err.Error()
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

type fileListInput struct {
	Path string `json:"path"`
}

func (r *Registry) fileList(input json.RawMessage, userID string) string {
	var in fileListInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if in.Path == "" {
		return "path is required — use an absolute path"
	}

	dirPath, err := resolvePath(in.Path, userID)
	if err != nil {
		return fmt.Sprintf("invalid path: %v", err)
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Sprintf("failed to list: %v", err)
	}

	if len(entries) == 0 {
		return fmt.Sprintf("%s (empty)", shortenHome(dirPath))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%s\n", shortenHome(dirPath))
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&sb, "  %s/\n", e.Name())
		} else {
			info, _ := e.Info()
			if info != nil {
				fmt.Fprintf(&sb, "  %s (%s)\n", e.Name(), formatSize(info.Size()))
			} else {
				fmt.Fprintf(&sb, "  %s\n", e.Name())
			}
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatSize(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
}

type fileEditInput struct {
	Path    string `json:"path"`
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

func (r *Registry) fileEdit(input json.RawMessage, userID string) string {
	var in fileEditInput
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
	count := strings.Count(content, in.OldText)
	switch count {
	case 0:
		return "old_text not found in file"
	case 1:
		// good
	default:
		return fmt.Sprintf("old_text found %d times — make it more specific", count)
	}

	if err := r.checkWritePermission(resolved, userID); err != nil {
		return err.Error()
	}

	newContent := strings.Replace(content, in.OldText, in.NewText, 1)
	if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
		return fmt.Sprintf("failed to write: %v", err)
	}

	return fmt.Sprintf("edited %s", in.Path)
}

func langFromExt(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "dockerfile":
		return "dockerfile"
	case "makefile":
		return "makefile"
	}
	ext := strings.TrimPrefix(filepath.Ext(base), ".")
	switch ext {
	case "sh", "zsh":
		return "bash"
	case "yml":
		return "yaml"
	case "mod":
		return "go"
	case "":
		return ""
	default:
		return ext
	}
}

func resolvePath(path, _ string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		return expandHome(path)
	}

	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	return "", fmt.Errorf("relative paths are not supported — use an absolute path")
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
