package tools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	findDefaultLimit    = 500
	findDefaultMaxDepth = 8
)

type findInput struct {
	Pattern  string `json:"pattern"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	Limit    int    `json:"limit"`
	MaxDepth int    `json:"max_depth"`
}

func (r *Registry) findFiles(ctx context.Context, input json.RawMessage, userID string) Result {
	var in findInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fail("invalid input: %v", err)
	}

	if in.Pattern == "" {
		return fail("pattern is required — e.g. \"*.go\", \"Makefile\"")
	}
	if in.Path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fail("path is required — use an absolute path")
		}
		in.Path = cwd
	}

	resolved, err := resolvePath(in.Path, userID)
	if err != nil {
		return fail("invalid path: %v", err)
	}

	findType := "f"
	if in.Type == "d" {
		findType = "d"
	}

	maxDepth := in.MaxDepth
	if maxDepth <= 0 {
		maxDepth = findDefaultMaxDepth
	}

	args := []string{
		resolved,
		"-maxdepth", strconv.Itoa(maxDepth),
		"-type", findType,
		"-iname", in.Pattern,
		"-not", "-path", "*/.git/*",
		"-not", "-path", "*/node_modules/*",
		"-not", "-path", "*/__pycache__/*",
		"-not", "-path", "*/.venv/*",
		"-not", "-path", "*/Library/*",
		"-not", "-path", "*/.Trash/*",
		"-not", "-path", "*/.cache/*",
	}

	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Discard stderr so permission-denied noise from system trees does not
	// leak into the result the model has to read.
	cmd := exec.CommandContext(tctx, "find", args...)
	cmd.Stderr = io.Discard
	output, err := cmd.Output()
	result := strings.TrimRight(string(output), "\n")

	if err != nil {
		if tctx.Err() == context.DeadlineExceeded {
			return fail("find timed out after 30s — try a more specific path")
		}
		// find may return partial results with errors
		if result == "" {
			return fail("find failed: %v", err)
		}
	}

	if result == "" {
		return ok("no files found matching pattern")
	}

	lines := strings.Split(result, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if rel, err := filepath.Rel(resolved, line); err == nil {
			lines[i] = rel
		} else {
			lines[i] = line
		}
	}

	filtered := lines[:0]
	for _, line := range lines {
		if line != "" {
			filtered = append(filtered, line)
		}
	}
	lines = filtered

	limit := in.Limit
	if limit <= 0 {
		limit = findDefaultLimit
	}
	if len(lines) > limit {
		lines = lines[:limit]
		return okf("%s\n\n[%d results limit reached. Use limit=%d for more, or refine pattern]", strings.Join(lines, "\n"), limit, limit*2)
	}

	return ok(strings.Join(lines, "\n"))
}
