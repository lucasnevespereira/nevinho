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

const (
	grepTimeout       = 30 * time.Second
	grepDefaultLimit  = 100
	grepMaxLineLength = 500
)

type grepInput struct {
	Pattern      string `json:"pattern"`
	Path         string `json:"path"`
	Glob         string `json:"glob"`
	IgnoreCase   bool   `json:"ignore_case"`
	ContextLines int    `json:"context_lines"`
	Limit        int    `json:"limit"`
}

func (r *Registry) grepSearch(ctx context.Context, input json.RawMessage, userID string) string {
	var in grepInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Sprintf("invalid input: %v", err)
	}

	if in.Pattern == "" {
		return "pattern is required"
	}
	if in.Path == "" {
		return "path is required — use an absolute path"
	}

	resolved, err := resolvePath(in.Path, userID)
	if err != nil {
		return fmt.Sprintf("invalid path: %v", err)
	}

	args := []string{"-rn", "--binary-files=without-match"}
	if in.IgnoreCase {
		args = append(args, "-i")
	}
	if in.ContextLines > 0 {
		args = append(args, fmt.Sprintf("-C%d", in.ContextLines))
	}
	if in.Glob != "" {
		args = append(args, "--include="+in.Glob)
	}
	args = append(args, "--", in.Pattern, resolved)

	tctx, cancel := context.WithTimeout(ctx, grepTimeout)
	defer cancel()

	cmd := exec.CommandContext(tctx, "grep", args...)
	output, err := cmd.CombinedOutput()
	result := strings.TrimRight(string(output), "\n")

	if err != nil {
		if tctx.Err() == context.DeadlineExceeded {
			return "grep timed out after 30s — try a more specific pattern or path"
		}
		// grep exits 1 when no matches found
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "no matches found"
		}
		if result != "" {
			return result
		}
		return fmt.Sprintf("grep failed: %v", err)
	}

	if result == "" {
		return "no matches found"
	}

	// Make paths relative to the search directory
	result = makePathsRelative(result, resolved)

	// Truncate long lines
	lines := strings.Split(result, "\n")
	truncatedLines := false
	for i, line := range lines {
		if len(line) > grepMaxLineLength {
			lines[i] = line[:grepMaxLineLength] + "...(truncated)"
			truncatedLines = true
		}
	}

	// Apply match limit
	limit := in.Limit
	if limit <= 0 {
		limit = grepDefaultLimit
	}
	if len(lines) > limit {
		lines = lines[:limit]
		result = strings.Join(lines, "\n") + fmt.Sprintf("\n\n[%d matches limit reached. Use limit=%d for more, or refine pattern]", limit, limit*2)
	} else {
		result = strings.Join(lines, "\n")
	}

	if truncatedLines {
		result += fmt.Sprintf("\n[Some lines truncated to %d chars]", grepMaxLineLength)
	}

	return result
}

func makePathsRelative(output, basePath string) string {
	// Ensure basePath ends with separator for clean prefix removal
	if !strings.HasSuffix(basePath, string(filepath.Separator)) {
		basePath += string(filepath.Separator)
	}
	return strings.ReplaceAll(output, basePath, "")
}
