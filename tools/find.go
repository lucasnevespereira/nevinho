package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const findDefaultLimit = 500

type findInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Limit   int    `json:"limit"`
}

func (r *Registry) findFiles(input json.RawMessage, userID string) string {
	var in findInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if in.Pattern == "" {
		return "pattern is required — e.g. \"*.go\", \"Makefile\""
	}
	if in.Path == "" {
		return "path is required — use an absolute path"
	}

	resolved, err := resolvePath(in.Path, userID)
	if err != nil {
		return fmt.Sprintf("invalid path: %v", err)
	}

	args := []string{
		resolved,
		"-type", "f",
		"-name", in.Pattern,
		"-not", "-path", "*/.git/*",
		"-not", "-path", "*/node_modules/*",
		"-not", "-path", "*/__pycache__/*",
		"-not", "-path", "*/.venv/*",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "find", args...)
	output, err := cmd.CombinedOutput()
	result := strings.TrimRight(string(output), "\n")

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "find timed out after 30s — try a more specific path"
		}
		// find may print partial results + errors; return what we have
		if result == "" {
			return fmt.Sprintf("find failed: %v", err)
		}
	}

	if result == "" {
		return "no files found matching pattern"
	}

	// Convert to relative paths
	lines := strings.Split(result, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if rel, err := filepath.Rel(resolved, line); err == nil {
			lines[i] = rel
		} else {
			lines[i] = line
		}
	}

	// Remove empty lines
	filtered := lines[:0]
	for _, line := range lines {
		if line != "" {
			filtered = append(filtered, line)
		}
	}
	lines = filtered

	// Apply limit
	limit := in.Limit
	if limit <= 0 {
		limit = findDefaultLimit
	}
	if len(lines) > limit {
		lines = lines[:limit]
		return strings.Join(lines, "\n") + fmt.Sprintf("\n\n[%d results limit reached. Use limit=%d for more, or refine pattern]", limit, limit*2)
	}

	return strings.Join(lines, "\n")
}
