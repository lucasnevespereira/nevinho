package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lucasnevespereira/nevinho/safeio"
)

const (
	fileName  = "memory.md"
	maxEntries = 20
)

// Load reads memory entries from disk. Returns empty string if missing.
func Load(configDir string) string {
	data, err := os.ReadFile(filepath.Join(configDir, fileName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// Add appends an entry and rotates oldest if over cap.
func Add(configDir, entry string) error {
	path := filepath.Join(configDir, fileName)
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil
	}

	existing := Load(configDir)
	var lines []string
	if existing != "" {
		lines = strings.Split(existing, "\n")
	}

	// Don't add duplicates
	for _, l := range lines {
		if l == entry {
			return nil
		}
	}

	lines = append(lines, entry)

	// Rotate oldest entries if over cap
	if len(lines) > maxEntries {
		lines = lines[len(lines)-maxEntries:]
	}

	return safeio.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// correction patterns that indicate user preferences
var correctionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^remember\b.+`),
	regexp.MustCompile(`(?i)^always\b.+`),
	regexp.MustCompile(`(?i)^never\b.+`),
	regexp.MustCompile(`(?i)^don'?t\b.+`),
	regexp.MustCompile(`(?i)^stop\b.+`),
	regexp.MustCompile(`(?i)^use .+ instead of .+`),
	regexp.MustCompile(`(?i)^i prefer\b.+`),
	regexp.MustCompile(`(?i)^i don'?t like\b.+`),
	regexp.MustCompile(`(?i)^i want\b.+you\b.+`),
}

// DetectCorrection checks if user text contains a correction or preference.
// Returns the extracted entry or empty string.
func DetectCorrection(text string) string {
	text = strings.TrimSpace(text)

	// Check each line for correction patterns
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		for _, pat := range correctionPatterns {
			if pat.MatchString(line) {
				return line
			}
		}
	}
	return ""
}
