package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucasnevespereira/nevinho/safeio"
)

const (
	summariesDir   = "summaries"
	summaryMaxSize = 4000 // ~1000 tokens
)

// summaryPath returns the on-disk location for a user's persisted summary.
// The userID is sanitized to prevent path traversal even though Discord IDs
// are numeric snowflakes. We don't trust callers.
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

// saveSummary writes the summary atomically through safeio.WriteFile so a
// crash mid write cannot leave a half written file.
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
	if err := safeio.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		return fmt.Errorf("write summary: %w", err)
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
