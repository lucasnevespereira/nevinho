package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	summariesDir   = "summaries"
	summaryMaxSize = 4000 // ~1000 tokens, generous cap
)

// summaryPath returns the on-disk location for a user's persisted summary.
// The userID is sanitized to prevent path traversal even though Discord IDs
// are numeric snowflakes; we don't trust callers.
func summaryPath(configDir, userID string) (string, error) {
	clean := sanitizeUserID(userID)
	if clean == "" {
		return "", fmt.Errorf("invalid user id")
	}
	return filepath.Join(configDir, summariesDir, clean+".md"), nil
}

func sanitizeUserID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.ContainsAny(id, `/\.`) {
		return ""
	}
	return filepath.Base(id)
}

// loadSummary reads a persisted summary for the user. Returns "" if missing.
func loadSummary(configDir, userID string) string {
	path, err := summaryPath(configDir, userID)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// saveSummary writes the summary atomically: write to .tmp then rename so a
// crash mid-write can't leave a half-written file.
func saveSummary(configDir, userID, text string) error {
	path, err := summaryPath(configDir, userID)
	if err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return deleteSummary(configDir, userID)
	}
	if len(text) > summaryMaxSize {
		text = text[:summaryMaxSize]
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create summaries dir: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text+"\n"), 0644); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename summary: %w", err)
	}
	return nil
}

// deleteSummary removes the persisted summary. Missing file is not an error.
func deleteSummary(configDir, userID string) error {
	path, err := summaryPath(configDir, userID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
